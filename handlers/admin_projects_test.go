package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashu-choudhury/portfolio/store"
)

func TestProjectCRUDAndAttachmentUpload(t *testing.T) {
	memStore := store.NewMemory()
	srv := &Server{Store: memStore}

	// 1. Create a new project via UpsertProject
	proj := store.Project{
		Slug:        "test-project",
		Name:        "Test Project",
		Tagline:     "Awesome test project",
		Summary:     "Short summary",
		Description: "# Heading\nFull markdown details.",
		Language:    "Kotlin",
		Year:        "2026",
		Visible:     true,
		Featured:    true,
	}

	ctx := context.Background()
	if err := memStore.UpsertProject(ctx, proj); err != nil {
		t.Fatalf("UpsertProject failed: %v", err)
	}

	// 2. Fetch project
	fetched, err := memStore.GetProject(ctx, "test-project")
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if fetched.Name != "Test Project" || fetched.Description != "# Heading\nFull markdown details." {
		t.Errorf("unexpected project fetched: %+v", fetched)
	}

	// 3. Test Delete
	if err := memStore.DeleteProject(ctx, "test-project"); err != nil {
		t.Fatalf("DeleteProject failed: %v", err)
	}

	_, err = memStore.GetProject(ctx, "test-project")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound after deletion, got %v", err)
	}

	// 4. Test Attachment directory creation
	targetDir := filepath.Join(wwwDirRoot(), "projects", "demo-attach")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		t.Fatalf("mkdir projects attachment failed: %v", err)
	}
	testFile := filepath.Join(targetDir, "spec.pdf")
	if err := os.WriteFile(testFile, []byte("PDF content"), 0644); err != nil {
		t.Fatalf("write spec file: %v", err)
	}
	defer os.RemoveAll(targetDir)

	// Verify public route serving for project attachment
	handler := srv.filesHandler()
	req := httptest.NewRequest(http.MethodGet, "/files/projects/demo-attach/spec.pdf", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for project attachment when S3 unconfigured, got %d", rec.Code)
	}
}

func TestParseProjectForm(t *testing.T) {
	srv := &Server{Store: store.NewMemory()}

	form := url.Values{}
	form.Set("name", "New Engine")
	form.Set("slug", "new-engine")
	form.Set("tagline", "Fast engine")
	form.Set("summary", "Summary")
	form.Set("description", "## Engine Details")
	form.Set("language", "Go")
	form.Set("year", "2026")
	form.Set("stack", "Go, SQLite")
	form.Set("features", "Async I/O\nZero Copy")
	form.Set("visible", "on")

	req := httptest.NewRequest(http.MethodPost, "/admin/projects/save", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	p, _, errMsg := srv.parseProjectForm(req)
	if errMsg != "" {
		t.Fatalf("unexpected parse error: %s", errMsg)
	}
	if p.Name != "New Engine" || p.Slug != "new-engine" || !p.Visible {
		t.Errorf("parsed project mismatch: %+v", p)
	}
	if len(p.Stack) != 2 || p.Stack[0] != "Go" {
		t.Errorf("stack parsing failed: %v", p.Stack)
	}
	if len(p.Features) != 2 || p.Features[0] != "Async I/O" {
		t.Errorf("features parsing failed: %v", p.Features)
	}
}

func TestAdminProjectsSyncRoute(t *testing.T) {
	memStore := store.NewMemory()
	srv := New(memStore, nil)
	handler := srv.Handler()

	// Test unauthenticated GET request to admin projects redirects to login
	req := httptest.NewRequest(http.MethodGet, "/admin/projects", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/admin/login" {
		t.Errorf("expected redirect to /admin/login for unauthenticated request, got code %d, loc %s", rec.Code, rec.Header().Get("Location"))
	}
}

