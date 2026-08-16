// Package importer syncs the owner's GitHub repositories into the store,
// classifying each repo (original / rewritten / clone) and inferring a
// project "fingerprint" from its file signals.
//
// The importer needs network access to api.github.com — run it on the
// machine where the site is built or deployed:
//
//	GITHUB_TOKEN=... go run ./cmd/import-github -db portfolio.db
//
// Without a token, the unauthenticated rate limit (60 req/h) applies.
package importer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ashu-choudhury/portfolio/store"
)

// DefaultOwner is the GitHub username imported when -user is not given.
const DefaultOwner = "ashu-choudhury"

const defaultOwner = DefaultOwner

// Client talks to the GitHub REST API.
type Client struct {
	hc    *http.Client
	owner string
	token string
}

// NewClient returns a GitHub API client for the given owner. It reads
// GITHUB_TOKEN from the environment if set.
func NewClient(owner string) *Client {
	if owner == "" {
		owner = defaultOwner
	}
	return &Client{
		hc:    &http.Client{Timeout: 20 * time.Second},
		owner: owner,
		token: os.Getenv("GITHUB_TOKEN"),
	}
}

// Repo mirrors the fields of the GitHub REST API repository object we use.
type Repo struct {
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Homepage    string `json:"homepage"`
	Language    string `json:"language"`
	Fork        bool   `json:"fork"`
	ForkParent  struct {
		FullName string `json:"full_name"`
		PushedAt string `json:"pushed_at"`
	} `json:"parent"`
	Topics    []string  `json:"topics"`
	Stars     int       `json:"stargazers_count"`
	Forks     int       `json:"forks_count"`
	HTMLURL   string    `json:"html_url"`
	CreatedAt time.Time `json:"created_at"`
	PushedAt  time.Time `json:"pushed_at"`
}

// Commit is a minimal commit object for authorship checks.
type Commit struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	Committer struct {
		Login string `json:"login"`
	} `json:"committer"`
}

// FileEntry is a root-level file/dir entry from the contents API.
type FileEntry struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ListRepos returns all public repositories for the owner, newest-first.
func (c *Client) ListRepos(ctx context.Context) ([]Repo, error) {
	var all []Repo
	for page := 1; page <= 5; page++ {
		var batch []Repo
		url := fmt.Sprintf("https://api.github.com/users/%s/repos?per_page=100&page=%d&sort=pushed", c.owner, page)
		if err := c.get(ctx, url, &batch); err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

// RecentCommits returns the most recent commits on the default branch.
func (c *Client) RecentCommits(ctx context.Context, repo string, n int) ([]Commit, error) {
	var out []Commit
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/commits?per_page=%d", c.owner, repo, n)
	if err := c.get(ctx, url, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RootFiles returns the root-level entries of the default branch.
func (c *Client) RootFiles(ctx context.Context, repo string) ([]FileEntry, error) {
	var out []FileEntry
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/", c.owner, repo)
	if err := c.get(ctx, url, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Readme returns the decoded README text (may be empty).
func (c *Client) Readme(ctx context.Context, repo string) (string, error) {
	var raw struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/readme", c.owner, repo)
	if err := c.get(ctx, url, &raw); err != nil {
		return "", err
	}
	if raw.Encoding == "base64" {
		b, err := decodeBase64(raw.Content)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return raw.Content, nil
}

// get performs a GET request and decodes the JSON response. 404s return a
// non-nil error with IsNotFound semantics.
func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("github %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound{url: url}
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		// Rate-limited? Surface a friendly hint.
		if resp.StatusCode == http.StatusForbidden && strings.Contains(string(body), "rate limit") {
			return fmt.Errorf("GitHub API rate limit exceeded — set GITHUB_TOKEN")
		}
		return fmt.Errorf("github %s: %s: %s", url, resp.Status, strings.TrimSpace(string(body)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// errNotFound distinguishes missing resources from transport errors.
type errNotFound struct{ url string }

func (e errNotFound) Error() string { return "github 404: " + e.url }

// IsNotFound reports whether err came from a 404 response.
func IsNotFound(err error) bool {
	_, ok := err.(errNotFound)
	return ok
}

func decodeBase64(s string) ([]byte, error) {
	// GitHub inserts newlines into the base64 JSON field.
	s = strings.ReplaceAll(s, "\n", "")
	return base64.StdEncoding.DecodeString(s)
}

// normalizeSlug derives a canonical URL slug from a GitHub repo name:
// lowercase and underscore-free. GitHub treats repo names as
// case-insensitive, so ClipSync and clipsync (or jiosaavn_dart and
// jiosaavn-dart) are the same project and must share one URL.
func normalizeSlug(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", "-"))
}

// toProject converts a GitHub repo into a store.Project with defaults.
func (c *Client) toProject(r Repo) store.Project {
	return store.Project{
		Slug:           normalizeSlug(r.Name),
		Name:           prettyName(r.Name),
		Tagline:        firstLine(r.Description),
		Summary:        r.Description,
		Description:    r.Description,
		Homepage:       r.Homepage,
		Language:       r.Language,
		Topics:         r.Topics,
		Stars:          r.Stars,
		Forks:          r.Forks,
		IsFork:         r.Fork,
		ParentFullName: r.ForkParent.FullName,
		Classification: store.ClassificationOriginal,
		Visible:        !r.Fork,
		Featured:       false,
		RepoURL:        r.HTMLURL,
		Source:         "github",
		CreatedAt:      r.CreatedAt.UTC().Format("2006-01-02"),
		PushedAt:       r.PushedAt.UTC().Format("2006-01-02"),
	}
}

// prettyName converts a repo name into a display name.
func prettyName(slug string) string {
	s := strings.ReplaceAll(slug, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	words := strings.Fields(s)
	for i, w := range words {
		if w == "go" || w == "dart" || w == "ai" || w == "api" {
			words[i] = strings.ToUpper(w)
		} else {
			words[i] = strings.Title(w)
		}
	}
	return strings.Join(words, " ")
}

func firstLine(s string) string {
	if i := strings.Index(s, "\n"); i >= 0 {
		return s[:i]
	}
	return s
}
