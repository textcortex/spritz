package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"spritz.sh/operator/sharedmounts"
)

const (
	defaultPollSeconds    = 30
	defaultPublishSeconds = 60
)

type sharedMountClient struct {
	baseURL string
	token   string
	client  *http.Client
}

type sharedMountState struct {
	spec            sharedmounts.MountSpec
	currentRevision string
	currentChecksum string
}

func main() {
	mode := flag.String("mode", "", "init or sidecar")
	flag.Parse()

	logger := log.New(os.Stdout, "[shared-syncer] ", log.LstdFlags)

	mounts, apiURL, token, ownerID, err := loadConfig()
	if err != nil {
		logger.Fatalf("config error: %v", err)
	}
	if len(mounts) == 0 {
		logger.Print("no shared mounts configured; exiting")
		return
	}

	client := &sharedMountClient{
		baseURL: strings.TrimRight(apiURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}

	state := make([]*sharedMountState, 0, len(mounts))
	for _, mount := range mounts {
		state = append(state, &sharedMountState{spec: mount})
	}

	ctx := context.Background()
	if err := runInit(ctx, logger, client, ownerID, state); err != nil {
		logger.Fatalf("init failed: %v", err)
	}

	switch strings.ToLower(strings.TrimSpace(*mode)) {
	case "init":
		return
	case "sidecar":
		runSidecar(ctx, logger, client, ownerID, state)
	default:
		logger.Fatalf("invalid mode: %s", *mode)
	}
}

func loadConfig() ([]sharedmounts.MountSpec, string, string, string, error) {
	rawMounts := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS"))
	mounts, err := sharedmounts.ParseMountsJSON(rawMounts)
	if err != nil {
		return nil, "", "", "", err
	}
	apiURL := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_API_URL"))
	if apiURL == "" {
		return nil, "", "", "", fmt.Errorf("SPRITZ_SHARED_MOUNTS_API_URL is required")
	}
	token := strings.TrimSpace(os.Getenv("SPRITZ_SHARED_MOUNTS_TOKEN"))
	if token == "" {
		return nil, "", "", "", fmt.Errorf("SPRITZ_SHARED_MOUNTS_TOKEN is required")
	}
	ownerID := strings.TrimSpace(os.Getenv("SPRITZ_OWNER_ID"))
	if err := sharedmounts.ValidateScopeID(ownerID); err != nil {
		return nil, "", "", "", err
	}
	for _, mount := range mounts {
		if err := sharedmounts.ValidateName(mount.Name); err != nil {
			return nil, "", "", "", err
		}
		if err := sharedmounts.ValidateScope(mount.Scope); err != nil {
			return nil, "", "", "", err
		}
		if mount.Scope != sharedmounts.ScopeOwner {
			return nil, "", "", "", fmt.Errorf("unsupported shared mount scope: %s", mount.Scope)
		}
		if strings.TrimSpace(mount.MountPath) == "" {
			return nil, "", "", "", fmt.Errorf("mountPath is required for shared mount %s", mount.Name)
		}
	}
	return mounts, apiURL, token, ownerID, nil
}

func runInit(ctx context.Context, logger *log.Logger, client *sharedMountClient, ownerID string, mounts []*sharedMountState) error {
	for _, state := range mounts {
		if err := ensureMountPath(state.spec.MountPath); err != nil {
			return err
		}
		manifest, found, err := client.latest(ctx, ownerID, state.spec.Name)
		if err != nil {
			return err
		}
		if !found {
			if err := ensureEmptyLive(state.spec.MountPath); err != nil {
				return err
			}
			continue
		}
		if err := applyRevision(ctx, client, ownerID, state.spec, manifest.Revision); err != nil {
			return err
		}
		state.currentRevision = manifest.Revision
		state.currentChecksum = manifest.Checksum
	}
	logger.Print("init complete")
	return nil
}

func runSidecar(ctx context.Context, logger *log.Logger, client *sharedMountClient, ownerID string, mounts []*sharedMountState) {
	for _, state := range mounts {
		state := state
		if state.spec.SyncMode == sharedmounts.SyncPoll {
			go pollLoop(ctx, logger, client, ownerID, state)
		}
		if state.spec.Mode == sharedmounts.ModeSnapshot {
			go publishLoop(ctx, logger, client, ownerID, state)
		}
	}

	select {}
}

func pollLoop(ctx context.Context, logger *log.Logger, client *sharedMountClient, ownerID string, state *sharedMountState) {
	interval := state.spec.PollSeconds
	if interval <= 0 {
		interval = defaultPollSeconds
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			manifest, found, err := client.latest(ctx, ownerID, state.spec.Name)
			if err != nil {
				logger.Printf("poll error for %s: %v", state.spec.Name, err)
				continue
			}
			if !found {
				continue
			}
			if manifest.Revision == state.currentRevision {
				continue
			}
			if err := applyRevision(ctx, client, ownerID, state.spec, manifest.Revision); err != nil {
				logger.Printf("apply error for %s: %v", state.spec.Name, err)
				continue
			}
			state.currentRevision = manifest.Revision
			state.currentChecksum = manifest.Checksum
		}
	}
}

