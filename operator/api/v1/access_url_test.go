package v1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestInstanceURLForSpritzUsesIngressPath(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ingress: &SpritzIngress{
				Host: "console.example.com",
				Path: "/i/openclaw-tide-wind",
			},
		},
	}

	if got := InstanceURLForSpritz(spritz); got != "https://console.example.com/i/openclaw-tide-wind/" {
		t.Fatalf("expected instance url, got %q", got)
	}
}

func TestChatURLForSpritzUsesCanonicalPathRoute(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ingress: &SpritzIngress{
				Host: "console.example.com",
				Path: "/i/openclaw-tide-wind",
			},
		},
	}

	if got := ChatURLForSpritz(spritz); got != "https://console.example.com/c/openclaw-tide-wind" {
		t.Fatalf("expected chat url, got %q", got)
	}
}

func TestAccessURLForSpritzPromotesChatURL(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ingress: &SpritzIngress{
				Host: "console.example.com",
				Path: "/i/openclaw-tide-wind",
			},
		},
	}

	if got := AccessURLForSpritz(spritz); got != "https://console.example.com/c/openclaw-tide-wind" {
		t.Fatalf("expected access url to prefer chat url, got %q", got)
	}
}

func TestAccessURLForSpritzUsesCanonicalPathRouteWithoutIngress(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ports: []SpritzPort{{ContainerPort: 8080}},
		},
	}

	want := "http://openclaw-tide-wind.spritz-test.svc.cluster.local:8080/c/openclaw-tide-wind"
	if got := AccessURLForSpritz(spritz); got != want {
		t.Fatalf("expected access url %q, got %q", want, got)
	}
}

func TestAccessURLForSpritzUsesSharedHostRouteModelWithoutIngress(t *testing.T) {
	t.Setenv("SPRITZ_ROUTE_MODEL_TYPE", SharedHostRouteModelType)
	t.Setenv("SPRITZ_ROUTE_HOST", "console.example.com")
	t.Setenv("SPRITZ_ROUTE_INSTANCE_PATH_PREFIX", "/i")
	t.Setenv("SPRITZ_ROUTE_CHAT_PATH_PREFIX", "/c")

	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ports: []SpritzPort{{ContainerPort: 8080}},
		},
	}

	if got := AccessURLForSpritz(spritz); got != "https://console.example.com/c/openclaw-tide-wind" {
		t.Fatalf("expected shared-host access url, got %q", got)
	}
	if got := InstanceURLForSpritz(spritz); got != "https://console.example.com/i/openclaw-tide-wind/" {
		t.Fatalf("expected shared-host instance url, got %q", got)
	}
}

func TestAccessURLForSpritzPrefersExplicitIngressOverSharedHostRouteModel(t *testing.T) {
	t.Setenv("SPRITZ_ROUTE_MODEL_TYPE", SharedHostRouteModelType)
	t.Setenv("SPRITZ_ROUTE_HOST", "console.example.com")
	t.Setenv("SPRITZ_ROUTE_CHAT_PATH_PREFIX", "/chat")

	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ingress: &SpritzIngress{
				Host: "dedicated.example.com",
				Path: "/custom/openclaw-tide-wind",
			},
		},
	}

	if got := AccessURLForSpritz(spritz); got != "https://dedicated.example.com/chat/openclaw-tide-wind" {
		t.Fatalf("expected explicit ingress access url, got %q", got)
	}
}

func metav1ObjectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
}
