package main

import (
	"archive/tar"
	"bytes"
	"context"
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
