package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashu-choudhury/portfolio/store"
)

// seedSEOStore populates the memory store with one shown project, one hidden
// project and one published post so the sitemap has dynamic entries to test.
func seedSEOStore(t *testing.T) *store.Memory {
	t.Helper()
	ms := store.NewMemory()
	ctx := context.Background()
	if err := ms.UpsertProject(ctx, store.Project{
		Slug:    "demo-project",
		Name:    "Demo Project",
		Visible: true,
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Hidden projects must never appear in the sitemap.
	if err := ms.UpsertProject(ctx, store.Project{
		Slug:    "hidden-project",
		Name:    "Hidden Project",
		Visible: false,
	}); err != nil {
		t.Fatalf("seed hidden project: %v", err)
	}
	if _, err := ms.CreatePost(ctx, store.Post{
		Slug:      "demo-post",
		Title:     "Demo Post",
		Published: true,
	}); err != nil {
		t.Fatalf("seed post: %v", err)
	}
	return ms
}

func TestRobotsTXT(t *testing.T) {
	srv := New(seedSEOStore(t), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("robots.txt status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"User-agent: *",
		"Allow: /",
		"Disallow: /admin",
		"Sitemap: https://ashuchoudhury.in/sitemap.xml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt missing %q; got:\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Cache-Control"); ct == "" {
		t.Error("robots.txt should set Cache-Control")
	}
}

func TestSitemapXML(t *testing.T) {
	srv := New(seedSEOStore(t), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("sitemap status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"https://ashuchoudhury.in/",
		"https://ashuchoudhury.in/about",
		"https://ashuchoudhury.in/projects",
		"https://ashuchoudhury.in/blog",
		"https://ashuchoudhury.in/contact",
		"https://ashuchoudhury.in/projects/demo-project",
		"https://ashuchoudhury.in/blog/demo-post",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap missing %q; got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "hidden-project") {
		t.Error("sitemap must not include hidden projects")
	}
	if strings.Contains(body, "/admin") {
		t.Error("sitemap must not include admin paths")
	}
}

func TestLLMSTXT(t *testing.T) {
	srv := New(seedSEOStore(t), nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/llms.txt", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("llms.txt status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "# ") {
		t.Errorf("llms.txt must start with an H1 heading; got:\n%s", body)
	}
	for _, want := range []string{
		"https://ashuchoudhury.in/about",
		"https://ashuchoudhury.in/projects",
		"https://ashuchoudhury.in/blog",
		"https://ashuchoudhury.in/contact",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt missing %q; got:\n%s", want, body)
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("llms.txt content-type = %q, want text/markdown", ct)
	}
}

func TestTrailingSlashRedirect(t *testing.T) {
	srv := New(seedSEOStore(t), nil)
	handler := srv.Handler()

	tests := []struct {
		path string
		want string
	}{
		{"/about/", "/about"},
		{"/projects/", "/projects"},
		{"/about/?utm=x", "/about?utm=x"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != http.StatusMovedPermanently {
			t.Errorf("GET %s status = %d, want 301", tt.path, rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != tt.want {
			t.Errorf("GET %s Location = %q, want %q", tt.path, loc, tt.want)
		}
	}

	// The root and slash-less paths must pass through untouched.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET / status = %d, want 200", rec.Code)
	}
}
