package handlers

import "testing"

func TestParseRepoOwnerAndName(t *testing.T) {
	tests := []struct {
		repoURL   string
		slug      string
		wantOwner string
		wantRepo  string
	}{
		{
			repoURL:   "https://github.com/ashu-choudhury/portfolio",
			slug:      "portfolio",
			wantOwner: "ashu-choudhury",
			wantRepo:  "portfolio",
		},
		{
			repoURL:   "https://github.com/someuser/somerepo/",
			slug:      "somerepo",
			wantOwner: "someuser",
			wantRepo:  "somerepo",
		},
		{
			repoURL:   "",
			slug:      "my-project",
			wantOwner: "ashu-choudhury",
			wantRepo:  "my-project",
		},
	}

	for _, tt := range tests {
		owner, repo := parseRepoOwnerAndName(tt.repoURL, tt.slug)
		if owner != tt.wantOwner || repo != tt.wantRepo {
			t.Errorf("parseRepoOwnerAndName(%q, %q) = (%q, %q); want (%q, %q)",
				tt.repoURL, tt.slug, owner, repo, tt.wantOwner, tt.wantRepo)
		}
	}
}
