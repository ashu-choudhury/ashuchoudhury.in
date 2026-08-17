package store

import (
	"context"
	"testing"
	"time"
)

func testAIGenJob(id string, createdAt time.Time) AIGenJob {
	return AIGenJob{
		ID:         id,
		Status:     "queued",
		Stage:      "Preparing…",
		Model:      "gpt-4o-mini",
		TopicHint:  "Docker",
		Publish:    true,
		Title:      "Running Docker on Android",
		Slug:       "running-docker-on-android",
		Summary:    "A hands-on guide.",
		Tags:       []string{"docker", "android"},
		PostID:     7,
		CreatedAt:  createdAt,
		FinishedAt: createdAt.Add(2 * time.Minute),
	}
}

// testStores returns the two Store implementations sharing one test body.
func testStores(t *testing.T) map[string]Store {
	mem := NewMemory()
	sql, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("open sqlite :memory:: %v", err)
	}
	t.Cleanup(func() { _ = sql.Close() })
	return map[string]Store{"memory": mem, "sqlite": sql}
}

func TestAIGenJobUpsertAndList(t *testing.T) {
	ctx := context.Background()
	for name, s := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			older := testAIGenJob("job-old", now.Add(-time.Hour))
			older.Status = "published"
			newer := testAIGenJob("job-new", now)
			newer.Status = "failed"
			newer.Error = "boom"

			if err := s.UpsertAIGenJob(ctx, older); err != nil {
				t.Fatalf("upsert older: %v", err)
			}
			if err := s.UpsertAIGenJob(ctx, newer); err != nil {
				t.Fatalf("upsert newer: %v", err)
			}

			// Update in place (upsert semantics) and verify the change sticks.
			newer.Status = "done"
			if err := s.UpsertAIGenJob(ctx, newer); err != nil {
				t.Fatalf("upsert update: %v", err)
			}

			jobs, err := s.ListAIGenJobs(ctx, 0)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(jobs) != 2 {
				t.Fatalf("expected 2 jobs, got %d", len(jobs))
			}
			// Newest first.
			if jobs[0].ID != "job-new" || jobs[0].Status != "done" {
				t.Errorf("expected job-new first with updated status, got %+v", jobs[0])
			}
			if jobs[1].ID != "job-old" || jobs[1].Status != "published" {
				t.Errorf("expected job-old second, got %+v", jobs[1])
			}
			if len(jobs[1].Tags) != 2 || jobs[1].Tags[0] != "docker" {
				t.Errorf("tags not round-tripped: %v", jobs[1].Tags)
			}
			if jobs[1].PostID != 7 || jobs[1].Publish != true {
				t.Errorf("fields not round-tripped: %+v", jobs[1])
			}

			// Limit.
			limited, err := s.ListAIGenJobs(ctx, 1)
			if err != nil {
				t.Fatalf("list limited: %v", err)
			}
			if len(limited) != 1 || limited[0].ID != "job-new" {
				t.Errorf("limit=1 failed: %+v", limited)
			}
		})
	}
}

func TestFailStaleAIGenJobs(t *testing.T) {
	ctx := context.Background()
	for name, s := range testStores(t) {
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			stale := testAIGenJob("stale", now.Add(-30*time.Minute)) // non-terminal, old
			fresh := testAIGenJob("fresh", now.Add(-time.Minute))    // non-terminal, recent
			terminal := testAIGenJob("terminal", now.Add(-time.Hour))
			terminal.Status = "published"

			for _, j := range []AIGenJob{stale, fresh, terminal} {
				if err := s.UpsertAIGenJob(ctx, j); err != nil {
					t.Fatalf("upsert: %v", err)
				}
			}

			cutoff := now.Add(-10 * time.Minute)
			n, err := s.FailStaleAIGenJobs(ctx, cutoff, "interrupted")
			if err != nil {
				t.Fatalf("fail stale: %v", err)
			}
			if n != 1 {
				t.Errorf("expected exactly 1 stale job marked, got %d", n)
			}

			jobs, _ := s.ListAIGenJobs(ctx, 0)
			byID := map[string]AIGenJob{}
			for _, j := range jobs {
				byID[j.ID] = j
			}
			if j := byID["stale"]; j.Status != "failed" || j.Error != "interrupted" {
				t.Errorf("stale job not failed: %+v", j)
			}
			if j := byID["fresh"]; j.Status != "queued" {
				t.Errorf("fresh job must be untouched: %+v", j)
			}
			if j := byID["terminal"]; j.Status != "published" {
				t.Errorf("terminal job must be untouched: %+v", j)
			}
		})
	}
}
