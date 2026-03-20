package v1

import (
	"fmt"
	"net/url"
	"strings"
)

const defaultWebPort = int32(8080)

// InstanceURLForSpritz returns the canonical instance URL for a spritz based
// on its ingress or primary service port configuration.
func InstanceURLForSpritz(spritz *Spritz) string {
	if spritz == nil {
		return ""
	}
	if !IsWebEnabled(spritz.Spec) {
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

	if routeModel := SharedHostRouteModelFromEnv(); routeModel.Enabled() {
		return routeModel.InstanceURL(spritz.Name)
	}

	return WebServiceURLForSpritz(spritz)
}

// ChatURLForSpritz returns the canonical agent chat URL for a spritz when the
// instance is exposed through a web surface.
func ChatURLForSpritz(spritz *Spritz) string {
	instanceURL := InstanceURLForSpritz(spritz)
	if instanceURL == "" {
		return ""
	}
	parsed, err := url.Parse(instanceURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	name := url.PathEscape(strings.TrimSpace(spritz.Name))
	chatPath := fmt.Sprintf("/c/%s", name)
	if routeModel := SharedHostRouteModelFromEnv(); routeModel.Enabled() {
		chatPath = routeModel.ChatPath(spritz.Name)
	}
	if spritz.Spec.Ingress != nil && spritz.Spec.Ingress.Host != "" {
		parsed.Path = chatPath
		parsed.RawPath = parsed.Path
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	if routeModel := SharedHostRouteModelFromEnv(); routeModel.Enabled() {
		return routeModel.ChatURL(spritz.Name)
	}

	parsed.Path = chatPath
	parsed.RawPath = parsed.Path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

// AccessURLForSpritz returns the canonical primary access URL for a spritz.
// Human-facing clients should use the chat URL when available, and otherwise
// fall back to the instance URL.
func AccessURLForSpritz(spritz *Spritz) string {
	if chatURL := ChatURLForSpritz(spritz); chatURL != "" {
		return chatURL
	}
	return InstanceURLForSpritz(spritz)
}

// IsWebEnabled reports whether the web surface should be exposed for a spritz.
func IsWebEnabled(spec SpritzSpec) bool {
	if spec.Features == nil || spec.Features.Web == nil {
		return true
	}
	return *spec.Features.Web
}
