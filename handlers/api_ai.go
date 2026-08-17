package handlers

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// aiJobStatusResponse is the JSON shape of the public status endpoint.
type aiJobStatusResponse struct {
	JobID  string        `json:"job_id"`
	Status string        `json:"status"`
	Stage  string        `json:"stage,omitempty"`
	Error  string        `json:"error,omitempty"`
	Post   *AIBlogResult `json:"post,omitempty"`
}

// aiJobCreateResponse is the immediate reply of the ping endpoint in async
// mode: the job runs in the background and publishes on its own.
type aiJobCreateResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// handleAIGeneratePing is the endpoint an external scheduler pings to wake
// up the AI and automatically publish the next blog post. Flow:
//
//  1. Session 1 (strategist) reviews the published blog history and picks
//     the single best next topic — unless a topic is supplied in the request.
//  2. Session 2 (writer) starts a brand-new chat that only knows the chosen
//     topic and writes the complete post.
//  3. The post is published automatically.
//
// By default the endpoint returns 202 immediately and the pipeline runs in
// the background (poll GET /api/ai/generate/status?job_id=…). Add
// ?wait=true to run synchronously and receive the post in the response.
//
// Authentication: the shared secret must be sent as
// "Authorization: Bearer <token>", "X-AI-Token: <token>" or "?token=<token>",
// where <token> is the ai_generate_token setting or AI_GENERATE_TOKEN env.
func (s *Server) handleAIGeneratePing(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	status, err := s.checkAIGenerateToken(r)
	if err != nil {
		writeAIJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	cfg := s.DefaultAIConfig(ctx)
	if cfg.APIKey == "" {
		writeAIJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "AI generation is not configured: no AI API key set (OPENAI_API_KEY/AI_API_KEY or Admin Settings).",
		})
		return
	}

	topic := r.URL.Query().Get("topic")
	if topic == "" {
		topic = r.FormValue("topic")
	}
	if topic == "" && r.Body != nil {
		var body struct {
			Topic string `json:"topic"`
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
			topic = body.Topic
		}
	}
	topic = strings.TrimSpace(topic)

	model := strings.TrimSpace(r.URL.Query().Get("model"))

	jobID, err := s.startAIGenJob(ctx, topic, model, true)
	if err != nil {
		writeAIJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	// Synchronous mode: wait for the run and return the published post.
	if r.URL.Query().Get("wait") == "true" || r.URL.Query().Get("wait") == "1" {
		job := s.waitForAIJob(jobID, ctx)
		if job == nil {
			writeAIJSON(w, http.StatusGone, map[string]string{"error": "job expired before finishing"})
			return
		}
		resp := aiJobStatusResponse{JobID: job.ID, Status: job.Status, Stage: job.Stage}
		switch job.Status {
		case aiStatusPublished:
			resp.Post = job.Result
			writeAIJSON(w, http.StatusOK, resp)
		case aiStatusFailed:
			resp.Error = job.Err
			writeAIJSON(w, http.StatusUnprocessableEntity, resp)
		default:
			resp.Error = "job did not finish in time"
			writeAIJSON(w, http.StatusGatewayTimeout, resp)
		}
		return
	}

	writeAIJSON(w, http.StatusAccepted, aiJobCreateResponse{
		JobID:   jobID,
		Status:  aiStatusQueued,
		Message: "AI generation started — it will pick the next topic from the blog history and publish automatically.",
	})
}

// handleAIGenerateStatus reports the progress of a generation job.
func (s *Server) handleAIGenerateStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.checkAIGenerateToken(r)
	if err != nil {
		writeAIJSON(w, status, map[string]string{"error": err.Error()})
		return
	}

	job := s.aiJobs.get(r.URL.Query().Get("job_id"))
	if job == nil {
		writeAIJSON(w, http.StatusNotFound, map[string]string{"error": "job not found"})
		return
	}

	resp := aiJobStatusResponse{JobID: job.ID, Status: job.Status, Stage: job.Stage, Error: job.Err}
	if job.Result != nil {
		resp.Post = job.Result
	}
	writeAIJSON(w, http.StatusOK, resp)
}

// checkAIGenerateToken enforces the shared secret on the public AI endpoints.
// It returns an HTTP status and an error, or (0, nil) when authorized.
func (s *Server) checkAIGenerateToken(r *http.Request) (int, error) {
	expected := s.AIGenerateToken(r.Context())
	if expected == "" {
		return http.StatusServiceUnavailable, errors.New("AI generate endpoint is disabled: set AI_GENERATE_TOKEN (environment) or ai_generate_token (Admin → Settings)")
	}

	given := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		given = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if given == "" {
		given = strings.TrimSpace(r.Header.Get("X-AI-Token"))
	}
	if given == "" {
		given = strings.TrimSpace(r.URL.Query().Get("token"))
	}

	if given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(expected)) != 1 {
		return http.StatusUnauthorized, errors.New("invalid or missing AI generate token")
	}
	return 0, nil
}

// waitForAIJob blocks until the job reaches a terminal state or ctx expires.
func (s *Server) waitForAIJob(jobID string, ctx context.Context) *AIGenJob {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		job := s.aiJobs.get(jobID)
		if job == nil || job.Status == aiStatusPublished || job.Status == aiStatusFailed {
			return job
		}
		select {
		case <-ctx.Done():
			return s.aiJobs.get(jobID)
		case <-ticker.C:
		}
	}
}

// writeAIJSON writes a JSON response with the given status.
func writeAIJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("ai api: encode response: %v", err)
	}
}
