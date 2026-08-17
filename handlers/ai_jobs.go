package handlers

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
)

// AI generation job statuses.
const (
	aiStatusQueued     = "queued"
	aiStatusPlanning   = "planning"   // session 1: strategist picks the topic
	aiStatusWriting    = "writing"    // session 2: writer produces the post
	aiStatusPublishing = "publishing" // auto-publish path: saving to the store
	aiStatusPublished  = "published"  // auto-publish path: done + live
	aiStatusDone       = "done"       // admin path: ready to load into the editor
	aiStatusFailed     = "failed"
)

// AIGenJob is one run of the two-session AI pipeline. Jobs run in a
// background goroutine so the ping endpoint and the admin dashboard always
// respond immediately (the previous synchronous implementation left the
// admin UI stuck on "Generating…" when the provider was slow). Every state
// transition is also persisted to the store so the admin dashboard can show
// the run history.
type AIGenJob struct {
	ID         string
	Status     string
	Stage      string // human-readable progress text
	Model      string // model selected for this run
	TopicHint  string // optional topic from the caller
	Publish    bool   // auto-publish after writing (ping endpoint) vs. draft (admin)
	Result     *AIBlogResult
	Err        string
	PostID     int64
	CreatedAt  time.Time
	FinishedAt time.Time
}

// aiJobRegistry holds in-flight AI generation jobs. It is process-local —
// fine here because the app runs as a single container with SQLite on disk
// (the same assumption the rest of the app already makes).
type aiJobRegistry struct {
	mu    sync.Mutex
	jobs  map[string]*AIGenJob
	order []string // insertion order, oldest first, for cleanup
}

func newAIJobRegistry() *aiJobRegistry {
	return &aiJobRegistry{jobs: map[string]*AIGenJob{}}
}

// register adds a job and prunes entries older than one hour.
func (r *aiJobRegistry) register(j *AIGenJob) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	// Prune expired jobs (keep a small live window).
	keep := r.order[:0]
	for _, id := range r.order {
		if j, ok := r.jobs[id]; ok && j.CreatedAt.After(cutoff) {
			keep = append(keep, id)
		} else {
			delete(r.jobs, id)
		}
	}
	r.order = keep
	r.jobs[j.ID] = j
	r.order = append(r.order, j.ID)
}

// get returns a copy of a job, or nil.
func (r *aiJobRegistry) get(id string) *AIGenJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return nil
	}
	cp := *j
	if j.Result != nil {
		res := *j.Result
		cp.Result = &res
	}
	return &cp
}

// update applies fn to the live job under the lock.
func (r *aiJobRegistry) update(id string, fn func(*AIGenJob)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if j, ok := r.jobs[id]; ok {
		fn(j)
	}
}

// setStage updates the human-readable progress line for a job.
func (r *aiJobRegistry) setStage(id, status, stage string) {
	r.update(id, func(j *AIGenJob) {
		j.Status = status
		j.Stage = stage
	})
}

// startAIGenJob validates the AI configuration and launches the pipeline in
// a background goroutine. It returns the job ID immediately.
func (s *Server) startAIGenJob(ctx context.Context, topic, model string, publish bool) (string, error) {
	cfg := s.DefaultAIConfig(ctx)
	if cfg.APIKey == "" {
		return "", errAIAPIKeyMissing()
	}
	if model != "" {
		cfg.Model = model
	}

	job := &AIGenJob{
		ID:        randomHexToken(16),
		Status:    aiStatusQueued,
		Stage:     "Preparing…",
		Model:     cfg.Model,
		TopicHint: topic,
		Publish:   publish,
		CreatedAt: time.Now().UTC(),
	}
	s.aiJobs.register(job)
	s.persistAIGenJob(job)

	go s.runAIGenJob(job, cfg)
	return job.ID, nil
}

