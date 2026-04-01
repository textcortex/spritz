package v1

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestGeneratedCRDRequiresCompleteRuntimePolicy(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve caller path")
	}
	crdPath := filepath.Clean(
		filepath.Join(
			filepath.Dir(filename),
			"..",
			"..",
			"..",
			"crd",
			"generated",
			"spritz.sh_spritzes.yaml",
		),
	)
	contents, err := os.ReadFile(crdPath)
	if err != nil {
		t.Fatalf("failed to read generated CRD: %v", err)
	}

	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("failed to parse generated CRD: %v", err)
	}

	runtimePolicy := lookupGeneratedRuntimePolicySchema(t, document)
	validations, ok := runtimePolicy["x-kubernetes-validations"].([]any)
	if !ok || len(validations) == 0 {
		t.Fatalf("expected runtimePolicy schema validations, got %#v", runtimePolicy["x-kubernetes-validations"])
	}

	for _, rawValidation := range validations {
		validation, ok := rawValidation.(map[string]any)
		if !ok {
			continue
		}
		message, _ := validation["message"].(string)
		rule, _ := validation["rule"].(string)
		if message == "runtimePolicy requires networkProfile, mountProfile, exposureProfile, and revision together" &&
			strings.Contains(rule, "networkProfile") &&
			strings.Contains(rule, "mountProfile") &&
			strings.Contains(rule, "exposureProfile") &&
			strings.Contains(rule, "revision") {
			return
		}
	}

	t.Fatalf("expected generated CRD to require complete runtimePolicy, got %#v", validations)
}

func lookupGeneratedRuntimePolicySchema(t *testing.T, document map[string]any) map[string]any {
	t.Helper()

	spec := mustMap(t, document["spec"], "spec")
	versions, ok := spec["versions"].([]any)
	if !ok || len(versions) == 0 {
		t.Fatalf("expected CRD versions, got %#v", spec["versions"])
	}
	for _, rawVersion := range versions {
		version := mustMap(t, rawVersion, "version")
		name, _ := version["name"].(string)
		if name != "v1" {
			continue
		}
		schema := mustMap(t, version["schema"], "version.schema")
		openAPIV3Schema := mustMap(t, schema["openAPIV3Schema"], "version.schema.openAPIV3Schema")
		properties := mustMap(t, openAPIV3Schema["properties"], "openAPIV3Schema.properties")
		specSchema := mustMap(t, properties["spec"], "properties.spec")
		specProperties := mustMap(t, specSchema["properties"], "properties.spec.properties")
		return mustMap(t, specProperties["runtimePolicy"], "properties.spec.properties.runtimePolicy")
	}

	t.Fatalf("failed to locate v1 runtimePolicy schema")
	return nil
}

func mustMap(t *testing.T, value any, path string) map[string]any {
	t.Helper()

	typed, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected %s to be a mapping, got %#v", path, value)
	}
	return typed
}
