// Package handlers wires HTTP routes, middleware and the store together.
// Every handler talks to the store.Store interface — the center point —
// never to a concrete database.
package handlers

import (
	"context"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/components"
	"github.com/ashu-choudhury/portfolio/data"
	"github.com/ashu-choudhury/portfolio/storage"
	"github.com/ashu-choudhury/portfolio/store"
)

// Server holds the application's dependencies and routes.
type Server struct {
	Store     store.Store
	staticFS  fs.FS // embedded static assets (passed in from main)
	startedAt time.Time
	loginHits map[string][]time.Time
	adminUser string
	adminHash []byte
	backup    *storage.Backup
	dbPath    string
}

// SetBackup attaches the S3 backup pipeline to the server for instant write sync.
func (s *Server) SetBackup(b *storage.Backup, dsn string) {
	s.backup = b
	s.dbPath = dsn
}

// TriggerBackup performs an immediate WAL checkpoint and S3 backup after admin edits.
func (s *Server) TriggerBackup(ctx context.Context) {
	if s.backup != nil {
		go func() {
			bgCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = s.backup.Backup(bgCtx, func(c context.Context) error {
				if sqliteStore, ok := s.Store.(*store.SQLite); ok {
					return sqliteStore.Checkpoint(c)
				}
				return nil
			}, s.dbPath)
		}()
	}
}

// New builds a Server with the given store and static filesystem, applies
// stored settings and prepares the admin account.
func New(st store.Store, staticFS fs.FS) *Server {
	s := &Server{
		Store:     st,
		staticFS:  staticFS,
		startedAt: time.Now(),
		loginHits: map[string][]time.Time{},
	}
	s.initAdmin()
	s.applySettings()
	return s
}

// applySettings loads the admin-editable site identity into the data
// package so the public site reflects saved settings on the next render.
func (s *Server) applySettings() {
	ctx := rctx()
	title, _ := s.Store.GetSetting(ctx, "site_title")
	desc, _ := s.Store.GetSetting(ctx, "site_desc")
	data.SetSiteIdentity(title, desc)
}

// staticHandler serves embedded static assets with long-lived caching.
func (s *Server) staticHandler() http.Handler {
	if s.staticFS == nil {
		return http.NotFoundHandler()
	}
	return http.StripPrefix("/static/", http.FileServerFS(s.staticFS))
}

// filesHandler serves public uploads directly from storage/persisted/www.
// If a file is missing locally, it lazy-restores it from S3 on demand.
func (s *Server) filesHandler() http.Handler {
	wwwDir := filepath.Join("storage", "persisted", "www")
	_ = os.MkdirAll(wwwDir, 0755)
	fileServer := http.StripPrefix("/files/", http.FileServer(http.Dir(wwwDir)))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(r.URL.Path, "/files/")
		if unescaped, err := url.PathUnescape(relPath); err == nil && unescaped != "" {
			relPath = unescaped
		}
		localPath := filepath.Join(wwwDir, filepath.FromSlash(relPath))

		if _, err := os.Stat(localPath); os.IsNotExist(err) && s.backup != nil {
			_ = s.backup.RestoreFile(r.Context(), relPath, localPath)
		}

		fileServer.ServeHTTP(w, r)
	})
}

