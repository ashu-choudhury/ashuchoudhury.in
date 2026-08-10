package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, keeps CGO_ENABLED=0 single-binary builds
)

// schema is applied on every boot; it is idempotent (IF NOT EXISTS).
const schema = `
CREATE TABLE IF NOT EXISTS projects (
	slug            TEXT PRIMARY KEY,
	name            TEXT NOT NULL,
	tagline         TEXT NOT NULL DEFAULT '',
	summary         TEXT NOT NULL DEFAULT '',
	description     TEXT NOT NULL DEFAULT '',
	homepage        TEXT NOT NULL DEFAULT '',
	language        TEXT NOT NULL DEFAULT '',
	topics          TEXT NOT NULL DEFAULT '[]',
	stars           INTEGER NOT NULL DEFAULT 0,
	forks           INTEGER NOT NULL DEFAULT 0,
	is_fork         INTEGER NOT NULL DEFAULT 0,
	parent_full_name TEXT NOT NULL DEFAULT '',
	classification  TEXT NOT NULL DEFAULT 'original',
	visible         INTEGER NOT NULL DEFAULT 1,
	featured        INTEGER NOT NULL DEFAULT 0,
	fingerprint     TEXT NOT NULL DEFAULT '[]',
	stack           TEXT NOT NULL DEFAULT '[]',
	features        TEXT NOT NULL DEFAULT '[]',
	links           TEXT NOT NULL DEFAULT '[]',
	year            TEXT NOT NULL DEFAULT '',
	accent          TEXT NOT NULL DEFAULT '',
	mono            TEXT NOT NULL DEFAULT '',
	repo_url        TEXT NOT NULL DEFAULT '',
	source          TEXT NOT NULL DEFAULT 'github',
	created_at      TEXT NOT NULL DEFAULT '',
	pushed_at       TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_projects_shown ON projects(visible, classification, pushed_at);

CREATE TABLE IF NOT EXISTS posts (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	slug          TEXT NOT NULL UNIQUE,
	title         TEXT NOT NULL,
	summary       TEXT NOT NULL DEFAULT '',
	body          TEXT NOT NULL DEFAULT '',
	tags          TEXT NOT NULL DEFAULT '[]',
	published     INTEGER NOT NULL DEFAULT 0,
	published_at  TEXT NOT NULL DEFAULT '',
	updated_at    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_posts_published ON posts(published, published_at);

CREATE VIRTUAL TABLE IF NOT EXISTS posts_fts USING fts5(
	slug, title, summary, body,
	content='posts', content_rowid='id'
);
CREATE TRIGGER IF NOT EXISTS posts_ai AFTER INSERT ON posts BEGIN
	INSERT INTO posts_fts(rowid, slug, title, summary, body)
	VALUES (new.id, new.slug, new.title, new.summary, new.body);
END;
CREATE TRIGGER IF NOT EXISTS posts_ad AFTER DELETE ON posts BEGIN
	INSERT INTO posts_fts(posts_fts, rowid, slug, title, summary, body)
	VALUES ('delete', old.id, old.slug, old.title, old.summary, old.body);
END;
CREATE TRIGGER IF NOT EXISTS posts_au AFTER UPDATE ON posts BEGIN
	INSERT INTO posts_fts(posts_fts, rowid, slug, title, summary, body)
	VALUES ('delete', old.id, old.slug, old.title, old.summary, old.body);
	INSERT INTO posts_fts(rowid, slug, title, summary, body)
	VALUES (new.id, new.slug, new.title, new.summary, new.body);
END;

CREATE TABLE IF NOT EXISTS messages (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	name       TEXT NOT NULL,
	email      TEXT NOT NULL,
	body       TEXT NOT NULL,
	created_at TEXT NOT NULL,
	read       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS page_views (
	date  TEXT NOT NULL,
	path  TEXT NOT NULL,
	count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (date, path)
);

CREATE TABLE IF NOT EXISTS sessions (
	token      TEXT PRIMARY KEY,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// SQLite implements Store backed by a SQLite database.
type SQLite struct {
	db *sql.DB
}

// OpenSQLite opens (creating if needed) the database at dsn and applies the
// schema. Use ":memory:" for an ephemeral database (tests, demos).
func OpenSQLite(dsn string) (*SQLite, error) {
	// Ensure the parent directory exists for file-backed databases (e.g.
	// storage/persisted/portfolio.db) so a fresh checkout just works.
	if dsn != ":memory:" {
		if dir := filepath.Dir(dsn); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("create db dir: %w", err)
			}
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// modernc.org/sqlite: a single connection avoids SQLITE_BUSY on writes.
	db.SetMaxOpenConns(1)
	// WAL mode lets a second process (e.g. the GitHub importer) read and
	// write while the site server is running; busy_timeout waits instead
	// of failing immediately when a write lock is momentarily held.
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &SQLite{db: db}, nil
}

// Close implements Store.
func (s *SQLite) Close() error { return s.db.Close() }

// Checkpoint flushes the WAL into the main database file. Call it before
// backing the file up (e.g. to S3) so the backup is a consistent snapshot.
func (s *SQLite) Checkpoint(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// ---------------------------------------------------------------------------
// Projects

const projectCols = `slug, name, tagline, summary, description, homepage, language,
topics, stars, forks, is_fork, parent_full_name, classification, visible, featured,
fingerprint, stack, features, links, year, accent, mono, repo_url, source, created_at, pushed_at`

func scanProject(row interface{ Scan(...any) error }) (*Project, error) {
	var (
		p                    Project
		topics, stack, feats string
		links, fp            string
		visible, featured    int
	)
	err := row.Scan(&p.Slug, &p.Name, &p.Tagline, &p.Summary, &p.Description,
		&p.Homepage, &p.Language, &topics, &p.Stars, &p.Forks, &p.IsFork,
		&p.ParentFullName, (*string)(&p.Classification), &visible, &featured,
		&fp, &stack, &feats, &links, &p.Year, &p.Accent, &p.Mono,
		&p.RepoURL, &p.Source, &p.CreatedAt, &p.PushedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(topics), &p.Topics)
	_ = json.Unmarshal([]byte(stack), &p.Stack)
	_ = json.Unmarshal([]byte(feats), &p.Features)
	_ = json.Unmarshal([]byte(links), &p.Links)
	_ = json.Unmarshal([]byte(fp), &p.Fingerprint)
	p.Visible = visible != 0
	p.Featured = featured != 0
	return &p, nil
}

func projectArgs(p Project) []any {
	topics, _ := json.Marshal(p.Topics)
	stack, _ := json.Marshal(p.Stack)
	feats, _ := json.Marshal(p.Features)
	links, _ := json.Marshal(p.Links)
	fp, _ := json.Marshal(p.Fingerprint)
	visible, featured := 0, 0
	if p.Visible {
		visible = 1
	}
	if p.Featured {
		featured = 1
	}
	isFork := 0
	if p.IsFork {
		isFork = 1
	}
	return []any{p.Slug, p.Name, p.Tagline, p.Summary, p.Description, p.Homepage,
		p.Language, string(topics), p.Stars, p.Forks, isFork, p.ParentFullName,
		string(p.Classification), visible, featured, string(fp), string(stack),
		string(feats), string(links), p.Year, p.Accent, p.Mono, p.RepoURL, p.Source,
		p.CreatedAt, p.PushedAt}
}

// ListProjects implements Store.
func (s *SQLite) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+projectCols+" FROM projects")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ListShownProjects implements Store.
func (s *SQLite) ListShownProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+projectCols+" FROM projects WHERE visible = 1 AND classification != 'clone' ORDER BY pushed_at DESC, slug ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetProject implements Store.
func (s *SQLite) GetProject(ctx context.Context, slug string) (*Project, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+projectCols+" FROM projects WHERE slug = ?", slug)
	p, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// UpsertProject implements Store.
func (s *SQLite) UpsertProject(ctx context.Context, p Project) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects (`+projectCols+`) VALUES (
		?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(slug) DO UPDATE SET
			name=excluded.name, tagline=excluded.tagline, summary=excluded.summary,
			description=excluded.description, homepage=excluded.homepage,
			language=excluded.language, topics=excluded.topics, stars=excluded.stars,
			forks=excluded.forks, is_fork=excluded.is_fork,
			parent_full_name=excluded.parent_full_name, fingerprint=excluded.fingerprint,
			stack=excluded.stack, features=excluded.features, links=excluded.links,
			year=excluded.year, accent=excluded.accent, mono=excluded.mono,
			repo_url=excluded.repo_url, source=excluded.source,
			created_at=excluded.created_at, pushed_at=excluded.pushed_at`,
		projectArgs(p)...)
	return err
}

