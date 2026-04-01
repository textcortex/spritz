package acptext

import "testing"

func TestExtractPreservesWhitespaceInTextBlocks(t *testing.T) {
	got := Extract([]any{
		map[string]any{"text": "hello"},
		map[string]any{"text": " world"},
		map[string]any{"text": "\nagain"},
	})
	want := "hello\n world\n\nagain"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestExtractSupportsResourceBlocks(t *testing.T) {
	if got := Extract(map[string]any{"resource": map[string]any{"text": "resource text"}}); got != "resource text" {
		t.Fatalf("expected resource text, got %q", got)
	}
	if got := Extract(map[string]any{"resource": map[string]any{"uri": "file://workspace/report.txt"}}); got != "file://workspace/report.txt" {
		t.Fatalf("expected resource uri, got %q", got)
	}
	if got := Extract(map[string]any{"resource": map[string]any{"text": "", "uri": "file://workspace/fallback.txt"}}); got != "file://workspace/fallback.txt" {
		t.Fatalf("expected resource uri fallback, got %q", got)
	}
}

func TestJoinChunksPreservesChunkBoundaryWhitespaceAndNewlines(t *testing.T) {
	got := JoinChunks([]any{
		[]any{map[string]any{"text": "I'll "}},
		[]any{map[string]any{"text": "spawn a dedicated agent for you using the"}},
		[]any{map[string]any{"text": "\nSpritz controls.\n\nThe"}},
		[]any{map[string]any{"text": " Slack account could not be resolved.\n"}},
	})
	want := "I'll spawn a dedicated agent for you using the\nSpritz controls.\n\nThe Slack account could not be resolved.\n"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
