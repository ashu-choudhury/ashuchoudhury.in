package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "session"

// ---------------------------------------------------------------------------
// Security headers

// securityHeaders applies sensible security headers, including a strict CSP
// that works with htmx (self-hosted script) and server-rendered HTML.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", strings.Join([]string{
			"default-src 'self'",
			"script-src 'self'",
			"style-src 'self'",
			"img-src 'self' data:",
			"font-src 'self'",
			"connect-src 'self'",
			"object-src 'none'",
			"base-uri 'self'",
			"frame-ancestors 'none'",
			"form-action 'self'",
		}, "; "))
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		next.ServeHTTP(w, r)
	})
}

// logRequests writes a concise access log line per request.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s, %s)", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start).Round(time.Microsecond))
	})
}

// ---------------------------------------------------------------------------
// Analytics

// analytics records a page view for public HTML pages. Fragments served to
// htmx (search, form results) are not double-counted; static assets, admin
// pages and bots are ignored.
func (s *Server) analytics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if !s.countView(r) {
			return
		}
		day := time.Now().UTC().Format("2006-01-02")
		if err := s.Store.RecordPageView(r.Context(), day, r.URL.Path); err != nil {
			log.Printf("analytics: record %s: %v", r.URL.Path, err)
		}
	})
}

func (s *Server) countView(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	if r.Header.Get("HX-Request") == "true" {
		return false // fragment requests — the parent page already counts
	}
	p := r.URL.Path
	if p == "/" {
		return true
	}
	if strings.HasPrefix(p, "/static/") || strings.HasPrefix(p, "/admin") {
		return false
	}
	if p == "/sitemap.xml" || p == "/robots.txt" || p == "/favicon.ico" {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	if strings.Contains(ua, "bot") || strings.Contains(ua, "crawl") || strings.Contains(ua, "spider") {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// Admin auth

// adminOnly guards /admin routes with the session cookie.
func (s *Server) adminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionToken(r)
		if token == "" {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		ok, err := s.Store.SessionValid(r.Context(), token)
		if err != nil || !ok {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrfGuard validates the csrf field on admin POST requests. The preview
// endpoint is stateless (renders markdown only) and is exempt.
func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/admin") && r.URL.Path != "/admin/posts/preview" {
			if !csrfOK(r.FormValue("csrf")) {
				http.Error(w, "invalid or missing CSRF token", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func csrfOK(given string) bool {
	return subtle.ConstantTimeCompare([]byte(given), []byte(csrfToken())) == 1
}

// sessionToken extracts the admin session cookie value.
func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// newSessionToken returns a fresh random token and stores the session.
func (s *Server) newSessionToken(w http.ResponseWriter) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	now := time.Now()
	if err := s.Store.CreateSession(rctx(), store.Session{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}); err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   7 * 24 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token, nil
}

// ---------------------------------------------------------------------------
// Password hashing

func bcryptHash(password string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("bcrypt: %v", err)
	}
	return h
}

// ---------------------------------------------------------------------------
// Login rate limiting

// loginAllowed returns false when the IP has exceeded the attempt budget.
func (s *Server) loginAllowed(ip string) bool {
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	hits := s.loginHits[ip]
	keep := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	s.loginHits[ip] = keep
	if len(keep) >= 5 {
		return false
	}
	s.loginHits[ip] = append(s.loginHits[ip], now)
	return true
}

// ---------------------------------------------------------------------------
// Misc helpers

// csrfToken returns the process-wide CSRF token used in admin forms.
func csrfToken() string { return csrfTokenGlobal }

// csrfTokenGlobal is set once at startup.
var csrfTokenGlobal = func() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}()

// rctx returns a background context for store calls.
func rctx() context.Context { return context.Background() }
