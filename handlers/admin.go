package handlers

import (
	"crypto/subtle"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/components"
	"github.com/ashu-choudhury/portfolio/data"
	"github.com/ashu-choudhury/portfolio/importer"
	"github.com/ashu-choudhury/portfolio/store"
	"golang.org/x/crypto/bcrypt"
)

const settingsTitle = "site_title"
const settingsDesc = "site_desc"

// ---------------------------------------------------------------------------
// Authentication

func (s *Server) handleAdminLoginPage(w http.ResponseWriter, r *http.Request) {
	if s.loggedIn(r) {
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Sign in"}, components.AdminLogin(""))
}

func (s *Server) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ip := clientIP(r)
	if !s.loginAllowed(ip) {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Sign in"},
			components.AdminLogin("Too many attempts — try again in a minute."))
		return
	}
	user := r.FormValue("username")
	pass := r.FormValue("password")
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.adminUser)) == 1
	passOK := bcrypt.CompareHashAndPassword(s.adminHash, []byte(pass)) == nil
	if !userOK || !passOK {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Sign in"},
			components.AdminLogin("Invalid username or password."))
		return
	}
	if _, err := s.newSessionToken(w); err != nil {
		log.Printf("admin login: session: %v", err)
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if token := sessionToken(r); token != "" {
		_ = s.Store.DeleteSession(rctx(), token)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

// loggedIn reports whether the request carries a valid admin session.
func (s *Server) loggedIn(r *http.Request) bool {
	token := sessionToken(r)
	if token == "" {
		return false
	}
	ok, err := s.Store.SessionValid(rctx(), token)
	return err == nil && ok
}

// clientIP returns the best-effort client address (no proxy headers are
// trusted — this is for rate limiting, not security).
func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// ---------------------------------------------------------------------------
// Dashboard

func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	total, _ := s.Store.PageViewTotal(ctx)
	top, _ := s.Store.TopPages(ctx, 10)
	daily, _ := s.Store.DailyViews(ctx, 14)
	posts, _ := s.Store.ListPosts(ctx, true)
	messages, _ := s.Store.ListMessages(ctx)
	projects, _ := s.Store.ListProjects(ctx)

	today := int64(0)
	todayKey := time.Now().UTC().Format("2006-01-02")
	for _, d := range daily {
		if d.Date == todayKey {
			today = d.Count
		}
	}

	aiRuns, _ := s.Store.ListAIGenJobs(ctx, 8)

	d := components.DashboardData{
		TotalViews:   total,
		TodayViews:   today,
		TopPages:     top,
		Daily:        daily,
		PostCount:    len(posts),
		MessageCount: len(messages),
		ProjectCount: len(projects),
		AIGenJobs:    aiRuns,
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Dashboard", Active: "dashboard"},
		components.AdminDashboard(d))
}

// ---------------------------------------------------------------------------
// Posts

func (s *Server) handleAdminPosts(w http.ResponseWriter, r *http.Request) {
	posts, err := s.Store.ListPosts(r.Context(), true)
	if err != nil {
		http.Error(w, "could not load posts", http.StatusInternalServerError)
		return
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Posts", Active: "posts"},
		components.AdminPostsList(posts))
}

func (s *Server) handleAdminPostNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := s.ConfiguredAIModels(ctx)
	cfg := s.DefaultAIConfig(ctx)

	if r.Method == http.MethodGet {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "New post", Active: "posts"}, components.AdminPostForm(nil, true, "", "", models, cfg.Model, "", nil))
		return
	}
	// POST create
	p, errMsg := s.parsePostForm(r)
	if errMsg != "" {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "New post", Active: "posts"},
			components.AdminPostForm(p, true, errMsg, renderMarkdown(p.Body), models, cfg.Model, "", nil))
		return
	}
	if _, err := s.Store.GetPost(r.Context(), p.Slug); err == nil {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "New post", Active: "posts"},
			components.AdminPostForm(p, true, "A post with this slug already exists.", renderMarkdown(p.Body), models, cfg.Model, "", nil))
		return
	}
	id, err := s.Store.CreatePost(r.Context(), *p)
	if err != nil {
		log.Printf("admin: create post: %v", err)
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "New post", Active: "posts"},
			components.AdminPostForm(p, true, "Could not save the post — the slug may already be taken.", renderMarkdown(p.Body), models, cfg.Model, "", nil))
		return
	}
	http.Redirect(w, r, "/admin/posts/"+itoa64(id)+"/edit", http.StatusSeeOther)
}

