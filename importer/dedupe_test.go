package importer

import (
	"context"
	"testing"

	"github.com/ashu-choudhury/portfolio/store"
)

func mustUpsert(t *testing.T, ms *store.Memory, p store.Project) {
	t.Helper()
	if err := ms.UpsertProject(context.Background(), p); err != nil {
		t.Fatalf("upsert %s: %v", p.Slug, err)
	}
}

func TestDedupeMergesCaseVariants(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemory()
	mustUpsert(t, ms, store.Project{Slug: "ClipSync", Name: "ClipSync", Visible: true, Featured: true, Features: []string{"A"}})
	mustUpsert(t, ms, store.Project{Slug: "clipsync", Name: "ClipSync", Visible: true})

	if err := Dedupe(ctx, ms); err != nil {
		t.Fatalf("Dedupe: %v", err)
	}

	kept, err := ms.GetProject(ctx, "clipsync")
	if err != nil || kept == nil {
		t.Fatalf("canonical project clipsync missing after dedupe: %v", err)
	}
	if !kept.Visible || !kept.Featured {
		t.Errorf("visibility/featured overrides not merged: %+v", kept)
	}
	if len(kept.Features) != 1 || kept.Features[0] != "A" {
		t.Errorf("curated features not merged: %v", kept.Features)
	}
	if _, err := ms.GetProject(ctx, "ClipSync"); err == nil {
		t.Error("duplicate ClipSync should have been deleted")
	}
}

func TestDedupeMergesUnderscoreVariants(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemory()
	mustUpsert(t, ms, store.Project{Slug: "jiosaavn_dart", Name: "Jiosaavn DART", Visible: true})
	mustUpsert(t, ms, store.Project{Slug: "jiosaavn-dart", Name: "Jiosaavn Dart", Visible: true, Summary: "main"})

	if err := Dedupe(ctx, ms); err != nil {
		t.Fatalf("Dedupe: %v", err)
	}

	kept, err := ms.GetProject(ctx, "jiosaavn-dart")
	if err != nil || kept == nil {
		t.Fatalf("canonical jiosaavn-dart missing after dedupe: %v", err)
	}
	if kept.Summary != "main" {
		t.Errorf("content from duplicate not merged: %q", kept.Summary)
	}
	if _, err := ms.GetProject(ctx, "jiosaavn_dart"); err == nil {
		t.Error("duplicate jiosaavn_dart should have been deleted")
	}
}

func TestDedupeCanonicalSlugWinsRegardlessOfOrder(t *testing.T) {
	// The canonical slug is inserted first here (the reverse of the usual
	// case) — the non-canonical variant must still be the one removed.
	ctx := context.Background()
	ms := store.NewMemory()
	mustUpsert(t, ms, store.Project{Slug: "ibmtts-go-server", Name: "IBMTTS Go Server", Visible: true})
	mustUpsert(t, ms, store.Project{Slug: "IBMTTS-go-server", Name: "IBMTTS GO Server", Visible: true, Featured: true})

	if err := Dedupe(ctx, ms); err != nil {
		t.Fatalf("Dedupe: %v", err)
	}

	kept, err := ms.GetProject(ctx, "ibmtts-go-server")
	if err != nil || kept == nil {
		t.Fatalf("canonical ibmtts-go-server missing after dedupe: %v", err)
	}
	if !kept.Featured {
		t.Error("featured override from duplicate not merged")
	}
	if _, err := ms.GetProject(ctx, "IBMTTS-go-server"); err == nil {
		t.Error("duplicate IBMTTS-go-server should have been deleted")
	}
}

func TestDedupeLeavesDistinctProjects(t *testing.T) {
	ctx := context.Background()
	ms := store.NewMemory()
	mustUpsert(t, ms, store.Project{Slug: "ashu-choudhury", Name: "Ashu Choudhury", Visible: true})
	mustUpsert(t, ms, store.Project{Slug: "ashuchoudhury.in", Name: "Ashuchoudhury.In", Visible: true})

	if err := Dedupe(ctx, ms); err != nil {
		t.Fatalf("Dedupe: %v", err)
	}

	for _, slug := range []string{"ashu-choudhury", "ashuchoudhury.in"} {
		if _, err := ms.GetProject(ctx, slug); err != nil {
			t.Errorf("distinct project %s should remain: %v", slug, err)
		}
	}
}

func TestNormalizeSlug(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"ClipSync", "clipsync"},
		{"clipsync", "clipsync"},
		{"jiosaavn_dart", "jiosaavn-dart"},
		{"IBMTTS-go-server", "ibmtts-go-server"},
		{"Live-Internet-Speed-Tester-NVDA-addon", "live-internet-speed-tester-nvda-addon"},
	}
	for _, tt := range tests {
		if got := normalizeSlug(tt.in); got != tt.want {
			t.Errorf("normalizeSlug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