func publishLoop(ctx context.Context, logger *log.Logger, client *sharedMountClient, ownerID string, state *sharedMountState) {
	interval := state.spec.PublishSeconds
	if interval <= 0 {
		interval = defaultPublishSeconds
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checksum, bundle, err := bundleLive(state.spec.MountPath)
			if err != nil {
				logger.Printf("bundle error for %s: %v", state.spec.Name, err)
				continue
			}
			checksumValue := "sha256:" + checksum
			if checksumValue == state.currentChecksum {
				_ = os.Remove(bundle)
				continue
			}
			revision := time.Now().UTC().Format("2006-01-02T15-04-05Z")
			if err := client.uploadRevision(ctx, ownerID, state.spec.Name, revision, bundle); err != nil {
				_ = os.Remove(bundle)
				logger.Printf("upload error for %s: %v", state.spec.Name, err)
				continue
			}
			manifest := sharedmounts.LatestManifest{
				Revision:  revision,
				Checksum:  checksumValue,
				UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			}
			if err := client.updateLatest(ctx, ownerID, state.spec.Name, manifest, state.currentRevision); err != nil {
				if errors.Is(err, errConflict) {
					latest, found, latestErr := client.latest(ctx, ownerID, state.spec.Name)
					if latestErr == nil && found {
						state.currentRevision = latest.Revision
						state.currentChecksum = latest.Checksum
					}
					_ = os.Remove(bundle)
					continue
				}
				_ = os.Remove(bundle)
				logger.Printf("latest update error for %s: %v", state.spec.Name, err)
				continue
			}
			_ = os.Remove(bundle)
			state.currentRevision = manifest.Revision
			state.currentChecksum = manifest.Checksum
		}
	}
}

func ensureMountPath(mountPath string) error {
	return os.MkdirAll(mountPath, 0o755)
}

func ensureEmptyLive(mountPath string) error {
	currentPath := filepath.Join(mountPath, "current")
	if err := os.MkdirAll(currentPath, 0o755); err != nil {
		return err
	}
	return updateLiveSymlink(mountPath, currentPath)
}

func applyRevision(ctx context.Context, client *sharedMountClient, ownerID string, spec sharedmounts.MountSpec, revision string) error {
	if err := ensureMountPath(spec.MountPath); err != nil {
		return err
	}
	tempFile, err := os.CreateTemp("", "spritz-shared-*.tar.gz")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	if err := client.downloadRevision(ctx, ownerID, spec.Name, revision, tempFile); err != nil {
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	incoming := filepath.Join(spec.MountPath, ".incoming-"+revision)
	_ = os.RemoveAll(incoming)
	if err := os.MkdirAll(incoming, 0o755); err != nil {
		return err
	}
	if err := extractTarGz(tempPath, incoming); err != nil {
		return err
	}
	currentPath := filepath.Join(spec.MountPath, "current")
	_ = os.RemoveAll(currentPath)
	if err := os.Rename(incoming, currentPath); err != nil {
		return err
	}
	return updateLiveSymlink(spec.MountPath, currentPath)
}

func updateLiveSymlink(mountPath, target string) error {
	livePath := filepath.Join(mountPath, "live")
	tmpName := fmt.Sprintf(".live-tmp-%d", time.Now().UnixNano())
	tmpPath := filepath.Join(mountPath, tmpName)
	_ = os.RemoveAll(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, livePath); err != nil {
		_ = os.RemoveAll(tmpPath)
		return err
	}
	return nil
}

func bundleLive(mountPath string) (string, string, error) {
	livePath := filepath.Join(mountPath, "live")
	realPath, err := filepath.EvalSymlinks(livePath)
	if err != nil {
		realPath = livePath
	}
	stat, err := os.Stat(realPath)
	if err != nil {
		return "", "", err
	}
	if !stat.IsDir() {
		return "", "", fmt.Errorf("live path is not a directory: %s", realPath)
	}
	file, err := os.CreateTemp("", "spritz-shared-*.tar.gz")
	if err != nil {
		return "", "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(file.Name())
		}
	}()
	hasher := sha256.New()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(io.MultiWriter(gzipWriter, hasher))
	if err := writeTarContents(tarWriter, realPath); err != nil {
		_ = tarWriter.Close()
		_ = gzipWriter.Close()
		_ = file.Close()
		return "", "", err
	}
	if err := tarWriter.Close(); err != nil {
		_ = gzipWriter.Close()
		_ = file.Close()
		return "", "", err
	}
	if err := gzipWriter.Close(); err != nil {
		_ = file.Close()
		return "", "", err
	}
	if err := file.Close(); err != nil {
		return "", "", err
	}
	checksum := hex.EncodeToString(hasher.Sum(nil))
	cleanup = false
	return checksum, file.Name(), nil
}

