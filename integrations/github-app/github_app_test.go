package main

import "testing"

func TestRepoNameFromPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want string
	}{
		{"owner repo", "owner/repo", "repo"},
		{"single segment", "repo", "repo"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoNameFromPath(tc.path); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
