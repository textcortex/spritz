package controllers

import (
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestRepoDirForSingleRepoInfersName(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"https git", "https://github.com/textcortex/spritz.git", "/workspace/spritz"},
		{"https no suffix", "https://github.com/textcortex/spritz", "/workspace/spritz"},
		{"ssh scp", "git@github.com:textcortex/spritz.git", "/workspace/spritz"},
		{"ssh url", "ssh://git@github.com/textcortex/spritz.git", "/workspace/spritz"},
		{"trailing slash", "https://github.com/textcortex/spritz.git/", "/workspace/spritz"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := spritzv1.SpritzRepo{URL: tc.url}
			got := repoDirFor(repo, 0, 1)
			if got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestRepoDirForFallsBackWhenURLMissing(t *testing.T) {
	repo := spritzv1.SpritzRepo{URL: ""}
	got := repoDirFor(repo, 0, 1)
	if got != "/workspace/repo" {
		t.Fatalf("expected /workspace/repo, got %s", got)
	}
}

func TestRepoDirForMultipleReposUsesIndex(t *testing.T) {
	repo := spritzv1.SpritzRepo{URL: "https://github.com/textcortex/spritz.git"}
	got := repoDirFor(repo, 1, 2)
	if got != "/workspace/repo-2" {
		t.Fatalf("expected /workspace/repo-2, got %s", got)
	}
}

func TestRepoDirForRespectsExplicitDir(t *testing.T) {
	repo := spritzv1.SpritzRepo{URL: "https://github.com/textcortex/spritz.git", Dir: "spritz"}
	got := repoDirFor(repo, 0, 1)
	if got != "/workspace/spritz" {
		t.Fatalf("expected /workspace/spritz, got %s", got)
	}
}
