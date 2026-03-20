package v1

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

const (
	SharedHostRouteModelType    = "shared-host"
	defaultAPIPathPrefix        = "/api"
	defaultAuthPathPrefix       = "/oauth2"
	defaultInstancePathPrefix   = "/i"
	defaultChatPathPrefix       = "/c"
	sharedHostRouteModelTypeEnv = "SPRITZ_ROUTE_MODEL_TYPE"
	sharedHostRouteHostEnv      = "SPRITZ_ROUTE_HOST"
	sharedHostAPIPathPrefixEnv  = "SPRITZ_ROUTE_API_PATH_PREFIX"
	sharedHostAuthPathPrefixEnv = "SPRITZ_ROUTE_AUTH_PATH_PREFIX"
	sharedHostInstancePrefixEnv = "SPRITZ_ROUTE_INSTANCE_PATH_PREFIX"
	sharedHostChatPathPrefixEnv = "SPRITZ_ROUTE_CHAT_PATH_PREFIX"
)

// SharedHostRouteModel describes the canonical shared-host route contract that
// API and operator components use to form external URLs and public route
// prefixes.
type SharedHostRouteModel struct {
	Type               string
	Host               string
	APIPathPrefix      string
	AuthPathPrefix     string
	InstancePathPrefix string
	ChatPathPrefix     string
}

// SharedHostRouteModelFromEnv loads the canonical shared-host route model from
// process environment with stable Spritz defaults.
func SharedHostRouteModelFromEnv() SharedHostRouteModel {
	return SharedHostRouteModel{
		Type:               normalizeRouteModelType(os.Getenv(sharedHostRouteModelTypeEnv)),
		Host:               normalizeRouteHost(os.Getenv(sharedHostRouteHostEnv)),
		APIPathPrefix:      normalizeRoutePrefix(os.Getenv(sharedHostAPIPathPrefixEnv), defaultAPIPathPrefix),
		AuthPathPrefix:     normalizeRoutePrefix(os.Getenv(sharedHostAuthPathPrefixEnv), defaultAuthPathPrefix),
		InstancePathPrefix: normalizeRoutePrefix(os.Getenv(sharedHostInstancePrefixEnv), defaultInstancePathPrefix),
		ChatPathPrefix:     normalizeRoutePrefix(os.Getenv(sharedHostChatPathPrefixEnv), defaultChatPathPrefix),
	}
}

// Enabled reports whether shared-host canonical URL generation should be used.
func (m SharedHostRouteModel) Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(m.Type), SharedHostRouteModelType) && strings.TrimSpace(m.Host) != ""
}

// InstancePath returns the canonical external instance path for the named
// spritz.
func (m SharedHostRouteModel) InstancePath(name string) string {
	return joinSharedHostPath(m.InstancePathPrefix, name)
}

// ChatPath returns the canonical external chat path for the named spritz.
func (m SharedHostRouteModel) ChatPath(name string) string {
	return joinSharedHostPath(m.ChatPathPrefix, name)
}

// InstanceURL returns the canonical external instance URL for the named spritz.
func (m SharedHostRouteModel) InstanceURL(name string) string {
	if !m.Enabled() {
		return ""
	}
	return fmt.Sprintf("https://%s%s/", m.Host, m.InstancePath(name))
}

// ChatURL returns the canonical external chat URL for the named spritz.
func (m SharedHostRouteModel) ChatURL(name string) string {
	if !m.Enabled() {
		return ""
	}
	return fmt.Sprintf("https://%s%s", m.Host, m.ChatPath(name))
}

// HTTPServicePortForSpritz returns the canonical service port for the web
// workload surface.
func HTTPServicePortForSpritz(spritz *Spritz) int32 {
	if spritz == nil {
		return defaultWebPort
	}
	if len(spritz.Spec.Ports) == 0 {
		return defaultWebPort
	}
	for _, port := range spritz.Spec.Ports {
		if strings.EqualFold(strings.TrimSpace(port.Name), "http") {
			if port.ServicePort != 0 {
				return port.ServicePort
			}
			return port.ContainerPort
		}
	}
	port := spritz.Spec.Ports[0]
	if port.ServicePort != 0 {
		return port.ServicePort
	}
	return port.ContainerPort
}

// WebServiceURLForSpritz resolves the in-cluster HTTP service URL for the
// workload web surface.
func WebServiceURLForSpritz(spritz *Spritz) string {
	if spritz == nil || !IsWebEnabled(spritz.Spec) {
		return ""
	}
	return fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d",
		spritz.Name,
		spritz.Namespace,
		HTTPServicePortForSpritz(spritz),
	)
}

func normalizeRouteModelType(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return SharedHostRouteModelType
	}
	return strings.ToLower(trimmed)
}

func normalizeRouteHost(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, "://") {
		if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
			return parsed.Host
		}
	}
	return strings.TrimSuffix(trimmed, "/")
}

func normalizeRoutePrefix(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = fallback
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	if len(trimmed) > 1 {
		trimmed = strings.TrimRight(trimmed, "/")
	}
	return trimmed
}

func joinSharedHostPath(prefix, name string) string {
	prefix = normalizeRoutePrefix(prefix, "/")
	name = url.PathEscape(strings.TrimSpace(name))
	if prefix == "/" {
		return "/" + name
	}
	return prefix + "/" + name
}
