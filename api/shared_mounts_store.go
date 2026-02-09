package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"strings"

	"spritz.sh/operator/sharedmounts"
)

var errSharedMountNotFound = errors.New("shared mount object not found")

type sharedMountsStore struct {
	config sharedMountsConfig
}

func newSharedMountsStore(config sharedMountsConfig) *sharedMountsStore {
	return &sharedMountsStore{config: config}
}

func (s *sharedMountsStore) latestPath(ownerID, mount string) string {
	return path.Join(sharedmounts.StoragePrefix(s.config.prefix, "owner", ownerID, mount), "latest.json")
}

func (s *sharedMountsStore) revisionPath(ownerID, mount, revision string) string {
	file := fmt.Sprintf("%s.tar.gz", revision)
	return path.Join(sharedmounts.StoragePrefix(s.config.prefix, "owner", ownerID, mount), "revisions", file)
}

func (s *sharedMountsStore) remotePath(objectPath string) string {
	return fmt.Sprintf("%s:%s/%s", s.config.rcloneRemote, s.config.bucket, objectPath)
}

func (s *sharedMountsStore) readObject(ctx context.Context, objectPath string) ([]byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := s.rcloneArgs("cat", s.remotePath(objectPath))
	cmd := exec.CommandContext(ctx, "rclone", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if isRcloneNotFound(stderr.String()) {
			return nil, errSharedMountNotFound
		}
		return nil, fmt.Errorf("rclone cat failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func (s *sharedMountsStore) streamObject(ctx context.Context, objectPath string, out io.Writer) error {
	var stderr bytes.Buffer
	args := s.rcloneArgs("cat", s.remotePath(objectPath))
	cmd := exec.CommandContext(ctx, "rclone", args...)
	cmd.Stdout = out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if isRcloneNotFound(stderr.String()) {
			return errSharedMountNotFound
		}
		return fmt.Errorf("rclone cat failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *sharedMountsStore) writeObject(ctx context.Context, objectPath string, body io.Reader) error {
	var stderr bytes.Buffer
	args := s.rcloneArgs("rcat", s.remotePath(objectPath))
	cmd := exec.CommandContext(ctx, "rclone", args...)
	cmd.Stdin = body
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rclone rcat failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (s *sharedMountsStore) rcloneArgs(args ...string) []string {
	if s.config.rcloneConfigPath != "" {
		return append([]string{"--config", s.config.rcloneConfigPath}, args...)
	}
	return args
}

func isRcloneNotFound(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such object") ||
		strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "object not found")
}
