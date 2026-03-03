package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestParseCSV(t *testing.T) {
	if parseCSV("") != nil {
		t.Fatal("expected nil for empty CSV")
	}
	got := parseCSV("/home/dev, /home/spritz")
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(got))
	}
	if got[0] != "/home/dev" || got[1] != "/home/spritz" {
		t.Fatalf("unexpected values: %v", got)
	}
}

func TestParseNodeSelector(t *testing.T) {
	selector, err := parseNodeSelector("spritz.sh/storage-ready=true,zone=fsn1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if selector["spritz.sh/storage-ready"] != "true" || selector["zone"] != "fsn1" {
		t.Fatalf("unexpected selector: %v", selector)
	}

	if _, err := parseNodeSelector("missingequals"); err == nil {
		t.Fatal("expected error for invalid selector entry")
	}
	if _, err := parseNodeSelector("=novalue"); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := parseNodeSelector("key="); err == nil {
		t.Fatal("expected error for empty value")
	}
}

func TestBuildHomeMountsDefault(t *testing.T) {
	mounts := buildHomeMounts()
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].Name != "home" {
		t.Fatalf("unexpected mount name: %s", mounts[0].Name)
	}
	if mounts[0].MountPath != repoInitHomeDir {
		t.Fatalf("unexpected mount path: %s", mounts[0].MountPath)
	}
}

func TestBuildPodSecurityContext(t *testing.T) {
	if ctx := buildPodSecurityContext(false, false); ctx != nil {
		t.Fatal("expected nil security context when no shared mounts or repo init")
	}

	ctx := buildPodSecurityContext(true, false)
	if ctx == nil || ctx.FSGroup == nil || *ctx.FSGroup != repoInitGroupID {
		t.Fatalf("expected fsGroup %d when shared mounts enabled, got %+v", repoInitGroupID, ctx)
	}

	ctx = buildPodSecurityContext(false, true)
	if ctx == nil || ctx.FSGroup == nil || *ctx.FSGroup != repoInitGroupID {
		t.Fatalf("expected fsGroup %d when repo init present, got %+v", repoInitGroupID, ctx)
	}
}

func TestBuildRepoInitContainerDedupesHomeMount(t *testing.T) {
	spritz := &spritzv1.Spritz{
		Spec: spritzv1.SpritzSpec{
			Repo: &spritzv1.SpritzRepo{
				URL: "https://github.com/example/repo.git",
			},
		},
	}

	homeMounts := buildHomeMounts()
	repos := repoEntries(spritz)
	containers, _, err := buildRepoInitContainers(spritz, repos, homeMounts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(containers) == 0 {
		t.Fatal("expected repo init container")
	}

	count := 0
	for _, mount := range containers[0].VolumeMounts {
		if mount.MountPath == repoInitHomeDir {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected single %s mount, got %d", repoInitHomeDir, count)
	}
}

func TestRepoDirNeedsWorkspaceMountHonorsSharedMounts(t *testing.T) {
	mountRoots := []corev1.VolumeMount{
		{Name: "shared", MountPath: "/shared"},
	}
	if repoDirNeedsWorkspaceMount("/shared/repo", mountRoots) {
		t.Fatal("expected repo dir under shared mount to skip workspace mount")
	}
	if repoDirNeedsWorkspaceMount("/workspace/repo", mountRoots) {
		t.Fatal("expected repo dir under /workspace to skip workspace mount")
	}
}

func TestValidateRepoDir(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"empty ok", "", false},
		{"relative ok", "spritz", false},
		{"relative nested ok", "project/app", false},
		{"relative up invalid", "../etc", true},
		{"relative up nested invalid", "foo/../../etc", true},
		{"absolute workspace ok", "/workspace/spritz", false},
		{"absolute workspace nested ok", "/workspace/spritz/app", false},
		{"absolute escape invalid", "/etc", true},
		{"absolute escape via traversal invalid", "/workspace/../etc", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRepoDir(tc.dir)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for %s", tc.dir)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.dir, err)
			}
		})
	}
}
