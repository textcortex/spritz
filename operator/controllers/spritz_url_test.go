package controllers

import (
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestSpritzURLIngressAddsTrailingSlash(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/spritz/w/tidy-fjord",
	}

	got := spritzURL(spritz)
	want := "https://console.example.com/spritz/w/tidy-fjord/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSpritzURLIngressRootStaysRoot(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/",
	}

	got := spritzURL(spritz)
	want := "https://console.example.com/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSpritzURLIngressKeepsExistingTrailingSlash(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/spritz/w/tidy-fjord/",
	}

	got := spritzURL(spritz)
	want := "https://console.example.com/spritz/w/tidy-fjord/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
