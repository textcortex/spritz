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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"spritz.sh/operator/sharedmounts"
)

const (
	defaultPollSeconds    = 30
	defaultPublishSeconds = 60
	sharedDirPerm         = 0o2775
	sharedFilePermMask    = 0o020
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
	mu              sync.Mutex
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
		// Long-polling calls can legitimately hold the connection open.
		// Prefer per-request timeouts (via context) over a tight global client timeout.
		client: &http.Client{Timeout: 5 * time.Minute},
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

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		state.mu.Lock()
		current := state.currentRevision
		state.mu.Unlock()

		manifest, found, err := client.latestWait(ctx, ownerID, state.spec.Name, current, interval)
		if err != nil {
			logger.Printf("poll error for %s: %v", state.spec.Name, err)
			time.Sleep(2 * time.Second)
			continue
		}
		if !found {
			continue
		}
		if manifest.Revision == current {
			continue
		}
		state.mu.Lock()
		err = applyRevision(ctx, client, ownerID, state.spec, manifest.Revision)
		if err == nil {
			state.currentRevision = manifest.Revision
			state.currentChecksum = manifest.Checksum
		}
		state.mu.Unlock()
		if err != nil {
			logger.Printf("apply error for %s: %v", state.spec.Name, err)
			continue
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

	trigger := make(chan struct{}, 1)
	go watchMount(ctx, logger, state.spec.MountPath, trigger)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-trigger:
		}

		state.mu.Lock()
		checksum, bundle, err := bundleMountRoot(state.spec.MountPath)
		state.mu.Unlock()
		if err != nil {
			logger.Printf("bundle error for %s: %v", state.spec.Name, err)
			continue
		}
		checksumValue := "sha256:" + checksum
		state.mu.Lock()
		currentChecksum := state.currentChecksum
		expectedRevision := state.currentRevision
		state.mu.Unlock()
		if checksumValue == currentChecksum {
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
		if err := client.updateLatest(ctx, ownerID, state.spec.Name, manifest, expectedRevision); err != nil {
			if errors.Is(err, errConflict) {
				latest, found, latestErr := client.latest(ctx, ownerID, state.spec.Name)
				if latestErr == nil && found {
					state.mu.Lock()
					state.currentRevision = latest.Revision
					state.currentChecksum = latest.Checksum
					state.mu.Unlock()
				}
				_ = os.Remove(bundle)
				continue
			}
			_ = os.Remove(bundle)
			logger.Printf("latest update error for %s: %v", state.spec.Name, err)
			continue
		}
		_ = os.Remove(bundle)
		state.mu.Lock()
		state.currentRevision = manifest.Revision
		state.currentChecksum = manifest.Checksum
		state.mu.Unlock()
	}
}

func watchMount(ctx context.Context, logger *log.Logger, mountPath string, trigger chan<- struct{}) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		logger.Printf("watch error for %s: %v", mountPath, err)
		return
	}
	defer watcher.Close()

	if err := addWatchRecursive(watcher, mountPath, mountPath); err != nil {
		logger.Printf("watch error for %s: %v", mountPath, err)
		return
	}

	debounceDelay := 1 * time.Second
	var debounceTimer *time.Timer
	var debounceCh <-chan time.Time

	resetDebounce := func() {
		if debounceTimer == nil {
			debounceTimer = time.NewTimer(debounceDelay)
			debounceCh = debounceTimer.C
			return
		}
		if !debounceTimer.Stop() {
			select {
			case <-debounceTimer.C:
			default:
			}
		}
		debounceTimer.Reset(debounceDelay)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-debounceCh:
			debounceCh = nil
			debounceTimer = nil
			select {
			case trigger <- struct{}{}:
			default:
			}
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !shouldTriggerPublish(ev.Op) {
				continue
			}
			if shouldIgnoreWatchEvent(mountPath, ev.Name) {
				continue
			}
			// Ensure new directories are watched.
			if ev.Op&fsnotify.Create != 0 {
				info, statErr := os.Stat(ev.Name)
				if statErr == nil && info.IsDir() {
					_ = addWatchRecursive(watcher, mountPath, ev.Name)
				}
			}
			resetDebounce()
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				return
			}
			if watchErr != nil {
				logger.Printf("watch error for %s: %v", mountPath, watchErr)
			}
		}
	}
}