func (s *Server) handleAdminPostEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := s.ConfiguredAIModels(ctx)
	cfg := s.DefaultAIConfig(ctx)

	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.Store.GetPostByID(ctx, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Edit post", Active: "posts"},
		components.AdminPostForm(p, false, "", renderMarkdown(p.Body), models, cfg.Model, "", nil))
}

func (s *Server) handleAdminPostSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := s.ConfiguredAIModels(ctx)
	cfg := s.DefaultAIConfig(ctx)

	p, errMsg := s.parsePostForm(r)
	if errMsg != "" {
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Edit post", Active: "posts"},
			components.AdminPostForm(p, false, errMsg, renderMarkdown(p.Body), models, cfg.Model, "", nil))
		return
	}
	if err := s.Store.UpdatePost(ctx, *p); err != nil {
		log.Printf("admin: update post: %v", err)
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Edit post", Active: "posts"},
			components.AdminPostForm(p, false, "Could not save the post.", renderMarkdown(p.Body), models, cfg.Model, "", nil))
		return
	}
	s.TriggerBackup(ctx)
	s.NotifySearchEngines()
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

// handleAdminAIGenerate starts the two-session AI pipeline as a background
// job and re-renders the post form with a live progress card. The form keeps
// whatever the admin already typed; the job card polls its own status and
// offers to load the finished draft into the editor.
func (s *Server) handleAdminAIGenerate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	models := s.ConfiguredAIModels(ctx)

	topic := strings.TrimSpace(r.FormValue("topic"))
	selectedModel := strings.TrimSpace(r.FormValue("model"))

	p, _ := s.parsePostForm(r)
	if p == nil {
		p = &store.Post{}
	}
	isNew := p.ID == 0

	jobID, err := s.startAIGenJob(ctx, topic, selectedModel, false)
	if err != nil {
		log.Printf("admin generate ai: %v", err)
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "New post", Active: "posts"},
			components.AdminPostForm(p, isNew, "AI Generation failed: "+err.Error(), renderMarkdown(p.Body), models, selectedModel, topic, nil))
		return
	}

	card := &components.AIGenCard{JobID: jobID, Status: aiStatusQueued, Stage: "Preparing…"}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.AdminPostForm(p, isNew, "", renderMarkdown(p.Body), models, selectedModel, topic, card).Render(ctx, w)
}

// handleAdminAIStatus renders the live progress card for a generation job.
// The card polls itself every 2s; when the job finishes it stops polling and
// shows the result (or the error).
func (s *Server) handleAdminAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	job := s.aiJobs.get(r.URL.Query().Get("job_id"))
	if job == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.AIGenStatus(components.AIGenCard{Status: aiStatusFailed, Err: "Job not found — it may have expired. Try generating again."}).Render(ctx, w)
		return
	}
	card := &components.AIGenCard{JobID: job.ID, Status: job.Status, Stage: job.Stage, Err: job.Err}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.AIGenStatus(*card).Render(ctx, w)
}

// handleAdminAILoad loads a finished generation job's result into the post
// editor as a draft ready to review and save.
func (s *Server) handleAdminAILoad(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	job := s.aiJobs.get(r.URL.Query().Get("job_id"))
	if job == nil || job.Result == nil {
		http.NotFound(w, r)
		return
	}

	models := s.ConfiguredAIModels(ctx)
	model := job.Model
	if model == "" {
		model = s.DefaultAIConfig(ctx).Model
	}

	p := &store.Post{
		Title:     job.Result.Title,
		Slug:      job.Result.Slug,
		Summary:   job.Result.Summary,
		Tags:      job.Result.Tags,
		Body:      job.Result.Body,
		Published: true, // pre-checked so saving publishes the generated post
	}
	topic := job.TopicHint
	if topic == "" {
		topic = job.Result.Title
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.AdminPostForm(p, true, "", renderMarkdown(p.Body), models, model, topic, nil).Render(ctx, w)
}

func (s *Server) handleAdminPostDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.Store.DeletePost(r.Context(), id)
	s.TriggerBackup(r.Context())
	s.NotifySearchEngines()
	http.Redirect(w, r, "/admin/posts", http.StatusSeeOther)
}

