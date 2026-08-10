package importer

import (
	"context"
	"log"

	"github.com/ashu-choudhury/portfolio/store"
)

// SyncOptions controls how much detail the importer fetches per repo.
type SyncOptions struct {
	// Owner is the GitHub username to import. Empty means DefaultOwner.
	Owner string
	// Fingerprints fetches root file listings to infer the stack (1 extra
	// API call per repo). Disable to save rate limit.
	Fingerprints bool
	// Readmes fetches README content to generate taglines (1 extra call
	// per repo).
	Readmes bool
}

// Sync pulls every repository for the owner, classifies it, infers a
// fingerprint, and upserts it into the store. Existing admin overrides
// (visible/featured/classification) are preserved by the store's upsert.
//
// Repos the owner does not own are imported as hidden clones so the admin
// panel can review and override them.
func Sync(ctx context.Context, s store.Store, opts SyncOptions) error {
	owner := opts.Owner
	if owner == "" {
		owner = defaultOwner
	}
	c := NewClient(owner)
	repos, err := c.ListRepos(ctx)
	if err != nil {
		return err
	}
	log.Printf("importer: found %d repositories for %s", len(repos), owner)
	for _, r := range repos {
		p := c.toProject(r)

		// Authorship check for forks: the most recent commit decides.
		if r.Fork {
			recent, err := c.RecentCommits(ctx, r.Name, 1)
			if err != nil && !IsNotFound(err) {
				log.Printf("importer: commits for %s: %v", r.Name, err)
			}
			cls := ClassifyRepo(r, recent, owner)
			p.Classification = cls.Class
			p.Visible = cls.Class != store.ClassificationClone
		}

		// Fingerprint from root file signals.
		if opts.Fingerprints {
			if files, err := c.RootFiles(ctx, r.Name); err == nil {
				p.Fingerprint = Fingerprint(files, r.Language)
			} else if !IsNotFound(err) {
				log.Printf("importer: files for %s: %v", r.Name, err)
			}
		}

		// A short tagline from the README when GitHub gives no description.
		if opts.Readmes && p.Summary == "" {
			if md, err := c.Readme(ctx, r.Name); err == nil {
				p.Summary = readmeTagline(md)
			}
		}

		if err := s.UpsertProject(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// readmeTagline derives a one-line tagline from the README's first heading.
func readmeTagline(md string) string {
	for _, line := range splitLines(md) {
		line = trim(line)
		if line == "" {
			continue
		}
		// "# Title" or "## Title" — skip headings that look like the repo
		// name itself; prefer the first paragraph.
		if isHeading(line) {
			continue
		}
		if len(line) > 200 {
			line = line[:200] + "…"
		}
		return line
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r'
}

func isHeading(line string) bool {
	if len(line) == 0 {
		return false
	}
	i := 0
	for i < len(line) && i < 6 && line[i] == '#' {
		i++
	}
	return i > 0
}
