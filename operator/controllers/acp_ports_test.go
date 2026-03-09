package controllers

import (
	"testing"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestContainerPortsAlwaysIncludeReservedACPPort(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Features = &spritzv1.SpritzFeatures{}
	webDisabled := false
	spritz.Spec.Features.Web = &webDisabled

	ports := containerPorts(spritz)
	if len(ports) != 1 {
		t.Fatalf("expected only ACP port when web and ssh are disabled, got %d", len(ports))
	}
	if ports[0].Name != "acp" {
		t.Fatalf("expected ACP container port name, got %q", ports[0].Name)
	}
	if ports[0].ContainerPort != spritzv1.DefaultACPPort {
		t.Fatalf("expected ACP container port %d, got %d", spritzv1.DefaultACPPort, ports[0].ContainerPort)
	}
}

func TestServicePortsDoNotDuplicateExplicitACPPort(t *testing.T) {
	spritz := &spritzv1.Spritz{}
	spritz.Spec.Ports = []spritzv1.SpritzPort{
		{
			Name:          "acp",
			ContainerPort: spritzv1.DefaultACPPort,
			ServicePort:   spritzv1.DefaultACPPort,
		},
	}

	ports := servicePorts(spritz)
	if len(ports) != 1 {
		t.Fatalf("expected explicit ACP service port to be reused, got %d ports", len(ports))
	}
	if ports[0].Name != "acp" {
		t.Fatalf("expected ACP service port name, got %q", ports[0].Name)
	}
	if ports[0].Port != spritzv1.DefaultACPPort {
		t.Fatalf("expected ACP service port %d, got %d", spritzv1.DefaultACPPort, ports[0].Port)
	}
}
