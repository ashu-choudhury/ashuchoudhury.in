package store

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory is an in-memory Store implementation used for tests, demos and
// zero-setup local runs. It implements the same Store interface as SQLite,
// which is the point of the center-point design: swap the constructor and
// nothing else changes.
type Memory struct {
	mu       sync.RWMutex
	projects map[string]Project
	posts    map[int64]Post
	nextPost int64
	messages []Message
	nextMsg  int64
	views    map[string]int64 // "date|path" -> count
	sessions map[string]Session
	settings map[string]string
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		projects: map[string]Project{},
		posts:    map[int64]Post{},
		messages: []Message{},
		views:    map[string]int64{},
		sessions: map[string]Session{},
		settings: map[string]string{},
	}
}

// Close implements Store.
func (m *Memory) Close() error { return nil }

// ---------------------------------------------------------------------------
// Projects

// ListProjects implements Store.
func (m *Memory) ListProjects(ctx context.Context) ([]Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Project, 0, len(m.projects))
	for _, p := range m.projects {
		out = append(out, p)
	}
	return out, nil
}

// ListShownProjects implements Store.
func (m *Memory) ListShownProjects(ctx context.Context) ([]Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	all := make([]Project, 0, len(m.projects))
	for _, p := range m.projects {
		all = append(all, p)
	}
	return Shown(all), nil
}

// GetProject implements Store.
func (m *Memory) GetProject(ctx context.Context, slug string) (*Project, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.projects[slug]
	if !ok {
		return nil, ErrNotFound
	}
	return &p, nil
}

// UpsertProject implements Store.
func (m *Memory) UpsertProject(ctx context.Context, p Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projects[p.Slug] = p
	return nil
}

// RenameProjectSlug implements Store.
func (m *Memory) RenameProjectSlug(ctx context.Context, oldSlug, newSlug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[oldSlug]
	if !ok {
		return nil
	}
	if _, taken := m.projects[newSlug]; taken {
		return nil
	}
	delete(m.projects, oldSlug)
	m.projects[newSlug] = p
	return nil
}

// SetProjectFeatured implements Store.
func (m *Memory) SetProjectFeatured(ctx context.Context, slug string, featured bool) error {
	return m.updateProject(slug, func(p *Project) { p.Featured = featured })
}

// SetProjectVisible implements Store.
func (m *Memory) SetProjectVisible(ctx context.Context, slug string, visible bool) error {
	return m.updateProject(slug, func(p *Project) { p.Visible = visible })
}

// SetProjectClassification implements Store.
func (m *Memory) SetProjectClassification(ctx context.Context, slug string, c Classification) error {
	return m.updateProject(slug, func(p *Project) { p.Classification = c })
}

// DeleteProject implements Store.
func (m *Memory) DeleteProject(ctx context.Context, slug string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.projects, slug)
	return nil
}

func (m *Memory) updateProject(slug string, fn func(*Project)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projects[slug]
	if !ok {
		return ErrNotFound
	}
	fn(&p)
	m.projects[slug] = p
	return nil
}

// ---------------------------------------------------------------------------
// Posts

