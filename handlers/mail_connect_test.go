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

func newAdminTestServer(t *testing.T) (*store.Memory, *Server) {
	t.Helper()
	ms := store.NewMemory()
	ctx := context.Background()
	_ = ms.CreateSession(ctx, store.Session{
		Token: "admin-session", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	return ms, New(ms, nil)
}

func adminConnectRequest(form url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/admin/mail/connect", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "admin-session"})
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-csrf"})
	req.Header.Set("X-CSRF-Token", "test-csrf")
	return req
}

// TestAdminMailConnectPastedToken is the Self Client path: a refresh token
// pasted on the connect form is saved directly and the OAuth redirect is
// skipped — the very flow that used to silently loop back to the connect
// screen when Zoho returned no refresh token.
func TestAdminMailConnectPastedToken(t *testing.T) {
	ms, srv := newAdminTestServer(t)
	ctx := context.Background()

	form := url.Values{}
	form.Set("client_id", "1000.CLIENT")
	form.Set("client_secret", "secret")
	form.Set("refresh_token", "1000.pasted-refresh-token")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, adminConnectRequest(form))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/mail" {
		t.Fatalf("redirect to %q, want /admin/mail (OAuth skip)", loc)
	}
	for k, want := range map[string]string{
		"zoho_client_id":     "1000.CLIENT",
		"zoho_client_secret": "secret",
		"zoho_refresh_token": "1000.pasted-refresh-token",
	} {
		if got, _ := ms.GetSetting(ctx, k); got != want {
			t.Errorf("setting %s = %q, want %q", k, got, want)
		}
	}
}

// TestAdminMailConnectWithoutToken keeps the OAuth redirect path: no pasted
// refresh token means the form redirects to the Zoho authorization flow.
func TestAdminMailConnectWithoutToken(t *testing.T) {
	_, srv := newAdminTestServer(t)

	form := url.Values{}
	form.Set("client_id", "1000.CLIENT")
	form.Set("client_secret", "secret")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, adminConnectRequest(form))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/admin/mail/oauth/start" {
		t.Fatalf("redirect to %q, want /admin/mail/oauth/start", loc)
	}
}

// TestAdminMailConnectRequiresCredentials guards the required fields.
func TestAdminMailConnectRequiresCredentials(t *testing.T) {
	_, srv := newAdminTestServer(t)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, adminConnectRequest(url.Values{}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "err=") {
		t.Fatalf("expected an error redirect, got %q", loc)
	}
}