// handleAdminPostPreview renders markdown for the live editor preview. It is
// stateless, so it is exempt from the CSRF check (see csrfGuard).
func (s *Server) handleAdminPostPreview(w http.ResponseWriter, r *http.Request) {
	body := r.FormValue("body")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.AdminPreview(renderMarkdown(body)).Render(r.Context(), w)
}

// parsePostForm reads and validates the post form into a store.Post.
// The returned error message is empty when validation passes.
func (s *Server) parsePostForm(r *http.Request) (*store.Post, string) {
	if err := r.ParseForm(); err != nil {
		return nil, "Bad form data."
	}
	p := &store.Post{}
	p.ID, _ = parseID(r.FormValue("id"))
	p.Title = strings.TrimSpace(r.FormValue("title"))
	p.Slug = strings.TrimSpace(r.FormValue("slug"))
	p.Summary = strings.TrimSpace(r.FormValue("summary"))
	p.Body = r.FormValue("body")
	p.Tags = splitTags(r.FormValue("tags"))
	p.Published = r.FormValue("published") == "on" || r.FormValue("published") == "true"

	switch {
	case p.Title == "":
		return p, "The title is required."
	case len(p.Title) > 160:
		return p, "Keep the title under 160 characters."
	case p.Body == "":
		return p, "The body is required."
	}
	if p.Slug == "" {
		p.Slug = slugify(p.Title)
	}
	if p.Slug == "" {
		return p, "A slug could not be generated from the title."
	}

	now := time.Now().UTC()
	if p.ID == 0 {
		// New post
		p.PublishedAt = now
	} else {
		// Keep the original publish date when editing.
		if existing, err := s.Store.GetPostByID(r.Context(), p.ID); err == nil {
			p.PublishedAt = existing.PublishedAt
		}
	}
	p.UpdatedAt = now
	return p, ""
}

// splitTags parses a comma-separated tag list, trimming and deduplicating.
func splitTags(raw string) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts arbitrary text into a URL-safe slug.
func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// parseID converts a numeric path/form value to an int64.
func parseID(s string) (int64, error) {
	var id int64
	if s == "" {
		return 0, store.ErrNotFound
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, store.ErrNotFound
		}
		id = id*10 + int64(c-'0')
	}
	return id, nil
}

// itoa64 converts an int64 to a string (admin templates share the same
// helper in the components package; this one lives here for handlers).
func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ---------------------------------------------------------------------------
// Messages

func (s *Server) handleAdminMessages(w http.ResponseWriter, r *http.Request) {
	messages, err := s.Store.ListMessages(r.Context())
	if err != nil {
		http.Error(w, "could not load messages", http.StatusInternalServerError)
		return
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Messages", Active: "messages"},
		components.AdminMessagesList(messages))
}

func (s *Server) handleAdminMessageDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.Store.DeleteMessage(r.Context(), id)
	http.Redirect(w, r, "/admin/messages", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Project curation

func (s *Server) handleAdminProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := s.Store.ListProjects(r.Context())
	if err != nil {
		http.Error(w, "could not load projects", http.StatusInternalServerError)
		return
	}
	msg := r.URL.Query().Get("msg")
	errMsg := r.URL.Query().Get("err")
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Projects", Active: "projects"},
		components.AdminProjectsList(projects, msg, errMsg))
}

func (s *Server) handleAdminProjectsSync(w http.ResponseWriter, r *http.Request) {
	opts := importer.SyncOptions{
		Owner:        importer.DefaultOwner,
		Fingerprints: true,
		Readmes:      true,
	}
	if err := importer.Sync(r.Context(), s.Store, opts); err != nil {
		log.Printf("admin projects sync: %v", err)
		http.Redirect(w, r, "/admin/projects?err="+url.QueryEscape("GitHub sync failed: "+err.Error()), http.StatusSeeOther)
		return
	}
	s.TriggerBackup(r.Context())
	s.NotifySearchEngines()
	http.Redirect(w, r, "/admin/projects?msg="+url.QueryEscape("Synced open-source repositories from GitHub successfully."), http.StatusSeeOther)
}

func (s *Server) handleAdminProjectNew(w http.ResponseWriter, r *http.Request) {
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "New project", Active: "projects"},
		components.AdminProjectForm(nil, true, "", ""))
}