// RenameProjectSlug implements Store.
func (s *SQLite) RenameProjectSlug(ctx context.Context, oldSlug, newSlug string) error {
	// No-op unless the old slug exists and the new one is free.
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE slug=?`, oldSlug).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return nil
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE slug=?`, newSlug).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil // new slug already taken — leave both rows alone
	}
	_, err := s.db.ExecContext(ctx, `UPDATE projects SET slug=? WHERE slug=?`, newSlug, oldSlug)
	return err
}

// SetProjectFeatured implements Store.
func (s *SQLite) SetProjectFeatured(ctx context.Context, slug string, featured bool) error {
	return s.setProjectFlag(ctx, "featured", slug, featured)
}

// SetProjectVisible implements Store.
func (s *SQLite) SetProjectVisible(ctx context.Context, slug string, visible bool) error {
	return s.setProjectFlag(ctx, "visible", slug, visible)
}

// SetProjectClassification implements Store.
func (s *SQLite) SetProjectClassification(ctx context.Context, slug string, c Classification) error {
	_, err := s.db.ExecContext(ctx, "UPDATE projects SET classification = ? WHERE slug = ?", string(c), slug)
	return err
}

func (s *SQLite) setProjectFlag(ctx context.Context, col, slug string, on bool) error {
	v := 0
	if on {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, "UPDATE projects SET "+col+" = ? WHERE slug = ?", v, slug)
	return err
}

