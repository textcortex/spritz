package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadRevisionSetsContentLength(t *testing.T) {
	tmp, err := os.CreateTemp("", "spritz-bundle-*.tar.gz")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	payload := []byte("bundle-data")
	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		t.Fatalf("write temp file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	var gotLength int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotLength = r.ContentLength
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &sharedMountClient{
		baseURL: srv.URL,
		token:   "token",
		client:  srv.Client(),
	}

	if err := client.uploadRevision(context.Background(), "owner", "mount", "rev", tmp.Name()); err != nil {
		t.Fatalf("uploadRevision failed: %v", err)
	}
	if gotLength != int64(len(payload)) {
		t.Fatalf("expected content length %d, got %d", len(payload), gotLength)
	}
}

func TestWriteTarContentsRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink("../outside", filepath.Join(root, "bad")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := writeTarContents(tw, root)
	_ = tw.Close()
	if err == nil {
		t.Fatal("expected error for escaping symlink")
	}
}

func TestParseLatestManifestWrapped(t *testing.T) {
	body := []byte(`{"status":"success","data":{"revision":"r1","checksum":"sha256:abc","updated_at":"2026-02-11T00:00:00Z"}}`)
	manifest, err := parseLatestManifest(body)
	if err != nil {
		t.Fatalf("parseLatestManifest failed: %v", err)
	}
	if manifest.Revision != "r1" || manifest.Checksum != "sha256:abc" {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}
}

func TestParseLatestManifestWithoutDataFails(t *testing.T) {
	body := []byte(`{"revision":"r2","checksum":"sha256:def","updated_at":"2026-02-11T00:00:00Z"}`)
	_, err := parseLatestManifest(body)
	if err == nil {
		t.Fatal("expected parseLatestManifest to fail without data envelope")
	}
}

func TestLatestRejectsInvalidPayload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "success",
			"data":   map[string]any{"updated_at": "2026-02-11T00:00:00Z"},
		})
	}))
	defer srv.Close()

	client := &sharedMountClient{
		baseURL: srv.URL,
		token:   "token",
		client:  srv.Client(),
	}

	_, _, err := client.latest(context.Background(), "owner", "mount")
	if err == nil {
		t.Fatal("expected error for invalid latest payload")
	}
}