func (s *Server) handleAdminProjectEdit(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	p, err := s.Store.GetProject(r.Context(), slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Edit project", Active: "projects"},
		components.AdminProjectForm(p, false, "", renderMarkdown(p.Description)))
}

func (s *Server) handleAdminProjectSave(w http.ResponseWriter, r *http.Request) {
	p, isNew, errMsg := s.parseProjectForm(r)
	if errMsg != "" {
		title := "Edit project"
		if isNew {
			title = "New project"
		}
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: title, Active: "projects"},
			components.AdminProjectForm(p, isNew, errMsg, renderMarkdown(p.Description)))
		return
	}

	if err := s.Store.UpsertProject(r.Context(), *p); err != nil {
		log.Printf("admin: save project: %v", err)
		s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Edit project", Active: "projects"},
			components.AdminProjectForm(p, isNew, "Could not save project.", renderMarkdown(p.Description)))
		return
	}
	s.TriggerBackup(r.Context())
	s.NotifySearchEngines()
	http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
}

func (s *Server) handleAdminProjectDelete(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if err := s.Store.DeleteProject(r.Context(), slug); err != nil {
		log.Printf("admin delete project %s: %v", slug, err)
	}
	s.TriggerBackup(r.Context())
	s.NotifySearchEngines()
	http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
}

func (s *Server) parseProjectForm(r *http.Request) (*store.Project, bool, string) {
	_ = r.ParseMultipartForm(32 << 20)

	isNew := r.FormValue("is_new") == "true"
	slug := strings.TrimSpace(r.FormValue("slug"))
	if slug == "" {
		slug = slugify(r.FormValue("name"))
	}

	p := &store.Project{
		Slug:           slug,
		Name:           strings.TrimSpace(r.FormValue("name")),
		Tagline:        strings.TrimSpace(r.FormValue("tagline")),
		Summary:        strings.TrimSpace(r.FormValue("summary")),
		Description:    r.FormValue("description"),
		Language:       strings.TrimSpace(r.FormValue("language")),
		Year:           strings.TrimSpace(r.FormValue("year")),
		RepoURL:        strings.TrimSpace(r.FormValue("repo_url")),
		Classification: store.Classification(r.FormValue("classification")),
		Visible:        r.FormValue("visible") == "on" || r.FormValue("visible") == "true",
		Featured:       r.FormValue("featured") == "on" || r.FormValue("featured") == "true",
		Stack:          splitTags(r.FormValue("stack")),
		Features:       splitLines(r.FormValue("features")),
		Source:         "github",
	}
	if p.RepoURL == "" {
		p.Source = "local"
	}
	if p.Classification == "" {
		p.Classification = store.ClassificationOriginal
	}
	if p.Year == "" {
		p.Year = strconv.Itoa(time.Now().Year())
	}
	p.PushedAt = time.Now().UTC().Format("2006-01-02")
	p.CreatedAt = p.PushedAt

	// Preserve existing links/fingerprint if editing
	if existing, err := s.Store.GetProject(r.Context(), p.Slug); err == nil {
		p.Links = existing.Links
		p.Fingerprint = existing.Fingerprint
		if p.CreatedAt == "" {
			p.CreatedAt = existing.CreatedAt
		}
	}

	switch {
	case p.Name == "":
		return p, isNew, "Project name is required."
	case p.Slug == "":
		return p, isNew, "Project slug is required."
	}

	// Handle optional file attachment
	file, header, err := r.FormFile("attachment")
	if err == nil && header != nil && header.Filename != "" {
		defer file.Close()
		cleanFileName := filepath.Base(header.Filename)
		targetDir := filepath.Join(wwwDirRoot(), "projects", p.Slug)
		_ = os.MkdirAll(targetDir, 0755)
		destPath := filepath.Join(targetDir, cleanFileName)

		if dst, err := os.Create(destPath); err == nil {
			_, _ = io.Copy(dst, file)
			_ = dst.Close()

			publicURL := "/files/projects/" + p.Slug + "/" + cleanFileName
			p.Links = append(p.Links, store.Link{
				Label: cleanFileName,
				URL:   publicURL,
				Icon:  "external",
			})
		}
	}

	return p, isNew, ""
}

