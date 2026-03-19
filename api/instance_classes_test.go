package main

import (
	"strings"
	"testing"
)

func TestNewPresetCatalogRejectsInvalidSanitizedInstanceClass(t *testing.T) {
	t.Setenv("SPRITZ_PRESETS", `[{
		"id": "zeno",
		"name": "Zeno",
		"image": "example.com/zeno:latest",
		"instanceClass": "!!!"
	}]`)

	_, err := newPresetCatalog()
	if err == nil {
		t.Fatal("expected invalid instanceClass error")
	}
	if !strings.Contains(err.Error(), "instanceClass is invalid") {
		t.Fatalf("expected invalid instanceClass error, got %v", err)
	}
}
