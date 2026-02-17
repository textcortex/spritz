package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	mountPath := filepath.Join(t.TempDir(), "mount")
	if err := ensureMountPath(mountPath); err != nil {
		t.Fatalf("ensureMountPath failed: %v", err)
	}

	info, err := os.Stat(mountPath)
	if err != nil {
		t.Fatalf("stat mount path failed: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected mount path to be a directory, got mode %v", info.Mode())
	}
	if info.Mode().Perm()&0o020 == 0 {
		t.Fatalf("expected mount directory to be group writable, got mode %v", info.Mode())
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

func TestMigrateLegacyLayoutFlattensCurrent(t *testing.T) {
	mountPath := t.TempDir()
	currentPath := filepath.Join(mountPath, "current")
	if err := os.MkdirAll(currentPath, 0o755); err != nil {
		t.Fatalf("mkdir current failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(currentPath, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write legacy file failed: %v", err)
	}
	if err := os.Symlink(currentPath, filepath.Join(mountPath, "live")); err != nil {
		t.Fatalf("create live symlink failed: %v", err)
	}

	migrated, err := migrateLegacyLayout(mountPath)
	if err != nil {
		t.Fatalf("migrateLegacyLayout failed: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrateLegacyLayout to migrate")
	}
	if _, err := os.Stat(filepath.Join(mountPath, "a.txt")); err != nil {
		t.Fatalf("expected file to be moved to root, got error: %v", err)
	}
	if _, err := os.Stat(currentPath); !os.IsNotExist(err) {
		t.Fatalf("expected current to be removed, got error: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(mountPath, "live")); !os.IsNotExist(err) {
		t.Fatalf("expected live symlink to be removed, got error: %v", err)
	}
}

func TestExtractTarGzPreservesModTime(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "bundle.tar.gz")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatalf("create archive: %v", err)
	}
	gw := gzip.NewWriter(out)
	tw := tar.NewWriter(gw)

	mod := time.Unix(1_700_000_000, 0).UTC()

	if err := tw.WriteHeader(&tar.Header{
		Name:     "d/",
		Mode:     0o755,
		Typeflag: tar.TypeDir,
		ModTime:  mod,
	}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	content := []byte("hello")
	if err := tw.WriteHeader(&tar.Header{
		Name:     "d/a.txt",
		Mode:     0o644,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
		ModTime:  mod,
	}); err != nil {
		t.Fatalf("write file header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write file content: %v", err)
	}

	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close archive: %v", err)
	}

	dest := filepath.Join(root, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	if err := extractTarGz(archive, dest); err != nil {
		t.Fatalf("extractTarGz failed: %v", err)
	}

	fileInfo, err := os.Stat(filepath.Join(dest, "d", "a.txt"))
	if err != nil {
		t.Fatalf("stat extracted file: %v", err)
	}
	if !fileInfo.ModTime().Truncate(time.Second).Equal(mod) {
		t.Fatalf("expected file modtime %s, got %s", mod, fileInfo.ModTime())
	}

	dirInfo, err := os.Stat(filepath.Join(dest, "d"))
	if err != nil {
		t.Fatalf("stat extracted dir: %v", err)
	}
	if !dirInfo.ModTime().Truncate(time.Second).Equal(mod) {
		t.Fatalf("expected dir modtime %s, got %s", mod, dirInfo.ModTime())
	}
}

func TestBundleMountRootChecksumIgnoresGroupWriteBit(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(filePath, []byte(`{"ok":true}`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	checksumA, bundleA, err := bundleMountRoot(root)
	if err != nil {
		t.Fatalf("bundleMountRoot first call failed: %v", err)
	}
	_ = os.Remove(bundleA)

	if err := os.Chmod(filePath, 0o664); err != nil {
		t.Fatalf("chmod file: %v", err)
	}

	checksumB, bundleB, err := bundleMountRoot(root)
	if err != nil {
		t.Fatalf("bundleMountRoot second call failed: %v", err)
	}
	_ = os.Remove(bundleB)

	if checksumA != checksumB {
		t.Fatalf("expected checksum to be stable across group-write normalization, got %s vs %s", checksumA, checksumB)
	}
}