func shouldIgnoreWatchEvent(mountRoot, path string) bool {
	rel, err := filepath.Rel(mountRoot, path)
	if err != nil || rel == "." || rel == "" {
		return false
	}
	firstComponent := strings.Split(rel, string(os.PathSeparator))[0]
	return strings.HasPrefix(firstComponent, ".incoming-") || firstComponent == "current" || firstComponent == "live"
}

func shouldTriggerPublish(op fsnotify.Op) bool {
	// Chmod is noisy (e.g. permission fixups) and doesn't imply content changes.
	mask := fsnotify.Create | fsnotify.Write | fsnotify.Remove | fsnotify.Rename
	return op&mask != 0
}

func addWatchRecursive(watcher *fsnotify.Watcher, mountRoot, walkRoot string) error {
	mountRootClean := filepath.Clean(mountRoot)
	walkRootClean := filepath.Clean(walkRoot)
	if shouldIgnoreWatchEvent(mountRootClean, walkRootClean) {
		return nil
	}
	return filepath.WalkDir(walkRootClean, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if shouldIgnoreWatchEvent(mountRootClean, path) {
			return filepath.SkipDir
		}
		if addErr := watcher.Add(path); addErr != nil {
			// If we can't watch a subtree, we still want the rest of the tree to work.
			if path == walkRootClean {
				return addErr
			}
			return filepath.SkipDir
		}
		return nil
	})
}

func ensureMountPath(mountPath string) error {
	if err := os.MkdirAll(mountPath, sharedDirPerm); err != nil {
		return err
	}
	// Legacy layout used mountPath/current for data and mountPath/live as a symlink.
	// We now treat mountPath itself as the data root, so migrate once if detected.
	if _, err := migrateLegacyLayout(mountPath); err != nil {
		return err
	}
	return enforceGroupWritableTree(mountPath)
}

func migrateLegacyLayout(mountPath string) (bool, error) {
	currentPath := filepath.Join(mountPath, "current")
	currentInfo, err := os.Stat(currentPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if !currentInfo.IsDir() {
		return false, nil
	}

	livePath := filepath.Join(mountPath, "live")
	liveInfo, err := os.Lstat(livePath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if liveInfo.Mode()&os.ModeSymlink == 0 {
		return false, nil
	}

	entries, err := os.ReadDir(currentPath)
	if err != nil {
		return true, err
	}
	for _, entry := range entries {
		src := filepath.Join(currentPath, entry.Name())
		dst := filepath.Join(mountPath, entry.Name())
		if _, err := os.Lstat(dst); err == nil {
			return true, fmt.Errorf("legacy shared mount migration conflict: %s already exists", dst)
		} else if !os.IsNotExist(err) {
			return true, err
		}
		if err := os.Rename(src, dst); err != nil {
			return true, err
		}
	}
	// Best-effort cleanup. If cleanup fails, leave a clear error rather than continuing
	// with a half-migrated tree that could corrupt sync.
	if err := os.Remove(livePath); err != nil && !os.IsNotExist(err) {
		return true, err
	}
	if err := os.RemoveAll(currentPath); err != nil {
		return true, err
	}
	return true, nil
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
	if err := os.MkdirAll(incoming, sharedDirPerm); err != nil {
		return err
	}
	if err := extractTarGz(tempPath, incoming); err != nil {
		return err
	}
	if err := enforceGroupWritableTree(incoming); err != nil {
		return err
	}
	if err := replaceMountContents(spec.MountPath, incoming); err != nil {
		return err
	}
	return enforceGroupWritableTree(spec.MountPath)
}

func replaceMountContents(mountPath, incoming string) error {
	incomingBase := filepath.Base(incoming)
	entries, err := os.ReadDir(mountPath)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == incomingBase {
			continue
		}
		if strings.HasPrefix(name, ".incoming-") {
			_ = os.RemoveAll(filepath.Join(mountPath, name))
			continue
		}
		// Legacy layout control entries. If they still exist, they are not data.
		if name == "current" || name == "live" {
			_ = os.RemoveAll(filepath.Join(mountPath, name))
			continue
		}
		if err := os.RemoveAll(filepath.Join(mountPath, name)); err != nil {
			return err
		}
	}
	incomingEntries, err := os.ReadDir(incoming)
	if err != nil {
		return err
	}
	for _, entry := range incomingEntries {
		src := filepath.Join(incoming, entry.Name())
		dst := filepath.Join(mountPath, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return os.RemoveAll(incoming)
}

func bundleMountRoot(mountPath string) (string, string, error) {
	stat, err := os.Stat(mountPath)
	if err != nil {
		return "", "", err
	}
	if !stat.IsDir() {
		return "", "", fmt.Errorf("mount path is not a directory: %s", mountPath)
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
	if err := writeTarContents(tarWriter, mountPath); err != nil {
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
		firstComponent := strings.Split(rel, string(os.PathSeparator))[0]
		if strings.HasPrefix(firstComponent, ".incoming-") || firstComponent == "current" || firstComponent == "live" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
	type dirTime struct {
		path string
		time time.Time
	}
	var dirTimes []dirTime
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
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
			if err := os.MkdirAll(target, sharedDirPerm); err != nil {
				return err
			}
			dirTimes = append(dirTimes, dirTime{path: target, time: header.ModTime})
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), sharedDirPerm); err != nil {
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
			if err := os.Chmod(target, os.FileMode(header.Mode)|sharedFilePermMask); err != nil {
				return err
			}
			_ = os.Chtimes(target, header.ModTime, header.ModTime)
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), sharedDirPerm); err != nil {
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

	// Directories get their mtime updated when we extract files within them. Restore
	// directory mtimes after extraction so repeated bundle->extract cycles don't
	// cause a different checksum when the content is unchanged.
	sort.Slice(dirTimes, func(i, j int) bool {
		return len(dirTimes[i].path) > len(dirTimes[j].path)
	})
	for _, dt := range dirTimes {
		_ = os.Chtimes(dt.path, dt.time, dt.time)
	}
	return nil
}

func enforceGroupWritableTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		switch {
		case mode.IsDir():
			desired := mode.Perm() | sharedFilePermMask | 0o2000
			if mode.Perm() == desired&0o777 && mode&os.ModeSetgid != 0 {
				return nil
			}
			return os.Chmod(path, desired)
		case mode.IsRegular():
			desired := mode.Perm() | sharedFilePermMask
			if mode.Perm() == desired {
				return nil
			}
			return os.Chmod(path, desired)
		default:
			return nil
		}
	})
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	manifest, err := parseLatestManifest(body)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	return manifest, true, nil
}

