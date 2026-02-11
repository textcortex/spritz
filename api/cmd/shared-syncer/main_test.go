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

func TestEnsureEmptyLiveCreatesWritableCurrent(t *testing.T) {
	mountPath := t.TempDir()
	if err := ensureEmptyLive(mountPath); err != nil {
		t.Fatalf("ensureEmptyLive failed: %v", err)
	}

	currentPath := filepath.Join(mountPath, "current")
	info, err := os.Stat(currentPath)
	if err != nil {
		t.Fatalf("stat current path failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected current path to be a directory, got mode %v", info.Mode())
	}
	if info.Mode().Perm()&0o020 == 0 {
		t.Fatalf("expected current directory to be group writable, got mode %v", info.Mode())
	}
}

func TestEnforceGroupWritableTreeAddsGroupWrite(t *testing.T) {
	root := t.TempDir()
	subDir := filepath.Join(root, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	filePath := filepath.Join(subDir, "config.json")
	if err := os.WriteFile(filePath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}

	if err := enforceGroupWritableTree(root); err != nil {
		t.Fatalf("enforceGroupWritableTree failed: %v", err)
	}

	dirInfo, err := os.Stat(subDir)
	if err != nil {
		t.Fatalf("stat dir failed: %v", err)
	}
	if dirInfo.Mode().Perm()&0o020 == 0 {
		t.Fatalf("expected group writable directory, got mode %v", dirInfo.Mode())
	}

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		t.Fatalf("stat file failed: %v", err)
	}
	if fileInfo.Mode().Perm()&0o020 == 0 {
		t.Fatalf("expected group writable file, got mode %v", fileInfo.Mode())
	}
}
