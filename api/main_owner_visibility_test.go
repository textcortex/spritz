package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func newListSpritzTestServer(t *testing.T, objects ...client.Object) *server {
	t.Helper()
	scheme := newTestSpritzScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme)
	if len(objects) > 0 {
		builder = builder.WithObjects(objects...)
	}
	return &server{
		client:    builder.Build(),
		scheme:    scheme,
		namespace: "spritz-test",
		auth: authConfig{
			mode:     authModeHeader,
			headerID: "X-Spritz-User-Id",
		},
		internalAuth: internalAuthConfig{enabled: false},
	}
}

func spritzForOwner(name, ownerID string, labels map[string]string) *spritzv1.Spritz {
	return &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "spritz-test",
			Labels:    labels,
		},
		Spec: spritzv1.SpritzSpec{
			Image: "example.com/spritz:latest",
			Owner: spritzv1.SpritzOwner{ID: ownerID},
		},
		Status: spritzv1.SpritzStatus{Phase: "Ready"},
	}
}

func TestListSpritzesUsesSpecOwnerWhenOwnerLabelMissing(t *testing.T) {
	ownedMissingLabel := spritzForOwner("tidy-otter", "user-1", nil)
	mislabelledOtherOwner := spritzForOwner("wrong-owner", "user-2", map[string]string{
		ownerLabelKey: ownerLabelValue("user-1"),
	})

	s := newListSpritzTestServer(t, ownedMissingLabel, mislabelledOtherOwner)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.GET("/api/spritzes", s.listSpritzes)

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Status string `json:"status"`
		Data   struct {
			Items []spritzv1.Spritz `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	if len(payload.Data.Items) != 1 {
		t.Fatalf("expected exactly one visible spritz, got %d", len(payload.Data.Items))
	}
	if payload.Data.Items[0].Name != "tidy-otter" {
		t.Fatalf("expected tidy-otter, got %q", payload.Data.Items[0].Name)
	}
}