// DeleteProject implements Store.
func (s *SQLite) DeleteProject(ctx context.Context, slug string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM projects WHERE slug = ?", slug)
	return err
}

// ---------------------------------------------------------------------------
// Posts

func postFromRow(row interface{ Scan(...any) error }) (*Post, error) {
	var (
		p         Post
		tags      string
		published int
		pub, upd  string
	)
	if err := row.Scan(&p.ID, &p.Slug, &p.Title, &p.Summary, &p.Body, &tags,
		&published, &pub, &upd); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tags), &p.Tags)
	p.Published = published != 0
	p.PublishedAt, _ = time.Parse(time.RFC3339, pub)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, upd)
	return &p, nil
}

const postCols = "id, slug, title, summary, body, tags, published, published_at, updated_at"

// ListPosts implements Store.
func (s *SQLite) ListPosts(ctx context.Context, includeDrafts bool) ([]Post, error) {
	q := "SELECT " + postCols + " FROM posts"
	if !includeDrafts {
		q += " WHERE published = 1"
	}
	q += " ORDER BY published_at DESC"
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		p, err := postFromRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetPost implements Store.
func (s *SQLite) GetPost(ctx context.Context, slug string) (*Post, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+postCols+" FROM posts WHERE slug = ?", slug)
	p, err := postFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// GetPostByID implements Store.
func (s *SQLite) GetPostByID(ctx context.Context, id int64) (*Post, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+postCols+" FROM posts WHERE id = ?", id)
	p, err := postFromRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// CreatePost implements Store.
func (s *SQLite) CreatePost(ctx context.Context, p Post) (int64, error) {
	tags, _ := json.Marshal(p.Tags)
	pub := p.PublishedAt.UTC().Format(time.RFC3339)
	if p.PublishedAt.IsZero() {
		pub = time.Now().UTC().Format(time.RFC3339)
	}
	upd := time.Now().UTC().Format(time.RFC3339)
	published := 0
	if p.Published {
		published = 1
	}
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO posts (slug, title, summary, body, tags, published, published_at, updated_at) VALUES (?,?,?,?,?,?,?,?)",
		p.Slug, p.Title, p.Summary, p.Body, string(tags), published, pub, upd)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdatePost implements Store.
func (s *SQLite) UpdatePost(ctx context.Context, p Post) error {
	tags, _ := json.Marshal(p.Tags)
	pub := p.PublishedAt.UTC().Format(time.RFC3339)
	if pub == "" || pub == "0001-01-01T00:00:00Z" {
		pub = time.Now().UTC().Format(time.RFC3339)
	}
	upd := time.Now().UTC().Format(time.RFC3339)
	published := 0
	if p.Published {
		published = 1
	}
	_, err := s.db.ExecContext(ctx,
		"UPDATE posts SET slug=?, title=?, summary=?, body=?, tags=?, published=?, published_at=?, updated_at=? WHERE id=?",
		p.Slug, p.Title, p.Summary, p.Body, string(tags), published, pub, upd, p.ID)
	return err
}

// DeletePost implements Store.
func (s *SQLite) DeletePost(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM posts WHERE id = ?", id)
	return err
}

// SearchPosts implements Store using SQLite FTS5.
func (s *SQLite) SearchPosts(ctx context.Context, query string) ([]Post, error) {
	// Full-text match over slug, title, summary and body.
	q := "SELECT " + postCols + " FROM posts WHERE published = 1 AND id IN (SELECT rowid FROM posts_fts WHERE posts_fts MATCH ?) ORDER BY published_at DESC"
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		// Unbalanced quotes/parens make FTS5 throw a syntax error — fall
		// back to a plain LIKE scan so search never 500s.
		return searchPostsLike(ctx, s.db, query)
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		p, err := postFromRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Also match posts whose tags contain the query (tags are JSON, so
	// they are not part of the FTS index).
	if len(out) == 0 {
		return searchPostsLike(ctx, s.db, query)
	}
	return out, nil
}

// searchPostsLike scans slug/title/summary/body and the tag list with LIKE.
func searchPostsLike(ctx context.Context, db *sql.DB, query string) ([]Post, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+postCols+" FROM posts WHERE published = 1 AND (lower(title) LIKE ? OR lower(summary) LIKE ? OR lower(body) LIKE ? OR lower(tags) LIKE ?) ORDER BY published_at DESC",
		"%"+strings.ToLower(query)+"%", "%"+strings.ToLower(query)+"%", "%"+strings.ToLower(query)+"%", "%"+strings.ToLower(query)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		p, err := postFromRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// Messages

// CreateMessage implements Store.
func (s *SQLite) CreateMessage(ctx context.Context, m Message) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO messages (name, email, body, created_at, read) VALUES (?,?,?,?,0)",
		m.Name, m.Email, m.Body, m.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

// ListMessages implements Store.
func (s *SQLite) ListMessages(ctx context.Context) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, email, body, created_at, read FROM messages ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		var read int
		if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.Body, &created, &read); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		m.Read = read != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteMessage implements Store.
func (s *SQLite) DeleteMessage(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM messages WHERE id = ?", id)
	return err
}

// ---------------------------------------------------------------------------
// Analytics

// RecordPageView implements Store. day must be YYYY-MM-DD (UTC).
func (s *SQLite) RecordPageView(ctx context.Context, day, path string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO page_views (date, path, count) VALUES (?,?,1) ON CONFLICT(date, path) DO UPDATE SET count = count + 1",
		day, path)
	return err
}

// PageViewTotal implements Store.
func (s *SQLite) PageViewTotal(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(count),0) FROM page_views").Scan(&n)
	return n, err
}

