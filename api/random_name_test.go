package main

import (
	"regexp"
	"testing"
)

func TestCreateRandomSpritzNameFormat(t *testing.T) {
	name := createRandomSpritzName(nil)
	if name == "" {
		t.Fatal("expected non-empty name")
	}
	pattern := regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	if !pattern.MatchString(name) {
		t.Fatalf("name %q did not match expected format", name)
	}
}
