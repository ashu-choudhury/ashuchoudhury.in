package importer

import (
	"strings"

	"github.com/ashu-choudhury/portfolio/store"
)

// OwnerLogin is the default GitHub username used for authorship checks.
const OwnerLogin = "ashu-choudhury"

// Classification describes the owner's repo policy:
//
//	original  -> created by the owner (or a fork the owner fully rewrote)
//	clone     -> a fork/clone of someone else's work, hidden from the site
//
// The "rewritten" distinction is folded into original at import time: a
// fork whose most recent commits are authored by the owner is treated as
// the owner's work; everything else is a clone. The admin panel can always
// override the final classification.
type Classification struct {
	Class store.Classification
	// Reason explains the decision in the admin UI.
	Reason string
}

// ClassifyRepo decides whether a repository counts as the owner's work.
// A repo is the owner's when either:
//   - it is not a fork, or
//   - the most recent commit on the default branch is authored by the owner
//     (evidence the owner actively rewrote/extended the fork).
func ClassifyRepo(r Repo, recent []Commit, owner string) Classification {
	if owner == "" {
		owner = OwnerLogin
	}
	if !r.Fork {
		return Classification{Class: store.ClassificationOriginal, Reason: "not a fork"}
	}
	if r.ForkParent.FullName == "" {
		return Classification{Class: store.ClassificationOriginal, Reason: "fork without tracked parent"}
	}
	for _, c := range recent {
		if c.Author.Login == owner || c.Committer.Login == owner {
			return Classification{Class: store.ClassificationOriginal,
				Reason: "fork, but most recent commit authored by " + owner}
		}
	}
	return Classification{Class: store.ClassificationClone,
		Reason: "fork with no recent commits by " + owner}
}

// Fingerprint infers the project's technology stack from root-level file
// signals. Returns a short list of human-readable signals used to enrich
// project cards, e.g. ["Android", "Kotlin", "Rust", "Go", "Node.js"].
func Fingerprint(files []FileEntry, language string) []string {
	set := map[string]bool{}
	names := make(map[string]bool, len(files))
	for _, f := range files {
		names[f.Name] = true
	}

	// Build-system signals are the most reliable.
	switch {
	case names["build.gradle.kts"] || names["build.gradle"] || names["settings.gradle"]:
		set["Android / JVM"] = true
	case names["Cargo.toml"]:
		set["Rust"] = true
	case names["go.mod"]:
		set["Go"] = true
	case names["package.json"]:
		set["Node.js"] = true
	case names["pubspec.yaml"] || names["pubspec.lock"]:
		set["Dart / Flutter"] = true
	case names["pom.xml"] || names["build.xml"]:
		set["Java"] = true
	case names["requirements.txt"] || names["pyproject.toml"] || names["setup.py"]:
		set["Python"] = true
	case names["Cargo.lock"]:
		set["Rust"] = true
	}

	// Language field from GitHub as a cross-check.
	if language != "" {
		set[language] = true
	}

	// Secondary signals for richer fingerprints.
	if names["gradlew"] || names["gradlew.bat"] {
		set["Gradle"] = true
	}
	if names["CMakeLists.txt"] {
		set["CMake"] = true
	}
	if names["Dockerfile"] {
		set["Docker"] = true
	}
	if names["docker-compose.yml"] || names["docker-compose.yaml"] {
		set["Docker Compose"] = true
	}
	if names["Makefile"] {
		set["Make"] = true
	}
	for _, f := range files {
		lower := strings.ToLower(f.Name)
		if strings.HasSuffix(lower, ".dart") {
			set["Dart"] = true
		}
		if strings.HasSuffix(lower, ".kt") || strings.HasSuffix(lower, ".kts") {
			set["Kotlin"] = true
		}
		if strings.HasSuffix(lower, ".rs") {
			set["Rust"] = true
		}
		if strings.HasSuffix(lower, ".go") {
			set["Go"] = true
		}
		if strings.HasSuffix(lower, ".py") {
			set["Python"] = true
		}
		if strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") {
			set["TypeScript"] = true
		}
		if strings.HasSuffix(lower, ".js") {
			set["JavaScript"] = true
		}
	}

	// Deterministic order for stable diffs: preferred signals first,
	// then anything else alphabetically.
	order := []string{"Android / JVM", "Kotlin", "Go", "Rust", "Node.js", "TypeScript", "JavaScript", "Python", "Dart / Flutter", "Dart", "Java", "Gradle", "CMake", "Docker", "Docker Compose", "Make"}
	seen := map[string]bool{}
	result := make([]string, 0, len(set))
	for _, o := range order {
		if set[o] && !seen[o] {
			result = append(result, o)
			seen[o] = true
		}
	}
	for s := range set {
		if !seen[s] {
			result = append(result, s)
		}
	}
	return result
}
