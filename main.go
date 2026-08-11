// Command portfolio is a single-binary portfolio website built with
// Go + Templ + HTMX: typed server-rendered HTML, HTML-over-the-wire
// interactivity and zero client-authored JavaScript.
//
// It ships with a full data layer (SQLite by default), first-party
// analytics, a blog with a markdown editor, and an admin panel.
//
// Build:
//
//	templ generate
//	go build -o portfolio .
//
// Run:
//
//	ADMIN_PASSWORD=changeme ./portfolio   # listens on :8080
//
// Environment:
//
//	PORT             listen address (default 8080)
//	SITE_URL         canonical origin (default https://ashuchoudhury.in)
//	DB_PATH          SQLite database file (default storage/persisted/portfolio.db)
//	ADMIN_USER       admin username (default admin)
//	ADMIN_PASSWORD   admin password (default admin — set it!)
//
// Persistence (all optional, all from the environment — nothing hardcoded):
//
//	S3_BUCKET              bucket name — setting it enables S3 backups
//	S3_REGION              default us-east-1
//	S3_ENDPOINT            custom endpoint (Cloudflare R2, MinIO, …)
//	S3_PREFIX              object key prefix, default "portfolio/"
//	S3_INTERVAL            seconds between backups, default 300
//	AWS_ACCESS_KEY_ID      falls back to the default credential chain
//	AWS_SECRET_ACCESS_KEY
//	AWS_SESSION_TOKEN
//
// The whole site (templates, CSS, htmx) is embedded into one executable;
// only the SQLite database file lives on disk, under storage/persisted/.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ashu-choudhury/portfolio/data"
	"github.com/ashu-choudhury/portfolio/handlers"
	"github.com/ashu-choudhury/portfolio/storage"
	"github.com/ashu-choudhury/portfolio/store"
)

//go:embed all:static
var staticFS embed.FS

func main() {
	// ------------------------------------------------------------------
	// Database: the store is the center point. Swap implementations here —
	// store.OpenSQLite (persistent) or store.NewMemory (ephemeral) — and
	// nothing else in the application changes.
	dsn := os.Getenv("TURSO_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		dsn = os.Getenv("DB_PATH")
	}
	if dsn == "" {
		dsn = filepath.Join("storage", "persisted", "portfolio.db")
	}

	// ------------------------------------------------------------------
	// S3 proxy layer (zero-disk media streaming).
	backupCfg := storage.ConfigFromEnv()
	var backup *storage.Backup
	if backupCfg.Enabled {
		b, err := storage.NewBackup(backupCfg, log.Default())
		if err != nil {
			log.Printf("[S3 DIAGNOSTIC ERROR] Failed to initialize S3 proxy: %v", err)
		} else {
			backup = b
			log.Printf("[S3 DIAGNOSTIC SUCCESS] S3 Storage proxy initialized cleanly (Bucket: '%s', Region: '%s', Endpoint: '%s')", backupCfg.Bucket, backupCfg.Region, backupCfg.Endpoint)
		}
	} else {
		log.Printf("[S3 DIAGNOSTIC WARNING] S3 is disabled. S3_BUCKET env variable is empty!")
	}

	db, err := store.OpenSQLite(dsn)
	if err != nil {
		log.Fatalf("open database (%s): %v", dsn, err)
	}
	defer db.Close()

	// Seed the curated catalogue (idempotent; preserves admin overrides).
	ctx := rctx()
	if err := store.Seed(ctx, db); err != nil {
		log.Fatalf("seed database: %v", err)
	}

	// ------------------------------------------------------------------
	// Static assets from the embedded filesystem.
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static embed: %v", err)
	}

	// ------------------------------------------------------------------
	// Routes + middleware.
	server := handlers.New(db, staticSub)
	if backup != nil {
		server.SetBackup(backup, dsn)
	}

	addr := os.Getenv("PORT")
	if addr == "" {
		addr = "8080"
	}
	log.Printf("portfolio listening on :%s — %s (db: %s)", addr, data.BaseURL(), dsn)
	if backup != nil {
		log.Printf("s3: backups enabled → %s/%s every %s", backupCfg.Bucket, backupCfg.Key, backupCfg.Interval)
	}

	// ------------------------------------------------------------------
	// Serve. Graceful shutdown: on SIGINT/SIGTERM stop accepting new
	// requests, let in-flight ones finish, then run a final S3 backup so
	// the last writes are persisted.
	httpSrv := &http.Server{
		Addr:              ":" + addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	case sig := <-stop:
		log.Printf("received %s — shutting down", sig)
		shutCtx, cancel := context.WithTimeout(rctx(), 15*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}

	log.Printf("server shut down gracefully")
	log.Printf("bye")
}

// rctx returns a background context for boot-time store calls.
func rctx() context.Context { return context.Background() }
