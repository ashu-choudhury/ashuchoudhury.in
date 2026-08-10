// Command import-github syncs the owner's GitHub repositories into the
// site's SQLite database, classifying each repo (original/rewritten/clone)
// and inferring project fingerprints from file signals.
//
// Usage:
//
//	GITHUB_TOKEN=<personal-access-token> go run ./cmd/import-github -user ashu-choudhury -db storage/persisted/portfolio.db
//
// The site itself is seeded with a curated catalogue, so this command is
// only needed to refresh projects from GitHub (new repos, updated stars,
// recency ordering). Admin overrides are preserved.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/ashu-choudhury/portfolio/importer"
	"github.com/ashu-choudhury/portfolio/store"
)

func main() {
	user := flag.String("user", importer.DefaultOwner, "GitHub username to import")
	db := flag.String("db", "storage/persisted/portfolio.db", "path to the SQLite database (default matches the site server)")
	noFingerprints := flag.Bool("no-fingerprints", false, "skip fetching root file listings")
	noReadmes := flag.Bool("no-readmes", false, "skip fetching READMEs")
	flag.Parse()

	s, err := store.OpenSQLite(*db)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if err := importer.Sync(ctx, s, importer.SyncOptions{
		Owner:        *user,
		Fingerprints: !*noFingerprints,
		Readmes:      !*noReadmes,
	}); err != nil {
		log.Fatalf("import: %v", err)
	}
	log.Printf("import complete — restart the site server to pick up changes")
}
