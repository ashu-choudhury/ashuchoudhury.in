package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/mail"
	"os"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/ashu-choudhury/portfolio/components"
	"github.com/ashu-choudhury/portfolio/data"
	"github.com/ashu-choudhury/portfolio/importer"
	"github.com/ashu-choudhury/portfolio/store"
)

const themeCookie = "theme"

// render wraps page content in the base layout, fetching the featured
// projects for the footer from the store.
func (s *Server) render(w http.ResponseWriter, r *http.Request, page data.PageMeta, content templ.Component) {
	featured, err := s.featuredProjects(r.Context())
	if err != nil {
		log.Printf("render: featured projects: %v", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	components.BaseLayout(page, content, featured).Render(r.Context(), w)
}

// featuredProjects returns the projects shown in the footer: featured first,
// then the most recently updated shown projects.
func (s *Server) featuredProjects(ctx context.Context) ([]store.Project, error) {
	all, err := s.Store.ListShownProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]store.Project, 0, 6)
	for _, p := range all {
		if p.Featured {
			out = append(out, p)
		}
	}
	for _, p := range all {
		if len(out) >= 6 {
			break
		}
		if !p.Featured {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	featured, _ := s.featuredProjects(r.Context())
	// Home hero uses the same featured set.
	renderHome := components.Home(featured)
	s.render(w, r, data.HomeMeta(theme(r)), renderHome)
}

func (s *Server) handleAbout(w http.ResponseWriter, r *http.Request) {
	featured, _ := s.featuredProjects(r.Context())
	s.render(w, r, data.AboutMeta(theme(r)), components.AboutPage(featured))
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	projects := s.filterProjects(r.Context(), query)

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.ProjectGrid(projects, query).Render(r.Context(), w)
		return
	}
	s.render(w, r, data.ProjectsMeta(theme(r), len(projects)), components.ProjectsPage(query, projects))
}

func (s *Server) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	p, err := s.Store.GetProject(r.Context(), slug)
	if err != nil || p.Classification == store.ClassificationClone || !p.Visible {
		s.handleNotFound(w, r)
		return
	}

	readmeHTML := ""
	if p.Source != "local" {
		owner, repo := parseRepoOwnerAndName(p.RepoURL, p.Slug)
		fetchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		client := importer.NewClient(owner)
		if content, err := client.Readme(fetchCtx, repo); err == nil && strings.TrimSpace(content) != "" {
			readmeHTML = renderMarkdown(content)
		} else if err != nil {
			log.Printf("handleProjectDetail: live fetch readme for %s/%s: %v", owner, repo, err)
		}
	}

	descHTML := ""
	if p.Description != "" {
		descHTML = renderMarkdown(p.Description)
	}

	next := s.nextProject(r.Context(), p)
	s.render(w, r, data.ProjectMeta(p, theme(r)), components.ProjectDetailPage(*p, next, readmeHTML, descHTML))
}

func (s *Server) handleContact(w http.ResponseWriter, r *http.Request) {
	var initial templ.Component
	if r.URL.Query().Get("sent") == "1" {
		initial = components.FormResultOk()
	}
	s.render(w, r, data.ContactMeta(theme(r)), components.ContactPage(initial))
}

