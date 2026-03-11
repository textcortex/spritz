package v1

import "fmt"

const defaultWebPort = int32(8080)

// AccessURLForSpritz returns the canonical access URL for a spritz based on its
// ingress or primary service port configuration.
func AccessURLForSpritz(spritz *Spritz) string {
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

// IsWebEnabled reports whether the web surface should be exposed for a spritz.
func IsWebEnabled(spec SpritzSpec) bool {
	if spec.Features == nil || spec.Features.Web == nil {
		return true
	}
	return *spec.Features.Web
}
