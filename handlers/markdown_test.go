package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
)

func TestRenderMarkdownRichWrapsFencedCodeWithCopyButton(t *testing.T) {
	html := renderMarkdownRich("```go\npackage main\n\nfunc main() {}\n```")
	for _, want := range []string{
		`class="code-block"`,
		`class="code-block-lang">go</span>`,
		`class="code-copy-btn"`,
		`>Copy</button>`,
		`<code class="language-go">`,
		"func main() {}",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("rich markdown missing %q; got:\n%s", want, html)
		}
	}
	// The header must come before the code itself.
	if strings.Index(html, "code-copy-btn") > strings.Index(html, "func main") {
		t.Error("copy button should appear before the code content")
	}
}

func TestRenderMarkdownRichWrapsIndentedCode(t *testing.T) {
	html := renderMarkdownRich("    indented := true\n")
	if !strings.Contains(html, `class="code-block"`) {
		t.Errorf("indented code block not wrapped:\n%s", html)
	}
	if !strings.Contains(html, "indented := true") {
		t.Errorf("indented code content missing:\n%s", html)
	}
}

func TestRenderMarkdownRichGenericLabelWithoutLanguage(t *testing.T) {
	html := renderMarkdownRich("```\nno language\n```")
	if !strings.Contains(html, `class="code-block-lang">code</span>`) {
		t.Errorf("unlabeled code block should show a generic label:\n%s", html)
	}
}

func TestRenderMarkdownRichEscapesCodeContent(t *testing.T) {
	html := renderMarkdownRich("```html\n<div class=\"x\">&amp;</div>\n```")
	if strings.Contains(html, `<div class="x">`) {
		t.Errorf("code content must be HTML-escaped:\n%s", html)
	}
	if !strings.Contains(html, "&lt;div") {
		t.Errorf("expected escaped code content:\n%s", html)
	}
}

func TestRenderMarkdownPlainHasNoCopyButton(t *testing.T) {
	html := renderMarkdown("```go\nx := 1\n```")
	if strings.Contains(html, "code-copy-btn") {
		t.Errorf("plain renderer (RSS) must not include copy buttons:\n%s", html)
	}
	if !strings.Contains(html, "<pre>") {
		t.Errorf("plain renderer should still render the code block:\n%s", html)
	}
}

func TestBlogPostPageRendersCopyButton(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	_, _ = ms.CreatePost(ctx, store.Post{
		Slug:        "code-post",
		Title:       "Code Post",
		Published:   true,
		PublishedAt: time.Now().UTC(),
		Body:        "```bash\necho hi\n```",
	})
	srv := New(ms, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/blog/code-post", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("blog post status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "code-copy-btn") {
		t.Errorf("blog post page missing copy button:\n%s", rec.Body.String())
	}
}

func TestRSSFeedHasNoCopyButton(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	_, _ = ms.CreatePost(ctx, store.Post{
		Slug:        "code-post",
		Title:       "Code Post",
		Published:   true,
		PublishedAt: time.Now().UTC(),
		Body:        "```bash\necho hi\n```",
	})
	srv := New(ms, nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/blog/feed.xml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("feed status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "code-copy-btn") {
		t.Error("RSS feed must not include copy buttons")
	}
	if !strings.Contains(rec.Body.String(), "echo hi") {
		t.Error("RSS feed should still contain the code content")
	}
}

func TestAdminPreviewRendersCopyButton(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	srv := New(ms, nil)
	handler := srv.Handler()

	// The preview endpoint is CSRF-exempt; it only needs a session.
	req := httptest.NewRequest(http.MethodPost, "/admin/posts/preview", strings.NewReader("body=```go%0Aprintln(1)%0A```"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "code-copy-btn") {
		t.Errorf("admin preview missing copy button:\n%s", rec.Body.String())
	}
}