// Handler returns the fully-wired http.Handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public pages
	mux.HandleFunc("GET /{$}", s.handleHome)
	mux.HandleFunc("GET /about", s.handleAbout)
	mux.HandleFunc("GET /projects", s.handleProjects)
	mux.HandleFunc("GET /projects/{slug}", s.handleProjectDetail)
	mux.HandleFunc("GET /contact", s.handleContact)
	mux.HandleFunc("POST /contact", s.handleContactSubmit)

	// Blog
	mux.HandleFunc("GET /blog", s.handleBlogIndex)
	mux.HandleFunc("GET /blog/search", s.handleBlogSearch)
	mux.HandleFunc("GET /blog/feed.xml", s.handleBlogFeed)
	mux.HandleFunc("GET /blog/{slug}", s.handleBlogPost)

	// HTMX endpoints
	mux.HandleFunc("GET /theme/toggle", s.handleThemeToggle)

	// SEO
	mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
	mux.HandleFunc("GET /robots.txt", s.handleRobots)

	// Admin
	mux.HandleFunc("GET /admin/login", s.handleAdminLoginPage)
	mux.HandleFunc("POST /admin/login", s.handleAdminLogin)
	mux.HandleFunc("GET /admin/logout", s.handleAdminLogout)
	mux.Handle("GET /admin", s.adminOnly(http.HandlerFunc(s.handleAdminDashboard)))
	mux.Handle("GET /admin/posts", s.adminOnly(http.HandlerFunc(s.handleAdminPosts)))
	mux.Handle("GET /admin/posts/new", s.adminOnly(http.HandlerFunc(s.handleAdminPostNew)))
	mux.Handle("POST /admin/posts/new", s.adminOnly(http.HandlerFunc(s.handleAdminPostNew)))
	mux.Handle("GET /admin/posts/{id}/edit", s.adminOnly(http.HandlerFunc(s.handleAdminPostEdit)))
	mux.Handle("POST /admin/posts/save", s.adminOnly(http.HandlerFunc(s.handleAdminPostSave)))
	mux.Handle("POST /admin/posts/{id}/delete", s.adminOnly(http.HandlerFunc(s.handleAdminPostDelete)))
	mux.Handle("POST /admin/posts/preview", s.adminOnly(http.HandlerFunc(s.handleAdminPostPreview)))
	mux.Handle("GET /admin/messages", s.adminOnly(http.HandlerFunc(s.handleAdminMessages)))
	mux.Handle("POST /admin/messages/{id}/delete", s.adminOnly(http.HandlerFunc(s.handleAdminMessageDelete)))
	mux.Handle("GET /admin/projects", s.adminOnly(http.HandlerFunc(s.handleAdminProjects)))
	mux.Handle("GET /admin/projects/new", s.adminOnly(http.HandlerFunc(s.handleAdminProjectNew)))
	mux.Handle("POST /admin/projects/new", s.adminOnly(http.HandlerFunc(s.handleAdminProjectSave)))
	mux.Handle("GET /admin/projects/{slug}/edit", s.adminOnly(http.HandlerFunc(s.handleAdminProjectEdit)))
	mux.Handle("POST /admin/projects/save", s.adminOnly(http.HandlerFunc(s.handleAdminProjectSave)))
	mux.Handle("POST /admin/projects/{slug}/delete", s.adminOnly(http.HandlerFunc(s.handleAdminProjectDelete)))
	mux.Handle("POST /admin/projects/{slug}/classify", s.adminOnly(http.HandlerFunc(s.handleAdminProjectClassify)))
	mux.Handle("POST /admin/projects/{slug}/visible", s.adminOnly(http.HandlerFunc(s.handleAdminProjectVisible)))
	mux.Handle("POST /admin/projects/{slug}/featured", s.adminOnly(http.HandlerFunc(s.handleAdminProjectFeatured)))
	mux.Handle("GET /admin/settings", s.adminOnly(http.HandlerFunc(s.handleAdminSettingsPage)))
	mux.Handle("POST /admin/settings", s.adminOnly(http.HandlerFunc(s.handleAdminSettingsSave)))

	// Admin File Manager
	mux.Handle("GET /admin/files", s.adminOnly(http.HandlerFunc(s.handleAdminFiles)))
	mux.Handle("POST /admin/files/upload", s.adminOnly(http.HandlerFunc(s.handleAdminFilesUpload)))
	mux.Handle("POST /admin/files/delete", s.adminOnly(http.HandlerFunc(s.handleAdminFilesDelete)))
	mux.Handle("POST /admin/files/mkdir", s.adminOnly(http.HandlerFunc(s.handleAdminFilesMkdir)))

	// Public uploaded files (storage/persisted/www)
	mux.Handle("GET /files/", s.filesHandler())

	// Static assets (embedded, cacheable)
	mux.Handle("GET /static/", s.staticHandler())

	// 404 for everything else
	mux.HandleFunc("/", s.handleNotFound)

	// Middleware chain: logging -> security headers -> analytics -> CSRF.
	// Admin routes additionally get an auth guard (wired per-route above
	// via adminOnly).
	return s.securityHeaders(s.logRequests(s.analytics(s.csrfGuard(mux))))
}

// initAdmin configures the single admin account from the environment.
func (s *Server) initAdmin() {
	user := os.Getenv("ADMIN_USER")
	if user == "" {
		user = "admin"
	}
	pass := os.Getenv("ADMIN_PASSWORD")
	if pass == "" {
		pass = "admin"
		log.Printf("⚠  ADMIN_PASSWORD not set — using default 'admin'. Set it in production.")
	}
	s.adminUser = user
	s.adminHash = bcryptHash(pass)

	// The components package renders the same process-wide CSRF token
	// into every admin form.
	components.SetCSRFToken(csrfTokenGlobal)
}
