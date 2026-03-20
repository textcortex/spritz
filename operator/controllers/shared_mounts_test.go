package controllers

import (
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
	"spritz.sh/operator/sharedmounts"
)

func TestLoadSharedMountsSettingsAddsRoutePrefixToAPIURL(t *testing.T) {
	t.Setenv("SPRITZ_SHARED_MOUNTS", `[{"name":"config","mountPath":"/home/dev/.config","scope":"owner","mode":"snapshot","syncMode":"poll","pollSeconds":30,"publishSeconds":60}]`)
	t.Setenv("SPRITZ_SHARED_MOUNTS_API_URL", "http://spritz-api.svc.cluster.local:8080")
	t.Setenv("SPRITZ_SHARED_MOUNTS_TOKEN_SECRET_NAME", "spritz-shared-mounts-internal-token")
	t.Setenv("SPRITZ_SHARED_MOUNTS_SYNCER_IMAGE", "spritz-api:latest")
	t.Setenv("SPRITZ_ROUTE_API_PATH_PREFIX", "/api")

	settings, err := loadSharedMountsSettings()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if settings.apiURL != "http://spritz-api.svc.cluster.local:8080/api" {
		t.Fatalf("expected route-aware shared mounts api url, got %q", settings.apiURL)
	}
}

func TestBuildSharedMountRuntimeSkipsConfiguredSharedMountsWhenNothingRequestsThem(t *testing.T) {
	spritz := &spritzv1.Spritz{
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "owner-1"},
		},
	}
	settings := sharedMountsSettings{
		enabled:         true,
		mounts:          nil,
		apiURL:          "http://spritz-api.svc.cluster.local:8080/api",
		tokenSecretName: "spritz-shared-mounts-internal-token",
		tokenSecretKey:  "token",
		syncerImage:     "spritz-api:latest",
	}

	runtime, err := buildSharedMountRuntime(spritz, settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runtime.volumes) != 0 {
		t.Fatalf("expected no shared mount volumes, got %d", len(runtime.volumes))
	}
	if len(runtime.volumeMounts) != 0 {
		t.Fatalf("expected no shared mount mounts, got %d", len(runtime.volumeMounts))
	}
	if len(runtime.env) != 0 {
		t.Fatalf("expected no shared mount env vars, got %d", len(runtime.env))
	}
	if runtime.initContainer != nil {
		t.Fatal("expected no shared mount init container")
	}
	if runtime.sidecarContainer != nil {
		t.Fatal("expected no shared mount sidecar container")
	}
}

func TestBuildSharedMountRuntimeHonorsExplicitSharedMountRequest(t *testing.T) {
	spritz := &spritzv1.Spritz{
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "owner-1"},
			SharedMounts: []sharedmounts.MountSpec{
				{
					Name:           "config",
					MountPath:      "/home/dev/.config",
					Scope:          sharedmounts.ScopeOwner,
					Mode:           sharedmounts.ModeSnapshot,
					SyncMode:       sharedmounts.SyncPoll,
					PollSeconds:    30,
					PublishSeconds: 60,
				},
			},
		},
	}
	settings := sharedMountsSettings{
		enabled:         true,
		mounts:          nil,
		apiURL:          "http://spritz-api.svc.cluster.local:8080/api",
		tokenSecretName: "spritz-shared-mounts-internal-token",
		tokenSecretKey:  "token",
		syncerImage:     "spritz-api:latest",
	}

	runtime, err := buildSharedMountRuntime(spritz, settings)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runtime.volumes) != 1 {
		t.Fatalf("expected 1 shared mount volume, got %d", len(runtime.volumes))
	}
	if runtime.initContainer == nil || runtime.sidecarContainer == nil {
		t.Fatal("expected explicit shared mount request to wire shared mount sync containers")
	}
}
