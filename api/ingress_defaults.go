package main

import (
	"os"
	"strings"

	spritzv1 "spritz.sh/operator/api/v1"
)

type ingressDefaults struct {
	Mode               string
	HostTemplate       string
	Path               string
	ClassName          string
	GatewayName        string
	GatewayNamespace   string
	GatewaySectionName string
}

func newIngressDefaults() ingressDefaults {
	return ingressDefaults{
		Mode:               os.Getenv("SPRITZ_DEFAULT_INGRESS_MODE"),
		HostTemplate:       os.Getenv("SPRITZ_DEFAULT_INGRESS_HOST_TEMPLATE"),
		Path:               os.Getenv("SPRITZ_DEFAULT_INGRESS_PATH"),
		ClassName:          os.Getenv("SPRITZ_DEFAULT_INGRESS_CLASS_NAME"),
		GatewayName:        os.Getenv("SPRITZ_DEFAULT_INGRESS_GATEWAY_NAME"),
		GatewayNamespace:   os.Getenv("SPRITZ_DEFAULT_INGRESS_GATEWAY_NAMESPACE"),
		GatewaySectionName: os.Getenv("SPRITZ_DEFAULT_INGRESS_GATEWAY_SECTION_NAME"),
	}
}

func (d ingressDefaults) enabled() bool {
	return d.Mode != "" || d.HostTemplate != "" || d.Path != "" || d.ClassName != "" ||
		d.GatewayName != "" || d.GatewayNamespace != "" || d.GatewaySectionName != ""
}

func applyIngressDefaults(spec *spritzv1.SpritzSpec, name, namespace string, defaults ingressDefaults) {
	if !defaults.enabled() {
		return
	}
	if spec.Ingress == nil && isWebDisabled(spec) {
		return
	}

	if spec.Ingress == nil {
		spec.Ingress = &spritzv1.SpritzIngress{}
	}

	if spec.Ingress.Mode == "" && defaults.Mode != "" {
		spec.Ingress.Mode = defaults.Mode
	}
	if spec.Ingress.Host == "" && defaults.HostTemplate != "" {
		spec.Ingress.Host = expandIngressTemplate(defaults.HostTemplate, name, namespace)
	}
	if spec.Ingress.Path == "" && defaults.Path != "" {
		spec.Ingress.Path = expandIngressTemplate(defaults.Path, name, namespace)
	}
	if spec.Ingress.ClassName == "" && defaults.ClassName != "" {
		spec.Ingress.ClassName = defaults.ClassName
	}
	if spec.Ingress.GatewayName == "" && defaults.GatewayName != "" {
		spec.Ingress.GatewayName = defaults.GatewayName
	}
	if spec.Ingress.GatewayNamespace == "" && defaults.GatewayNamespace != "" {
		spec.Ingress.GatewayNamespace = defaults.GatewayNamespace
	}
	if spec.Ingress.GatewaySectionName == "" && defaults.GatewaySectionName != "" {
		spec.Ingress.GatewaySectionName = defaults.GatewaySectionName
	}
}

func isWebDisabled(spec *spritzv1.SpritzSpec) bool {
	if spec.Features == nil || spec.Features.Web == nil {
		return false
	}
	return !*spec.Features.Web
}

func expandIngressTemplate(template, name, namespace string) string {
	replacer := strings.NewReplacer(
		"{name}", name,
		"{namespace}", namespace,
	)
	return replacer.Replace(template)
}