// ListPosts implements Store.
func (m *Memory) ListPosts(ctx context.Context, includeDrafts bool) ([]Post, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Post
	for _, p := range m.posts {
		if includeDrafts || p.Published {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	return out, nil
}

// GetPost implements Store.
func (m *Memory) GetPost(ctx context.Context, slug string) (*Post, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.posts {
		if p.Slug == slug {
			cp := p
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// GetPostByID implements Store.
func (m *Memory) GetPostByID(ctx context.Context, id int64) (*Post, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.posts[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := p
	return &cp, nil
}

// CreatePost implements Store.
func (m *Memory) CreatePost(ctx context.Context, p Post) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPost++
	p.ID = m.nextPost
	if p.PublishedAt.IsZero() {
		p.PublishedAt = time.Now().UTC()
	}
	p.UpdatedAt = time.Now().UTC()
	m.posts[p.ID] = p
	return p.ID, nil
}

// UpdatePost implements Store.
func (m *Memory) UpdatePost(ctx context.Context, p Post) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.posts[p.ID]; !ok {
		return ErrNotFound
	}
	p.UpdatedAt = time.Now().UTC()
	m.posts[p.ID] = p
	return nil
}

// DeletePost implements Store.
func (m *Memory) DeletePost(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.posts, id)
	return nil
}

// SearchPosts implements Store (simple substring search over published posts).
func (m *Memory) SearchPosts(ctx context.Context, query string) ([]Post, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	q := strings.ToLower(query)
	var out []Post
	for _, p := range m.posts {
		if !p.Published {
			continue
		}
		hay := strings.ToLower(p.Title + " " + p.Summary + " " + p.Body + " " + strings.Join(p.Tags, " "))
		if strings.Contains(hay, q) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	return out, nil
}

// ---------------------------------------------------------------------------
// Messages

// CreateMessage implements Store.
func (m *Memory) CreateMessage(ctx context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextMsg++
	msg.ID = m.nextMsg
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now().UTC()
	}
	m.messages = append(m.messages, msg)
	return nil
}

// ListMessages implements Store.
func (m *Memory) ListMessages(ctx context.Context) ([]Message, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Message, len(m.messages))
	copy(out, m.messages)
	return out, nil
}

// DeleteMessage implements Store.
func (m *Memory) DeleteMessage(ctx context.Context, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, msg := range m.messages {
		if msg.ID == id {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			return nil
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Analytics

// RecordPageView implements Store.
func (m *Memory) RecordPageView(ctx context.Context, day, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.views[day+"|"+path]++
	return nil
}

// PageViewTotal implements Store.
func (m *Memory) PageViewTotal(ctx context.Context) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int64
	for _, c := range m.views {
		n += c
	}
	return n, nil
}

// TopPages implements Store.
func (m *Memory) TopPages(ctx context.Context, limit int) ([]PageViewStat, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	agg := map[string]int64{}
	for k, c := range m.views {
		path := k[strings.Index(k, "|")+1:]
		agg[path] += c
	}
	var out []PageViewStat
	for path, c := range agg {
		out = append(out, PageViewStat{Path: path, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DailyViews implements Store.
func (m *Memory) DailyViews(ctx context.Context, days int) ([]DailyStat, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	agg := map[string]int64{}
	for k, c := range m.views {
		date := k[:strings.Index(k, "|")]
		if date < cutoff {
			continue
		}
		agg[date] += c
	}
	var out []DailyStat
	for d, c := range agg {
		out = append(out, DailyStat{Date: d, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out, nil
}

// PageViewsFor implements Store.
func (m *Memory) PageViewsFor(ctx context.Context, path string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var n int64
	for k, c := range m.views {
		if strings.HasSuffix(k, "|"+path) {
			n += c
		}
	}
	return n, nil
}

// ---------------------------------------------------------------------------
// Sessions

// CreateSession implements Store.
func (m *Memory) CreateSession(ctx context.Context, s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[s.Token] = s
	return nil
}

// SessionValid implements Store.
func (m *Memory) SessionValid(ctx context.Context, token string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[token]
	if !ok {
		return false, nil
	}
	if time.Now().After(s.ExpiresAt) {
		return false, nil
	}
	return true, nil
}

// DeleteSession implements Store.
func (m *Memory) DeleteSession(ctx context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

// ---------------------------------------------------------------------------
// Settings

// GetSetting implements Store.
func (m *Memory) GetSetting(ctx context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.settings[key], nil
}

// SetSetting implements Store.
func (m *Memory) SetSetting(ctx context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.settings[key] = value
	return nil
}

// Ping implements Store.
func (m *Memory) Ping(ctx context.Context) error {
	return nil
}