func writeTarContents(tw *tar.Writer, root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
			if entry.Type()&os.ModeSymlink != 0 {
				link, err := os.Readlink(path)
				if err != nil {
					return err
				}
				if filepath.IsAbs(link) {
					return fmt.Errorf("absolute symlinks are not supported: %s", path)
				}
				cleaned := filepath.Clean(link)
				if cleaned == "." || strings.HasPrefix(cleaned, "..") {
					return fmt.Errorf("symlink target escapes bundle: %s", path)
				}
				header.Typeflag = tar.TypeSymlink
				header.Linkname = link
			}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			if _, err := io.Copy(tw, file); err != nil {
				_ = file.Close()
				return err
			}
			_ = file.Close()
		}
		return nil
	})
}

func extractTarGz(archivePath, dest string) error {
	cleanDest := filepath.Clean(dest)
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if header == nil {
			continue
		}
		target := filepath.Join(cleanDest, filepath.Clean(header.Name))
		if target != cleanDest && !strings.HasPrefix(target, cleanDest+string(os.PathSeparator)) {
			return fmt.Errorf("invalid archive path: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				_ = out.Close()
				return err
			}
			_ = out.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			link := header.Linkname
			if filepath.IsAbs(link) {
				return fmt.Errorf("absolute symlink in archive: %s", header.Name)
			}
			cleanLink := filepath.Clean(link)
			if cleanLink == ".." || strings.HasPrefix(cleanLink, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("symlink escapes mount: %s", header.Name)
			}
			_ = os.RemoveAll(target)
			if err := os.Symlink(link, target); err != nil {
				return err
			}
		default:
			continue
		}
	}
}

var errConflict = errors.New("conflict")

func (c *sharedMountClient) latest(ctx context.Context, ownerID, mount string) (sharedmounts.LatestManifest, bool, error) {
	endpoint := c.endpoint(ownerID, mount, "latest")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	c.applyAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return sharedmounts.LatestManifest{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return sharedmounts.LatestManifest{}, false, fmt.Errorf("latest fetch failed: %s", strings.TrimSpace(string(body)))
	}
	var manifest sharedmounts.LatestManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	return manifest, true, nil
}

func (c *sharedMountClient) downloadRevision(ctx context.Context, ownerID, mount, revision string, dest io.Writer) error {
	endpoint := c.endpoint(ownerID, mount, "revisions", revision)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	c.applyAuth(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revision fetch failed: %s", strings.TrimSpace(string(body)))
	}
	_, err = io.Copy(dest, resp.Body)
	return err
}

func (c *sharedMountClient) uploadRevision(ctx context.Context, ownerID, mount, revision, bundlePath string) error {
	file, err := os.Open(bundlePath)
	if err != nil {
		return err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return err
	}
	endpoint := c.endpoint(ownerID, mount, "revisions", revision)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, file)
	if err != nil {
		return err
	}
	req.ContentLength = stat.Size()
	c.applyAuth(req)
	req.Header.Set("Content-Type", "application/gzip")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("revision upload failed: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *sharedMountClient) updateLatest(ctx context.Context, ownerID, mount string, manifest sharedmounts.LatestManifest, ifMatch string) error {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	endpoint := c.endpoint(ownerID, mount, "latest")
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	c.applyAuth(req)
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(ifMatch) != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusConflict {
		return errConflict
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("latest update failed: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func (c *sharedMountClient) endpoint(ownerID, mount string, parts ...string) string {
	segments := []string{"internal", "v1", "shared-mounts", "owner", url.PathEscape(ownerID), url.PathEscape(mount)}
	segments = append(segments, parts...)
	return c.baseURL + "/" + strings.Join(segments, "/")
}

func (c *sharedMountClient) applyAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
}
