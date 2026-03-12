package v1

import (
	"fmt"
	"net/url"
	"strings"
)

const defaultWebPort = int32(8080)

// WorkspaceURLForSpritz returns the canonical workspace URL for a spritz based
// on its ingress or primary service port configuration.
func WorkspaceURLForSpritz(spritz *Spritz) string {
	if spritz == nil {
		return ""
	}
	if spritz.Spec.Ingress != nil && spritz.Spec.Ingress.Host != "" {
		path := spritz.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}
		if path != "/" && path[len(path)-1] != '/' {
			path += "/"
		}
		return fmt.Sprintf("https://%s%s", spritz.Spec.Ingress.Host, path)
	}

	if len(spritz.Spec.Ports) == 0 {
		if !IsWebEnabled(spritz.Spec) {
			return ""
		}
		return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", spritz.Name, spritz.Namespace, defaultWebPort)
	}

	port := spritz.Spec.Ports[0]
	servicePort := port.ContainerPort
	if port.ServicePort != 0 {
		servicePort = port.ServicePort
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", spritz.Name, spritz.Namespace, servicePort)
}

// ChatURLForSpritz returns the canonical agent chat URL for a spritz when the
// workspace is exposed through a web surface.
func ChatURLForSpritz(spritz *Spritz) string {
	workspaceURL := WorkspaceURLForSpritz(spritz)
	if workspaceURL == "" {
		return ""
	}
	parsed, err := url.Parse(workspaceURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	parsed.Path = "/"
	parsed.RawPath = "/"
	parsed.RawQuery = ""
	parsed.Fragment = fmt.Sprintf("chat/%s", url.PathEscape(strings.TrimSpace(spritz.Name)))
	return parsed.String()
}

// AccessURLForSpritz returns the canonical primary access URL for a spritz.
// Human-facing clients should use the chat URL when available, and otherwise
// fall back to the workspace URL.
func AccessURLForSpritz(spritz *Spritz) string {
	if chatURL := ChatURLForSpritz(spritz); chatURL != "" {
		return chatURL
	}
	return WorkspaceURLForSpritz(spritz)
}

// IsWebEnabled reports whether the web surface should be exposed for a spritz.
func IsWebEnabled(spec SpritzSpec) bool {
	if spec.Features == nil || spec.Features.Web == nil {
		return true
	}
	return *spec.Features.Web
}
