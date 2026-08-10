// Package store defines the data-access center point for the entire site.
//
// Every handler talks to the Store interface — never to a concrete
// database — so the underlying database can be swapped without touching
// any other code. Two implementations ship with the app:
//
//   - store/sqlite: a persistent SQLite database (the default, pure-Go driver)
//   - store/memory: an in-memory store, used for tests and demos
//
// To swap databases, implement Store and pass the new instance to
// handlers.New in main.go — nothing else changes.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by Get* methods when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Classification describes whether a repository is genuinely the owner's
// work. The GitHub importer computes this; the admin panel can override it.
type Classification string

const (
	// ClassificationOriginal means the repo was created by the owner
	// (not a GitHub fork).
	ClassificationOriginal Classification = "original"
	// ClassificationRewritten means the repo was forked from someone else
	// but the owner substantially rewrote, extended or repurposed it.
	ClassificationRewritten Classification = "rewritten"
	// ClassificationClone means the repo is a fork/clone of someone else's
	// work with no meaningful contribution by the owner — hidden by default.
	ClassificationClone Classification = "clone"
)

// Project is a portfolio project sourced from GitHub (or curated locally).
type Project struct {
	Slug           string
	Name           string
	Tagline        string
	Summary        string
	Description    string
	Homepage       string
	Language       string
	Topics         []string
	Stars          int
	Forks          int
	IsFork         bool
	ParentFullName string
	Classification Classification
	Visible        bool
	Featured       bool
	Fingerprint    []string // inferred stack signals, e.g. "Android · Compose"
	Stack          []string // display stack chips
	Features       []string // key features for the detail page
	Links          []Link
	Year           string
	Accent         string // CSS gradient class name (accent-<slug> in style.css)
	Mono           string // monogram shown in card art
	RepoURL        string
	Source         string // "github" or "local"
	CreatedAt      string // ISO date
	PushedAt       string // ISO date — used for recency sorting
}

// Link is an external URL attached to a project or post.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Icon  string `json:"icon,omitempty"`
}

// Post is a blog post. Body is Markdown.
type Post struct {
	ID          int64
	Slug        string
	Title       string
	Summary     string
	Body        string
	Tags        []string
	Published   bool
	PublishedAt time.Time
	UpdatedAt   time.Time
}

// Message is a contact-form submission.
type Message struct {
	ID        int64
	Name      string
	Email     string
	Body      string
	CreatedAt time.Time
	Read      bool
}

// PageViewStat aggregates page views for a single path.
type PageViewStat struct {
	Path  string
	Count int64
}

// DailyStat aggregates page views for a single day.
type DailyStat struct {
	Date  string // YYYY-MM-DD
	Count int64
}

// Session is an authenticated admin session.
type Session struct {
	Token     string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Store is the data-access center point for the application.
type Store interface {
	// Projects
	ListProjects(ctx context.Context) ([]Project, error)
	// ListShownProjects returns visible, non-clone projects sorted by
	// most-recently-updated first (the site's canonical project order).
	ListShownProjects(ctx context.Context) ([]Project, error)
	GetProject(ctx context.Context, slug string) (*Project, error)
	UpsertProject(ctx context.Context, p Project) error
	// RenameProjectSlug migrates a project to a new slug, preserving all
	// columns (visibility, classification, featured, analytics). It is a
	// no-op when either slug does not exist.
	RenameProjectSlug(ctx context.Context, oldSlug, newSlug string) error
	SetProjectFeatured(ctx context.Context, slug string, featured bool) error
	SetProjectVisible(ctx context.Context, slug string, visible bool) error
	SetProjectClassification(ctx context.Context, slug string, c Classification) error
	DeleteProject(ctx context.Context, slug string) error

	// Posts
	ListPosts(ctx context.Context, includeDrafts bool) ([]Post, error)
	GetPost(ctx context.Context, slug string) (*Post, error)
	GetPostByID(ctx context.Context, id int64) (*Post, error)
	CreatePost(ctx context.Context, p Post) (int64, error)
	UpdatePost(ctx context.Context, p Post) error
	DeletePost(ctx context.Context, id int64) error
	SearchPosts(ctx context.Context, query string) ([]Post, error)

	// Messages
	CreateMessage(ctx context.Context, m Message) error
	ListMessages(ctx context.Context) ([]Message, error)
	DeleteMessage(ctx context.Context, id int64) error

	// Analytics
	RecordPageView(ctx context.Context, day, path string) error
	PageViewTotal(ctx context.Context) (int64, error)
	TopPages(ctx context.Context, limit int) ([]PageViewStat, error)
	DailyViews(ctx context.Context, days int) ([]DailyStat, error)
	PageViewsFor(ctx context.Context, path string) (int64, error)

	// Sessions
	CreateSession(ctx context.Context, s Session) error
	SessionValid(ctx context.Context, token string) (bool, error)
	DeleteSession(ctx context.Context, token string) error

	// Settings
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error

	// Close releases the underlying resources.
	Close() error
}

// Helpers -------------------------------------------------------------

// Shown filters projects to those surfaced on the public site and sorts
// them newest-first by pushed date. Used by the memory store and available
// to any implementation.
func Shown(projects []Project) []Project {
	out := make([]Project, 0, len(projects))
	for _, p := range projects {
		if p.Visible && p.Classification != ClassificationClone {
			out = append(out, p)
		}
	}
	// Stable sort: newest pushed date first; fall back to slug.
	sortProjects(out)
	return out
}

// sortProjects sorts projects by pushed date descending (empty dates last).
func sortProjects(ps []Project) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0; j-- {
			if moreRecent(ps[j], ps[j-1]) {
				ps[j], ps[j-1] = ps[j-1], ps[j]
			}
		}
	}
}

// moreRecent reports whether a is strictly more recent than b.
func moreRecent(a, b Project) bool {
	if a.PushedAt != b.PushedAt {
		return a.PushedAt > b.PushedAt
	}
	return a.Slug < b.Slug
}
