package controllers

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

func TestParseAccessModes(t *testing.T) {
	modes := parseAccessModes("rwo,ReadWriteMany,rox")
	if len(modes) != 3 {
		t.Fatalf("expected 3 access modes, got %d", len(modes))
	}
}

func TestBuildHomeMountsDefault(t *testing.T) {
	mounts := buildHomeMounts(homePVCSettings{})
	if len(mounts) != 1 {
		t.Fatalf("expected 1 mount, got %d", len(mounts))
	}
	if mounts[0].MountPath != "/home/dev" {
		t.Fatalf("unexpected mount path: %s", mounts[0].MountPath)
	}
}

func TestBuildHomeMountsDedup(t *testing.T) {
	settings := homePVCSettings{mountPaths: []string{"/home/dev", "/home/dev", "/home/spritz"}}
	mounts := buildHomeMounts(settings)
	if len(mounts) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(mounts))
	}
	paths := []string{mounts[0].MountPath, mounts[1].MountPath}
	if !containsPath(paths, "/home/dev") || !containsPath(paths, "/home/spritz") {
		t.Fatalf("unexpected mounts: %v", paths)
	}
}

func TestValidateMountPaths(t *testing.T) {
	if err := validateMountPaths([]string{"/home/dev", "/home/spritz"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := validateMountPaths([]string{"/home", "/home/spritz"}); err == nil {
		t.Fatal("expected overlap error")
	}
	if err := validateMountPaths([]string{"home"}); err == nil {
		t.Fatal("expected absolute path error")
	}
}

func TestLoadHomePVCSettingsDisabled(t *testing.T) {
	t.Setenv("SPRITZ_HOME_PVC_PREFIX", "")
	settings := loadHomePVCSettings()
	if settings.enabled {
		t.Fatal("expected home PVC disabled")
	}
	if len(settings.mountPaths) != 0 {
		t.Fatalf("expected no mount paths, got %v", settings.mountPaths)
	}
}

func TestOwnerPVCName(t *testing.T) {
	name := ownerPVCName("spritz-home", "user-123")
	if !strings.HasPrefix(name, "spritz-home-owner-") {
		t.Fatalf("unexpected pvc name: %s", name)
	}
}

func TestEnsureHomePVCAllowsPending(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add core scheme: %v", err)
	}
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add spritz scheme: %v", err)
	}

	now := metav1.NewTime(time.Now())
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "spritz-home-owner-test",
			Namespace:         "spritz",
			CreationTimestamp: now,
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimPending,
		},
	}

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pvc).Build()
	reconciler := &SpritzReconciler{Client: client}

	settings := homePVCSettings{
		accessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		size:        resource.MustParse("1Gi"),
	}
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{Name: "devbox1", Namespace: "spritz"},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-123"},
		},
	}

	if err := reconciler.ensureHomePVC(context.Background(), spritz, pvc.Name, settings); err != nil {
		t.Fatalf("expected pending PVC to be allowed, got error: %v", err)
	}
}

func TestBuildPodSecurityContext(t *testing.T) {
	if ctx := buildPodSecurityContext(false, false, false); ctx != nil {
		t.Fatal("expected nil security context when no repo init or home PVC")
	}

	ctx := buildPodSecurityContext(true, false, false)
	if ctx == nil || ctx.FSGroup == nil || *ctx.FSGroup != repoInitGroupID {
		t.Fatalf("expected fsGroup %d when home PVC enabled, got %+v", repoInitGroupID, ctx)
	}

	ctx = buildPodSecurityContext(false, false, true)
	if ctx == nil || ctx.FSGroup == nil || *ctx.FSGroup != repoInitGroupID {
		t.Fatalf("expected fsGroup %d when repo init present, got %+v", repoInitGroupID, ctx)
	}

	ctx = buildPodSecurityContext(false, true, false)
	if ctx == nil || ctx.FSGroup == nil || *ctx.FSGroup != repoInitGroupID {
		t.Fatalf("expected fsGroup %d when shared config PVC enabled, got %+v", repoInitGroupID, ctx)
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

	homeMounts := buildHomeMounts(homePVCSettings{})
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

func TestValidateRepoDir(t *testing.T) {
	cases := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"empty ok", "", false},
		{"relative ok", "spritz", false},
		{"relative nested ok", "spritz/app", false},
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

func containsPath(paths []string, value string) bool {
	for _, path := range paths {
		if path == value {
			return true
		}
	}
	return false
}
