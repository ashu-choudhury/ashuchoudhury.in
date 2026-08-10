package importer

import (
	"testing"

	"github.com/ashu-choudhury/portfolio/store"
)

func TestClassifyRepo(t *testing.T) {
	tests := []struct {
		name    string
		repo    Repo
		commits []Commit
		want    store.Classification
	}{
		{
			name: "original repo is the owner's work",
			repo: Repo{Fork: false},
			want: store.ClassificationOriginal,
		},
		{
			name: "fork with owner's recent commit counts as work",
			repo: Repo{Fork: true, ForkParent: struct {
				FullName string `json:"full_name"`
				PushedAt string `json:"pushed_at"`
			}{FullName: "someone/upstream"}},
			commits: []Commit{{Author: struct {
				Login string `json:"login"`
			}{Login: "ashu-choudhury"}}},
			want: store.ClassificationOriginal,
		},
		{
			name: "fork without owner commits is a clone",
			repo: Repo{Fork: true, ForkParent: struct {
				FullName string `json:"full_name"`
				PushedAt string `json:"pushed_at"`
			}{FullName: "someone/upstream"}},
			commits: []Commit{{Author: struct {
				Login string `json:"login"`
			}{Login: "upstream-author"}}},
			want: store.ClassificationClone,
		},
		{
			name: "fork with no tracked parent defaults to owner work",
			repo: Repo{Fork: true},
			want: store.ClassificationOriginal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyRepo(tt.repo, tt.commits, OwnerLogin)
			if got.Class != tt.want {
				t.Fatalf("ClassifyRepo = %s, want %s (%s)", got.Class, tt.want, got.Reason)
			}
		})
	}
}

func TestClassifyRepoCustomOwner(t *testing.T) {
	repo := Repo{Fork: true, ForkParent: struct {
		FullName string `json:"full_name"`
		PushedAt string `json:"pushed_at"`
	}{FullName: "someone/upstream"}}
	commits := []Commit{{Author: struct {
		Login string `json:"login"`
	}{Login: "my-other-login"}}}
	// With a custom owner, their recent commit counts as work.
	if got := ClassifyRepo(repo, commits, "my-other-login"); got.Class != store.ClassificationOriginal {
		t.Fatalf("custom owner: got %s, want original", got.Class)
	}
	// Without that owner in the commits, it is a clone.
	if got := ClassifyRepo(repo, commits, OwnerLogin); got.Class != store.ClassificationClone {
		t.Fatalf("other owner: got %s, want clone", got.Class)
	}
}

func TestFingerprint(t *testing.T) {
	tests := []struct {
		name     string
		files    []FileEntry
		language string
		want     []string
	}{
		{
			name:     "android project",
			files:    []FileEntry{{Name: "build.gradle.kts", Type: "file"}, {Name: "settings.gradle.kts", Type: "file"}, {Name: "gradlew", Type: "file"}, {Name: "app", Type: "dir"}},
			language: "Kotlin",
			want:     []string{"Android / JVM", "Kotlin", "Gradle"},
		},
		{
			name:     "rust project",
			files:    []FileEntry{{Name: "Cargo.toml", Type: "file"}, {Name: "src", Type: "dir"}},
			language: "Rust",
			want:     []string{"Rust"},
		},
		{
			name:     "go project",
			files:    []FileEntry{{Name: "go.mod", Type: "file"}, {Name: "main.go", Type: "file"}},
			language: "Go",
			want:     []string{"Go"},
		},
		{
			name:     "node project",
			files:    []FileEntry{{Name: "package.json", Type: "file"}, {Name: "index.ts", Type: "file"}},
			language: "TypeScript",
			want:     []string{"Node.js", "TypeScript"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Fingerprint(tt.files, tt.language)
			if len(got) != len(tt.want) {
				t.Fatalf("Fingerprint = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("Fingerprint = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestReadmeTagline(t *testing.T) {
	md := "# My Cool Repo\n\nThis is the first paragraph describing the project.\n\nMore content."
	got := readmeTagline(md)
	if got != "This is the first paragraph describing the project." {
		t.Fatalf("readmeTagline = %q", got)
	}
}