func (s *Server) handleContactSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	message := strings.TrimSpace(r.FormValue("message"))

	result := validateContact(name, email, message)
	if result != nil {
		if isHTMX(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			result.Render(r.Context(), w)
			return
		}
		s.render(w, r, data.ContactMeta(theme(r)), components.ContactPage(result))
		return
	}

	// Persist the message to the database (the admin inbox reads it).
	if err := s.Store.CreateMessage(r.Context(), store.Message{
		Name:      name,
		Email:     email,
		Body:      message,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		log.Printf("contact: store message: %v", err)
	}
	log.Printf("contact message from %s <%s>: %s", name, email, message)

	if isHTMX(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		components.FormResultOk().Render(r.Context(), w)
		return
	}
	http.Redirect(w, r, "/contact?sent=1", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// HTMX endpoints

// handleThemeToggle flips the theme cookie and asks htmx to refresh the page.
func (s *Server) handleThemeToggle(w http.ResponseWriter, r *http.Request) {
	next := "dark"
	if theme(r) == "dark" {
		next = "light"
	}
	http.SetCookie(w, &http.Cookie{
		Name:     themeCookie,
		Value:    next,
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 365,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// SEO endpoints

func (s *Server) handleSitemap(w http.ResponseWriter, r *http.Request) {
	var paths []string
	if ps, err := s.Store.ListShownProjects(r.Context()); err == nil {
		for _, p := range ps {
			paths = append(paths, "/projects/"+p.Slug)
		}
	}
	if posts, err := s.Store.ListPosts(r.Context(), false); err == nil {
		for _, p := range posts {
			paths = append(paths, "/blog/"+p.Slug)
		}
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(data.SitemapXML(paths...)))
}

func (s *Server) handleRobots(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(data.RobotsTXT()))
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	featured, _ := s.featuredProjects(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	components.BaseLayout(data.NotFoundMeta(theme(r)), components.NotFoundPage(), featured).Render(r.Context(), w)
}

// ---------------------------------------------------------------------------
// Helpers

// isHTMX reports whether the request was issued by htmx (excluding boosted
// full-page navigations, which must still receive complete documents).
func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Boosted") != "true"
}

// theme returns the pinned theme from the cookie, defaulting to dark.
func theme(r *http.Request) string {
	if c, err := r.Cookie(themeCookie); err == nil && c.Value != "" {
		return c.Value
	}
	return "dark"
}

// filterProjects returns shown projects matching the query across name,
// tagline, summary and stack.
func (s *Server) filterProjects(ctx context.Context, q string) []store.Project {
	all, err := s.Store.ListShownProjects(ctx)
	if err != nil || q == "" {
		if err != nil {
			log.Printf("projects: %v", err)
		}
		return all
	}
	q = strings.ToLower(q)
	out := []store.Project{}
	for _, p := range all {
		hay := strings.ToLower(p.Name + " " + p.Tagline + " " + p.Summary + " " + strings.Join(p.Stack, " ") + " " + strings.Join(p.Fingerprint, " "))
		if strings.Contains(hay, q) {
			out = append(out, p)
		}
	}
	return out
}

// nextProject returns the next shown project after p in recency order.
func (s *Server) nextProject(ctx context.Context, p *store.Project) store.Project {
	all, err := s.Store.ListShownProjects(ctx)
	if err != nil {
		return *p
	}
	if len(all) == 0 {
		return *p
	}
	for i, x := range all {
		if x.Slug == p.Slug {
			return all[(i+1)%len(all)]
		}
	}
	return all[0]
}

func validateContact(name, email, message string) templ.Component {
	switch {
	case name == "":
		return components.FormResultErr("Please tell me your name.")
	case len(name) > 80:
		return components.FormResultErr("That name is a little long — 80 characters max.")
	case email == "":
		return components.FormResultErr("An email address is needed so I can reply.")
	case !validEmail(email):
		return components.FormResultErr("That email address doesn't look right.")
	case message == "":
		return components.FormResultErr("Don't forget the message itself.")
	case len(message) > 2000:
		return components.FormResultErr("Keep the message under 2000 characters.")
	}
	return nil
}

func validEmail(email string) bool {
	a, err := mail.ParseAddress(email)
	return err == nil && a.Address == email
}

// parseRepoOwnerAndName extracts the owner and repository name from a GitHub URL or defaults to DefaultOwner and slug.
func parseRepoOwnerAndName(repoURL, slug string) (string, string) {
	if repoURL != "" {
		u := strings.TrimSuffix(repoURL, "/")
		parts := strings.Split(u, "/")
		if len(parts) >= 2 {
			return parts[len(parts)-2], parts[len(parts)-1]
		}
	}
	return importer.DefaultOwner, slug
}

// handleHealth returns a JSON diagnostic report of database connection status, active store type, S3 proxy status, and env configuration.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	start := time.Now()
	err := s.Store.Ping(r.Context())
	latency := time.Since(start).Milliseconds()

	storeType := s.Store.DriverName()
	status := "ok"
	dbStatus := "connected"
	if err != nil {
		status = "degraded"
		dbStatus = "error: " + err.Error()
		w.WriteHeader(http.StatusInternalServerError)
	}

	s3Status := "disabled"
	if s.backup != nil {
		s3Status = "enabled"
	}

	tursoURLEnv := "missing"
	if os.Getenv("TURSO_DATABASE_URL") != "" {
		tursoURLEnv = "configured"
	}

	tursoTokenEnv := "missing"
	if os.Getenv("TURSO_AUTH_TOKEN") != "" {
		tursoTokenEnv = "configured"
	}

	s3BucketEnv := "missing"
	if os.Getenv("S3_BUCKET") != "" {
		s3BucketEnv = "configured"
	}

	awsKeyEnv := "missing"
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("S3_ACCESS_KEY_ID") != "" || os.Getenv("S3_ACCESS_KEY") != "" {
		awsKeyEnv = "configured"
	}

	awsSecretEnv := "missing"
	if os.Getenv("AWS_SECRET_ACCESS_KEY") != "" || os.Getenv("S3_SECRET_ACCESS_KEY") != "" || os.Getenv("S3_SECRET_KEY") != "" {
		awsSecretEnv = "configured"
	}

	fmt.Fprintf(w, `{"status":"%s","store_type":"%s","database":"%s","ping_ms":%d,"s3_storage":"%s","env_checks":{"TURSO_DATABASE_URL":"%s","TURSO_AUTH_TOKEN":"%s","S3_BUCKET":"%s","AWS_ACCESS_KEY_ID":"%s","AWS_SECRET_ACCESS_KEY":"%s"}}`,
		status, storeType, dbStatus, latency, s3Status, tursoURLEnv, tursoTokenEnv, s3BucketEnv, awsKeyEnv, awsSecretEnv)
}

