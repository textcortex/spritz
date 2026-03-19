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
				Path: "/w/openclaw-tide-wind",
			},
		},
	}

	if got := InstanceURLForSpritz(spritz); got != "https://console.example.com/w/openclaw-tide-wind/" {
		t.Fatalf("expected instance url, got %q", got)
	}
}

func TestChatURLForSpritzUsesRootHashRoute(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ingress: &SpritzIngress{
				Host: "console.example.com",
				Path: "/w/openclaw-tide-wind",
			},
		},
	}

	if got := ChatURLForSpritz(spritz); got != "https://console.example.com/#chat/openclaw-tide-wind" {
		t.Fatalf("expected chat url, got %q", got)
	}
}

func TestAccessURLForSpritzPromotesChatURL(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ingress: &SpritzIngress{
				Host: "console.example.com",
				Path: "/w/openclaw-tide-wind",
			},
		},
	}

	if got := AccessURLForSpritz(spritz); got != "https://console.example.com/#chat/openclaw-tide-wind" {
		t.Fatalf("expected access url to prefer chat url, got %q", got)
	}
}

func TestAccessURLForSpritzFallsBackToInstanceURL(t *testing.T) {
	spritz := &Spritz{
		ObjectMeta: metav1ObjectMeta("openclaw-tide-wind", "spritz-test"),
		Spec: SpritzSpec{
			Ports: []SpritzPort{{ContainerPort: 8080}},
		},
	}

	want := "http://openclaw-tide-wind.spritz-test.svc.cluster.local:8080/#chat/openclaw-tide-wind"
	if got := AccessURLForSpritz(spritz); got != want {
		t.Fatalf("expected access url %q, got %q", want, got)
	}
}

func metav1ObjectMeta(name, namespace string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
	}
}
