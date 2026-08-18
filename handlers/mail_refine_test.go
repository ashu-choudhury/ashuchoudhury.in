package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
)

// TestAdminMailRefine swaps the compose textarea for the AI rewrite and
// shows a success note, without disturbing the rest of the form.
func TestAdminMailRefine(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	mock := newMockAIServer(t, chatResponse("Hi Priya,\n\nThanks for the update — the timeline looks good.\n\nBest regards,\nAshu"))
	_ = ms.SetSetting(ctx, "ai_base_url", mock.srv.URL)
	_ = ms.SetSetting(ctx, "ai_api_key", "test-api-key")
	srv := New(ms, nil)

	form := url.Values{}
	form.Set("subject", "Re: Project timeline")
	form.Set("body", "hi priya thanks for update")
	req := httptest.NewRequest(http.MethodPost, "/admin/mail/refine", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-csrf"})
	req.Header.Set("X-CSRF-Token", "test-csrf")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refine status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="mail-body"`) {
		t.Errorf("response missing the compose textarea:\n%s", body)
	}
	if !strings.Contains(body, "Thanks for the update") {
		t.Errorf("textarea was not replaced with the refined draft:\n%s", body)
	}
	if !strings.Contains(body, "Refined with AI") {
		t.Errorf("missing success note:\n%s", body)
	}
	// The mock must have received the draft for rewriting.
	bodies := mock.requestBodies()
	if len(bodies) == 0 || !strings.Contains(bodies[0], "hi priya thanks for update") {
		t.Errorf("AI request did not include the draft: %v", bodies)
	}
}

// TestAdminMailRefineEmptyBody keeps the textarea and surfaces an error
// when there is nothing to refine.
func TestAdminMailRefineEmptyBody(t *testing.T) {
	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	mock := newMockAIServer(t, chatResponse("irrelevant"))
	_ = ms.SetSetting(ctx, "ai_base_url", mock.srv.URL)
	_ = ms.SetSetting(ctx, "ai_api_key", "test-api-key")
	srv := New(ms, nil)

	form := url.Values{}
	form.Set("body", "   ")
	req := httptest.NewRequest(http.MethodPost, "/admin/mail/refine", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-csrf"})
	req.Header.Set("X-CSRF-Token", "test-csrf")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refine status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "message body is empty") {
		t.Errorf("expected an empty-body error note, got:\n%s", rec.Body.String())
	}
	if mock.callCount() != 0 {
		t.Errorf("AI should not be called for an empty draft (calls=%d)", mock.callCount())
	}
}

// TestMailBodyToHTML confirms plain text and Markdown become formatted
// HTML while raw HTML passes through untouched.
func TestMailBodyToHTML(t *testing.T) {
	cases := []struct {
		name, in string
		wantHTML bool // whether the output should contain a <p> tag
	}{
		{"plain text", "Thanks for the update!", true},
		{"markdown", "## Notes\n\n- one\n- two", true},
		{"raw html", "<div><b>Hi</b></div>", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := mailBodyToHTML(tc.in)
			hasP := strings.Contains(out, "<p") || strings.Contains(out, "<h2") || strings.Contains(out, "<ul")
			if hasP != tc.wantHTML {
				t.Errorf("mailBodyToHTML(%q) = %q; want html=%v", tc.in, out, tc.wantHTML)
			}
			if tc.name == "raw html" && out != tc.in {
				t.Errorf("raw HTML should pass through unchanged, got %q", out)
			}
		})
	}
}