func splitLines(raw string) []string {
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (s *Server) handleAdminProjectClassify(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	c := store.Classification(r.FormValue("classification"))
	if c != store.ClassificationOriginal && c != store.ClassificationRewritten && c != store.ClassificationClone {
		http.Error(w, "invalid classification", http.StatusBadRequest)
		return
	}
	if err := s.Store.SetProjectClassification(r.Context(), slug, c); err != nil {
		http.Error(w, "could not update", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminProjectVisible(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	visible := r.FormValue("visible") == "true"
	if err := s.Store.SetProjectVisible(r.Context(), slug, visible); err != nil {
		http.Error(w, "could not update", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
}

func (s *Server) handleAdminProjectFeatured(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	featured := r.FormValue("featured") == "true"
	if err := s.Store.SetProjectFeatured(r.Context(), slug, featured); err != nil {
		http.Error(w, "could not update", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/projects", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Settings

func (s *Server) handleAdminSettingsPage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	title := data.SiteName
	desc := data.SiteTag
	if v, err := s.Store.GetSetting(ctx, settingsTitle); err == nil && v != "" {
		title = v
	}
	if v, err := s.Store.GetSetting(ctx, settingsDesc); err == nil && v != "" {
		desc = v
	}

	aiCfg := s.DefaultAIConfig(ctx)
	aiModelsStr := strings.Join(s.ConfiguredAIModels(ctx), ", ")
	aiToken := s.AIGenerateToken(ctx)

	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "Settings", Active: "settings"},
		components.AdminSettingsForm(title, desc, aiCfg.BaseURL, aiCfg.APIKey, aiModelsStr, aiCfg.Model, aiToken, r.URL.Query().Get("saved") == "1"))
}

func (s *Server) handleAdminSettingsSave(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	title := strings.TrimSpace(r.FormValue("site_title"))
	desc := strings.TrimSpace(r.FormValue("site_desc"))
	aiBaseURL := strings.TrimSpace(r.FormValue("ai_base_url"))
	aiAPIKey := strings.TrimSpace(r.FormValue("ai_api_key"))
	aiModels := strings.TrimSpace(r.FormValue("ai_models"))
	aiDefaultModel := strings.TrimSpace(r.FormValue("ai_default_model"))
	aiGenerateToken := strings.TrimSpace(r.FormValue("ai_generate_token"))

	_ = s.Store.SetSetting(ctx, settingsTitle, title)
	_ = s.Store.SetSetting(ctx, settingsDesc, desc)
	_ = s.Store.SetSetting(ctx, "ai_base_url", aiBaseURL)
	_ = s.Store.SetSetting(ctx, "ai_api_key", aiAPIKey)
	_ = s.Store.SetSetting(ctx, "ai_models", aiModels)
	_ = s.Store.SetSetting(ctx, "ai_default_model", aiDefaultModel)
	_ = s.Store.SetSetting(ctx, "ai_generate_token", aiGenerateToken)

	data.SetSiteIdentity(title, desc)
	s.TriggerBackup(ctx)
	http.Redirect(w, r, "/admin/settings?saved=1", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// File Manager (storage/persisted/www)

func wwwDirRoot() string {
	return filepath.Join("storage", "persisted", "www")
}

func sanitizeRelPath(rel string) string {
	rel = strings.ReplaceAll(rel, "\\", "/")
	cleaned := filepath.Clean("/" + rel)
	cleaned = filepath.ToSlash(cleaned)
	cleaned = strings.Trim(cleaned, "/")
	if cleaned == "." || cleaned == "" {
		return ""
	}
	return cleaned
}

func (s *Server) handleAdminFiles(w http.ResponseWriter, r *http.Request) {
	relDir := sanitizeRelPath(r.URL.Query().Get("dir"))

	var files []components.FileInfo

	if s.backup == nil {
		log.Printf("[S3 DIAGNOSTIC WARNING] s.backup is NIL on request /admin/files. Check if S3_BUCKET is set in environment variables!")
	} else {
		log.Printf("[S3 DIAGNOSTIC] Fetching file list from S3 for relDir='%s'", relDir)
		s3Files, err := s.backup.ListPublicFiles(r.Context(), relDir)
		if err != nil {
			log.Printf("[S3 DIAGNOSTIC ERROR] ListPublicFiles relDir='%s' failed: %v", relDir, err)
		} else {
			log.Printf("[S3 DIAGNOSTIC SUCCESS] ListPublicFiles found %d items for relDir='%s'", len(s3Files), relDir)
		}
		for _, f := range s3Files {
			files = append(files, components.FileInfo{
				Name:      f.Name,
				RelPath:   f.RelPath,
				PublicURL: f.PublicURL,
				Size:      f.Size,
				IsDir:     f.IsDir,
				ModTime:   f.ModTime,
			})
		}
	}

	msg := r.URL.Query().Get("msg")
	errMsg := r.URL.Query().Get("err")

	s.renderAdminPage(w, r, components.AdminPageMeta{Title: "File Manager", Active: "files"},
		components.AdminFilesView(relDir, files, msg, errMsg))
}

func (s *Server) handleAdminFilesUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB RAM buffer
		http.Redirect(w, r, "/admin/files?err="+url.QueryEscape("Upload failed: "+err.Error()), http.StatusSeeOther)
		return
	}

	relDir := sanitizeRelPath(r.FormValue("dir"))
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("Please select a file to upload"), http.StatusSeeOther)
		return
	}
	defer file.Close()

	cleanName := filepath.Base(header.Filename)
	targetRel := cleanName
	if relDir != "" {
		targetRel = relDir + "/" + cleanName
	}

	if s.backup == nil {
		log.Printf("[S3 DIAGNOSTIC ERROR] Cannot upload file '%s': S3 storage is not configured", targetRel)
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("S3 Upload failed: S3 storage is not configured on server"), http.StatusSeeOther)
		return
	}

	contentType := header.Header.Get("Content-Type")
	log.Printf("[S3 DIAGNOSTIC] Uploading '%s' (%d bytes, %s) to S3", targetRel, header.Size, contentType)
	if err := s.backup.UploadPublicFile(r.Context(), targetRel, file, header.Size, contentType); err != nil {
		log.Printf("[S3 DIAGNOSTIC ERROR] UploadPublicFile '%s' failed: %v", targetRel, err)
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("S3 Upload failed: "+err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/files?dir="+relDir+"&msg="+url.QueryEscape("Uploaded "+cleanName+" successfully"), http.StatusSeeOther)
}

func (s *Server) handleAdminFilesDelete(w http.ResponseWriter, r *http.Request) {
	relPath := sanitizeRelPath(r.FormValue("path"))
	relDir := sanitizeRelPath(r.FormValue("dir"))
	if relPath == "" {
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("Invalid target path"), http.StatusSeeOther)
		return
	}

	if s.backup == nil {
		log.Printf("[S3 DIAGNOSTIC ERROR] Cannot delete path '%s': S3 storage is not configured", relPath)
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("S3 Delete failed: S3 storage is not configured on server"), http.StatusSeeOther)
		return
	}

	log.Printf("[S3 DIAGNOSTIC] Deleting path '%s' from S3", relPath)
	if err := s.backup.DeletePublicFile(r.Context(), relPath); err != nil {
		log.Printf("[S3 DIAGNOSTIC ERROR] DeletePublicFile '%s' failed: %v", relPath, err)
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("S3 Delete failed: "+err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/files?dir="+relDir+"&msg="+url.QueryEscape("Deleted successfully"), http.StatusSeeOther)
}

func (s *Server) handleAdminFilesMkdir(w http.ResponseWriter, r *http.Request) {
	relDir := sanitizeRelPath(r.FormValue("dir"))
	folderName := strings.TrimSpace(r.FormValue("folder_name"))
	folderName = filepath.Base(folderName)
	if folderName == "" || folderName == "." || folderName == "/" {
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("Folder name is required"), http.StatusSeeOther)
		return
	}

	if s.backup == nil {
		log.Printf("[S3 DIAGNOSTIC ERROR] Cannot create folder '%s': S3 storage is not configured", folderName)
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("S3 Mkdir failed: S3 storage is not configured on server"), http.StatusSeeOther)
		return
	}

	log.Printf("[S3 DIAGNOSTIC] Creating S3 folder marker for '%s' in '%s'", folderName, relDir)
	if err := s.backup.CreatePublicFolder(r.Context(), relDir, folderName); err != nil {
		log.Printf("[S3 DIAGNOSTIC ERROR] CreatePublicFolder '%s' failed: %v", folderName, err)
		http.Redirect(w, r, "/admin/files?dir="+relDir+"&err="+url.QueryEscape("S3 Mkdir failed: "+err.Error()), http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/admin/files?dir="+relDir+"&msg="+url.QueryEscape("Created folder "+folderName), http.StatusSeeOther)
}

func fmtSizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
