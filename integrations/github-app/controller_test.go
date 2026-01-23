package main

import (
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseRepoURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		host    string
		path    string
		wantErr bool
	}{
		{"https", "https://github.com/org/repo.git", "github.com", "org/repo", false},
		{"https no scheme", "github.com/org/repo", "github.com", "org/repo", false},
		{"ssh", "git@github.com:org/repo.git", "", "", true},
		{"invalid ssh", "git@github.com", "", "", true},
		{"short path", "https://github.com/org", "github.com", "org", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			host, path, err := parseRepoURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if host != tc.host || path != tc.path {
				t.Fatalf("got host=%q path=%q", host, path)
			}
			if tc.name == "short path" {
				if err := validateRepoPath(path); err == nil {
					t.Fatalf("expected validation error for short repo path")
				}
			}
		})
	}
}

func TestValidateRepoPath(t *testing.T) {
	if err := validateRepoPath("owner/repo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateRepoPath("owner"); err == nil {
		t.Fatalf("expected error for short path")
	}
	if err := validateRepoPath("org"); err == nil {
		t.Fatalf("expected error for missing repo segment")
	}
	if err := validateRepoPath("owner/"); err == nil {
		t.Fatalf("expected error for empty repo")
	}
}

func TestRepoAuthSecretName(t *testing.T) {
	name := strings.Repeat("a", 80)
	secretName := repoAuthSecretName(name, "owner/repo")
	if len(secretName) > 63 {
		t.Fatalf("secret name too long: %d", len(secretName))
	}
	if strings.Contains(secretName, "/") {
		t.Fatalf("secret name contains invalid '/': %s", secretName)
	}
}

func TestBuildNetrc(t *testing.T) {
	out := buildNetrc("github.com", "token123")
	if !strings.Contains(out, "machine github.com") {
		t.Fatalf("missing machine entry")
	}
	if !strings.Contains(out, "login "+netrcLoginToken) {
		t.Fatalf("missing login entry")
	}
	if !strings.Contains(out, "password token123") {
		t.Fatalf("missing password entry")
	}
}

func TestTokenNeedsRefresh(t *testing.T) {
	now := time.Now()
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
		tokenExpiryAnnotation: now.Add(30 * time.Minute).Format(time.RFC3339),
	}}}
	refresh, requeue := tokenNeedsRefresh(secret, now, "owner/repo")
	if refresh {
		t.Fatalf("did not expect refresh")
	}
	if requeue <= 0 {
		t.Fatalf("expected requeue delay")
	}

	secret.Annotations[tokenExpiryAnnotation] = now.Add(5 * time.Minute).Format(time.RFC3339)
	refresh, _ = tokenNeedsRefresh(secret, now, "owner/repo")
	if !refresh {
		t.Fatalf("expected refresh when expiry is near")
	}

	secret.Annotations[tokenRepoAnnotation] = "owner/other"
	refresh, _ = tokenNeedsRefresh(secret, now, "owner/repo")
	if !refresh {
		t.Fatalf("expected refresh when repo changes")
	}
}

func TestShouldPatchRepoAuth(t *testing.T) {
	if shouldPatchRepoAuth("", false, true) {
		t.Fatal("expected no patch when secret missing")
	}
	if shouldPatchRepoAuth("existing", true, true) {
		t.Fatal("expected no patch when auth already set")
	}
	if shouldPatchRepoAuth("", true, false) {
		t.Fatal("expected no patch when secret not managed")
	}
	if !shouldPatchRepoAuth("", true, true) {
		t.Fatal("expected patch when auth missing and secret managed")
	}
}
