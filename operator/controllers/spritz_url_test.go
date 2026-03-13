package controllers

import (
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestWorkspaceURLIngressAddsTrailingSlash(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/workspaces/w/tidy-fjord",
	}

	got := spritzv1.WorkspaceURLForSpritz(spritz)
	want := "https://console.example.com/workspaces/w/tidy-fjord/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestWorkspaceURLIngressRootStaysRoot(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/",
	}

	got := spritzv1.WorkspaceURLForSpritz(spritz)
	want := "https://console.example.com/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestWorkspaceURLIngressKeepsExistingTrailingSlash(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/workspaces/w/tidy-fjord/",
	}

	got := spritzv1.WorkspaceURLForSpritz(spritz)
	want := "https://console.example.com/workspaces/w/tidy-fjord/"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSpritzURLPrefersChatURLWhenWorkspaceIsWebAccessible(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Name = "tidy-fjord"
	spritz.Spec.Ingress = &spritzv1.SpritzIngress{
		Host: "console.example.com",
		Path: "/workspaces/w/tidy-fjord",
	}

	got := spritzURL(spritz)
	want := "https://console.example.com/#chat/tidy-fjord"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
