package main

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestReserveIdempotentCreateNameFailsWhenReservationDisappearsAfterCreateConflict(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	state := provisionerIdempotencyState{
		canonicalFingerprint: "fingerprint-1",
		resolvedPayload:      `{"spec":{"image":"example.com/spritz-openclaw:latest"}}`,
	}
	s.client = &createInterceptClient{
		Client: s.client,
		onCreate: func(_ context.Context, obj client.Object) error {
			configMap, ok := obj.(*corev1.ConfigMap)
			if !ok {
				return nil
			}
			if configMap.Name != idempotencyReservationName("zenobot", "discord-race") {
				return nil
			}
			return apierrors.NewAlreadyExists(schema.GroupResource{Group: "", Resource: "configmaps"}, configMap.Name)
		},
	}

	_, _, _, err := s.reserveIdempotentCreateName(context.Background(), "spritz-test", principal{ID: "zenobot", Type: principalTypeService}, "discord-race", "openclaw-tidal-wind", state)
	if err == nil {
		t.Fatal("expected missing reservation error")
	}
	if !apierrors.IsNotFound(err) {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestSetIdempotencyReservationNameFallsBackWhenReservationMissing(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	state := provisionerIdempotencyState{
		canonicalFingerprint: "fingerprint-1",
		resolvedPayload:      `{"spec":{"image":"example.com/spritz-openclaw:latest"}}`,
	}

	name, completed, payload, err := s.setIdempotencyReservationName(
		context.Background(),
		"zenobot",
		"discord-race",
		"openclaw-old-name",
		"openclaw-new-name",
		state,
	)
	if err != nil {
		t.Fatalf("setIdempotencyReservationName returned error: %v", err)
	}
	if completed {
		t.Fatal("expected missing reservation fallback to stay pending")
	}
	if name != "openclaw-new-name" {
		t.Fatalf("expected proposed name fallback, got %q", name)
	}
	if payload != state.resolvedPayload {
		t.Fatalf("expected resolved payload fallback, got %q", payload)
	}
}
