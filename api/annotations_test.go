package main

import "testing"

func TestParseKeyValueCSV(t *testing.T) {
	if got, err := parseKeyValueCSV(""); err != nil || got != nil {
		t.Fatalf("expected nil for empty input, got %v (err=%v)", got, err)
	}

	got, err := parseKeyValueCSV("spritz.sh/integration.repo-auth=github-app, foo=bar")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["spritz.sh/integration.repo-auth"] != "github-app" || got["foo"] != "bar" {
		t.Fatalf("unexpected map: %v", got)
	}

	if _, err := parseKeyValueCSV("missingequals"); err == nil {
		t.Fatal("expected error for missing equals")
	}
	if _, err := parseKeyValueCSV("=novalue"); err == nil {
		t.Fatal("expected error for empty key")
	}
	if _, err := parseKeyValueCSV("key="); err == nil {
		t.Fatal("expected error for empty value")
	}
}
