package main

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newTestSpritzScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register spritz scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to register core scheme: %v", err)
	}
	return scheme
}

func newCreateSpritzTestServer(t *testing.T) *server {
	t.Helper()
	scheme := newTestSpritzScheme(t)
	return &server{
		client: fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&spritzv1.Spritz{}).
			Build(),
		scheme:           scheme,
		namespace:        "spritz-test",
		controlNamespace: "spritz-test",
		auth: authConfig{
			mode:              authModeHeader,
			headerID:          "X-Spritz-User-Id",
			headerEmail:       "X-Spritz-User-Email",
			headerType:        "X-Spritz-Principal-Type",
			headerScopes:      "X-Spritz-Principal-Scopes",
			headerDefaultType: principalTypeHuman,
		},
		internalAuth:     internalAuthConfig{enabled: false},
		userConfigPolicy: userConfigPolicy{},
	}
}

type createInterceptClient struct {
	client.Client
	onCreate func(context.Context, client.Object) error
	onUpdate func(context.Context, client.Object) error
}

func (c *createInterceptClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if c.onCreate != nil {
		if err := c.onCreate(ctx, obj); err != nil {
			return err
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *createInterceptClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.onUpdate != nil {
		if err := c.onUpdate(ctx, obj); err != nil {
			return err
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

func configureProvisionerTestServer(s *server) {
	s.auth.headerTrustTypeAndScopes = true
	s.presets = presetCatalog{
		byID: []runtimePreset{{
			ID:         "openclaw",
			Name:       "OpenClaw",
			Image:      "example.com/spritz-openclaw:latest",
			NamePrefix: "openclaw",
		}},
	}
	s.provisioners = provisionerPolicy{
		allowedPresetIDs: map[string]struct{}{"openclaw": {}},
		defaultIdleTTL:   24 * time.Hour,
		maxIdleTTL:       24 * time.Hour,
		defaultTTL:       168 * time.Hour,
		maxTTL:           168 * time.Hour,
		rateWindow:       time.Hour,
	}
}