// runAIGenJob executes the two-session pipeline for a job:
//  1. Session 1 (strategist) reviews the blog history and picks one topic —
//     unless the caller already supplied a concrete topic.
//  2. Session 2 (writer) starts a brand-new chat that only knows the chosen
//     topic and writes the full post.
//  3. Publish automatically (ping path) or leave it for the admin to load.
func (s *Server) runAIGenJob(job *AIGenJob, cfg AIConfig) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	fail := func(err error) {
		log.Printf("ai job %s failed: %v", job.ID, err)
		s.aiJobs.update(job.ID, func(j *AIGenJob) {
			j.Status = aiStatusFailed
			j.Stage = "Failed"
			j.Err = err.Error()
			j.FinishedAt = time.Now().UTC()
		})
		s.persistAIGenJob(s.aiJobs.get(job.ID))
	}

	client := NewAIClient(cfg)

	// ------------------------------------------------------------------
	// Session 1 — pick the topic from the blog history.
	// ------------------------------------------------------------------
	history, err := s.Store.ListPosts(ctx, false)
	if err != nil {
		fail(err)
		return
	}

	var plan *AIBlogPlan
	topicHint := job.TopicHint
	if strings.TrimSpace(topicHint) == "" {
		if len(history) == 0 {
			fail(errNoHistoryOrTopic())
			return
		}
		s.setJobStage(job.ID, aiStatusPlanning, "Agent 1 — reviewing your blog history and picking the best next topic…")
		plan, err = client.SuggestNextTopic(ctx, history, "")
		if err != nil {
			fail(err)
			return
		}
	} else {
		// Caller already decided the topic: keep one session, straight to the writer.
		plan = &AIBlogPlan{Topic: topicHint, Title: prettyTopicTitle(topicHint)}
	}

	// ------------------------------------------------------------------
	// Session 2 — brand-new chat, writes the full post.
	// ------------------------------------------------------------------
	s.setJobStage(job.ID, aiStatusWriting, "Agent 2 — writing the full blog post in a fresh session…")
	result, err := client.WriteBlogPost(ctx, *plan)
	if err != nil {
		fail(err)
		return
	}
	// The strategist's tags carry over when the writer did not emit any.
	if len(result.Tags) == 0 && len(plan.Tags) > 0 {
		result.Tags = plan.Tags
	}

	// ------------------------------------------------------------------
	// Publish (or hand the draft to the admin).
	// ------------------------------------------------------------------
	if !job.Publish {
		s.aiJobs.update(job.ID, func(j *AIGenJob) {
			j.Status = aiStatusDone
			j.Stage = "Ready"
			j.Result = result
			j.FinishedAt = time.Now().UTC()
		})
		s.persistAIGenJob(s.aiJobs.get(job.ID))
		return
	}

	s.setJobStage(job.ID, aiStatusPublishing, "Publishing the post…")
	slug := s.uniquePostSlug(ctx, result.Slug)
	now := time.Now().UTC()
	id, err := s.Store.CreatePost(ctx, store.Post{
		Slug:        slug,
		Title:       result.Title,
		Summary:     result.Summary,
		Body:        result.Body,
		Tags:        result.Tags,
		Published:   true,
		PublishedAt: now,
		UpdatedAt:   now,
	})
	if err != nil {
		fail(err)
		return
	}
	result.Slug = slug
	s.TriggerBackup(ctx)
	s.NotifySearchEngines()

	s.aiJobs.update(job.ID, func(j *AIGenJob) {
		j.Status = aiStatusPublished
		j.Stage = "Published"
		j.Result = result
		j.PostID = id
		j.FinishedAt = time.Now().UTC()
	})
	s.persistAIGenJob(s.aiJobs.get(job.ID))
	log.Printf("ai job %s published post %q (%s)", job.ID, result.Title, slug)
}

// setJobStage advances a job's progress line in the live registry and
// mirrors the change to the persisted run history.
func (s *Server) setJobStage(id, status, stage string) {
	s.aiJobs.setStage(id, status, stage)
	s.persistAIGenJob(s.aiJobs.get(id))
}

// persistAIGenJob writes the job's current state into the store so the
// dashboard can show the run history. Failures are logged, never fatal —
// the live registry keeps working either way.
func (s *Server) persistAIGenJob(job *AIGenJob) {
	if job == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Store.UpsertAIGenJob(ctx, toStoreJob(job)); err != nil {
		log.Printf("ai job %s: persist history: %v", job.ID, err)
	}
}

// toStoreJob maps the live job (with the full result) to the persisted record.
func toStoreJob(j *AIGenJob) store.AIGenJob {
	sj := store.AIGenJob{
		ID:         j.ID,
		Status:     j.Status,
		Stage:      j.Stage,
		Model:      j.Model,
		TopicHint:  j.TopicHint,
		Publish:    j.Publish,
		Error:      j.Err,
		PostID:     j.PostID,
		CreatedAt:  j.CreatedAt,
		FinishedAt: j.FinishedAt,
	}
	if j.Result != nil {
		sj.Title = j.Result.Title
		sj.Slug = j.Result.Slug
		sj.Summary = j.Result.Summary
		sj.Tags = j.Result.Tags
	}
	return sj
}

// uniquePostSlug returns slug, or slug-2/slug-3/… when a post with that slug
// already exists.
func (s *Server) uniquePostSlug(ctx context.Context, slug string) string {
	base := slug
	for i := 2; ; i++ {
		if _, err := s.Store.GetPost(ctx, base); err != nil {
			return base
		}
		base = slug + "-" + itoa64(int64(i))
	}
}

// ---------------------------------------------------------------------------
// Small helpers

func errAIAPIKeyMissing() error {
	return errors.New("AI API key is missing — set OPENAI_API_KEY/AI_API_KEY in the environment or configure it in Admin Settings")
}

func errNoHistoryOrTopic() error {
	return errors.New("nothing to generate: no blog history and no topic was provided")
}
