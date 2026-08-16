package indexnow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSubmitSitemapSendsKeyAndURL(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := submit(context.Background(), srv.URL, "my-key", "https://ashuchoudhury.in/sitemap.xml")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	q, err := url.ParseQuery(strings.TrimPrefix(gotURL, "/indexnow?"))
	if err != nil {
		t.Fatalf("parse query %q: %v", gotURL, err)
	}
	if q.Get("key") != "my-key" {
		t.Errorf("key = %q, want my-key", q.Get("key"))
	}
	if q.Get("url") != "https://ashuchoudhury.in/sitemap.xml" {
		t.Errorf("url = %q, want sitemap URL", q.Get("url"))
	}
}

func TestSubmitSitemapSurfacesHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	err := submit(context.Background(), srv.URL, "k", "https://ashuchoudhury.in/sitemap.xml")
	if err == nil {
		t.Fatal("expected error for 400 response, got nil")
	}
}
