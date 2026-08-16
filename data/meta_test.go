package data

import (
	"strings"
	"testing"

	"github.com/ashu-choudhury/portfolio/store"
)

// All public meta descriptions must stay within Google's ~160-character
// SERP truncation window. Long descriptions get cut off and waste the
// snippet.
func TestMetaDescriptionsWithinLimit(t *testing.T) {
	long := strings.Repeat("summary content ", 30)
	p := &store.Project{Slug: "demo", Name: "Demo", Tagline: "A demo project", Summary: long}
	post := &store.Post{Slug: "demo-post", Title: "Demo Post", Summary: long}

	pages := []struct {
		name string
		desc string
	}{
		{"home", HomeMeta("dark").Description},
		{"about", AboutMeta("dark").Description},
		{"contact", ContactMeta("dark").Description},
		{"projects", ProjectsMeta("dark", 10).Description},
		{"project", ProjectMeta(p, "dark").Description},
		{"blog", BlogIndexMeta("dark").Description},
		{"post", BlogPostMeta(post, "dark").Description},
	}
	for _, pg := range pages {
		if n := len([]rune(pg.desc)); n > 160 {
			t.Errorf("%s description is %d chars (max 160): %q", pg.name, n, pg.desc)
		}
	}
}

// Canonical URLs must always point at the bare domain — the canonical
// host (www, if used, redirects to it at the hosting layer).
func TestCanonicalURLsUseBareDomain(t *testing.T) {
	for path, meta := range map[string]PageMeta{
		"/":        HomeMeta("dark"),
		"/about":   AboutMeta("dark"),
		"/contact": ContactMeta("dark"),
		"/blog":    BlogIndexMeta("dark"),
	} {
		if !strings.HasPrefix(meta.Canonical, "https://ashuchoudhury.in") {
			t.Errorf("canonical for %s = %q, want bare domain host", path, meta.Canonical)
		}
	}
	if got := BaseURL(); got != "https://ashuchoudhury.in" {
		t.Errorf("BaseURL() = %q, want https://ashuchoudhury.in", got)
	}
}