func (c *sharedMountClient) latestWait(ctx context.Context, ownerID, mount, ifNoneMatch string, waitSeconds int) (sharedmounts.LatestManifest, bool, error) {
	endpoint := c.endpoint(ownerID, mount, "latest")
	if waitSeconds > 0 || strings.TrimSpace(ifNoneMatch) != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return sharedmounts.LatestManifest{}, false, err
		}
		query := parsed.Query()
		if waitSeconds > 0 {
			query.Set("waitSeconds", strconv.Itoa(waitSeconds))
		}
		if strings.TrimSpace(ifNoneMatch) != "" {
			query.Set("ifNoneMatchRevision", strings.Trim(ifNoneMatch, "\""))
		}
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
	}

	// The API enforces the wait timeout, but still ensure the request can't hang
	// indefinitely in case of networking issues.
	reqCtx := ctx
	if waitSeconds > 0 {
		timeout := time.Duration(waitSeconds+15) * time.Second
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	c.applyAuth(req)
	if strings.TrimSpace(ifNoneMatch) != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return sharedmounts.LatestManifest{}, false, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return sharedmounts.LatestManifest{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return sharedmounts.LatestManifest{}, false, fmt.Errorf("latest fetch failed: %s", strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	manifest, err := parseLatestManifest(body)
	if err != nil {
		return sharedmounts.LatestManifest{}, false, err
	}
	return manifest, true, nil
}

func parseLatestManifest(body []byte) (sharedmounts.LatestManifest, error) {
	type latestEnvelope struct {
		Data *sharedmounts.LatestManifest `json:"data"`
	}
	var payload latestEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return sharedmounts.LatestManifest{}, err
	}
	if payload.Data == nil {
		return sharedmounts.LatestManifest{}, fmt.Errorf("latest decode failed: missing data payload")
	}
	manifest := *payload.Data
	if manifest.Revision == "" || manifest.Checksum == "" {
		return sharedmounts.LatestManifest{}, fmt.Errorf("latest decode failed: missing revision/checksum")
	}
	return manifest, nil
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