// TopPages implements Store.
func (s *SQLite) TopPages(ctx context.Context, limit int) ([]PageViewStat, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT path, SUM(count) AS total FROM page_views GROUP BY path ORDER BY total DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PageViewStat
	for rows.Next() {
		var v PageViewStat
		if err := rows.Scan(&v.Path, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DailyViews implements Store.
func (s *SQLite) DailyViews(ctx context.Context, days int) ([]DailyStat, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT date, SUM(count) AS total FROM page_views WHERE date >= date('now', ?) GROUP BY date ORDER BY date ASC",
		fmt.Sprintf("-%d days", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyStat
	for rows.Next() {
		var v DailyStat
		if err := rows.Scan(&v.Date, &v.Count); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PageViewsFor implements Store.
func (s *SQLite) PageViewsFor(ctx context.Context, path string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(SUM(count),0) FROM page_views WHERE path = ?", path).Scan(&n)
	return n, err
}

// ---------------------------------------------------------------------------
// Sessions

// CreateSession implements Store.
func (s *SQLite) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO sessions (token, created_at, expires_at) VALUES (?,?,?)",
		sess.Token, sess.CreatedAt.UTC().Format(time.RFC3339), sess.ExpiresAt.UTC().Format(time.RFC3339))
	return err
}

// SessionValid implements Store.
func (s *SQLite) SessionValid(ctx context.Context, token string) (bool, error) {
	var expires string
	err := s.db.QueryRowContext(ctx, "SELECT expires_at FROM sessions WHERE token = ?", token).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return false, nil
	}
	if time.Now().After(exp) {
		_ = s.DeleteSession(ctx, token)
		return false, nil
	}
	return true, nil
}

// DeleteSession implements Store.
func (s *SQLite) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
	return err
}

// ---------------------------------------------------------------------------
// Settings

// GetSetting implements Store.
func (s *SQLite) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetSetting implements Store.
func (s *SQLite) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, value)
	return err
}
