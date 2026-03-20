package controllers

import (
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestInstanceURLIngressAddsTrailingSlash(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/i/tidy-fjord",
	}

	got := spritzv1.InstanceURLForSpritz(spritz)
	want := "https://console.example.com/i/tidy-fjord/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestInstanceURLIngressRootStaysRoot(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/",
	}

	got := spritzv1.InstanceURLForSpritz(spritz)
	want := "https://console.example.com/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestInstanceURLIngressKeepsExistingTrailingSlash(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/i/tidy-fjord/",
	}

	got := spritzv1.InstanceURLForSpritz(spritz)
	want := "https://console.example.com/i/tidy-fjord/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSpritzURLPrefersChatURLWhenInstanceIsWebAccessible(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Name = "tidy-fjord"
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/i/tidy-fjord",
	}

	got := spritzURL(spritz)
	want := "https://console.example.com/c/tidy-fjord"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
