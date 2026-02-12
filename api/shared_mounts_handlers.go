package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
	"spritz.sh/operator/sharedmounts"
)

type sharedMountError struct {
	status  int
	message string
}

func (e sharedMountError) Error() string {
	return e.message
}

func (s *server) requireSharedMount(c echo.Context) (string, string, error) {
	if !s.sharedMounts.enabled || s.sharedMountsStore == nil {
		return "", "", sharedMountError{status: http.StatusNotFound, message: "shared mounts disabled"}
	}
	ownerID := strings.TrimSpace(c.Param("owner"))
	if err := sharedmounts.ValidateScopeID(ownerID); err != nil {
		return "", "", sharedMountError{status: http.StatusBadRequest, message: err.Error()}
	}
	mountName := strings.TrimSpace(c.Param("mount"))
	if err := sharedmounts.ValidateName(mountName); err != nil {
		return "", "", sharedMountError{status: http.StatusBadRequest, message: err.Error()}
	}
	if mount, ok := s.sharedMounts.mounts[mountName]; ok {
		if mount.Scope != sharedmounts.ScopeOwner {
			return "", "", sharedMountError{status: http.StatusBadRequest, message: "unsupported shared mount scope"}
		}
		return ownerID, mountName, nil
	}
	allowed, err := s.ownerHasMount(c.Request().Context(), ownerID, mountName)
	if err != nil {
		return "", "", sharedMountError{status: http.StatusInternalServerError, message: "failed to resolve shared mounts"}
	}
	if !allowed {
		return "", "", sharedMountError{status: http.StatusNotFound, message: "shared mount not found"}
	}
	return ownerID, mountName, nil
}

func (s *server) getSharedMountLatest(c echo.Context) error {
	ownerID, mountName, err := s.requireSharedMount(c)
	if err != nil {
		return writeSharedMountError(c, err)
	}

	waitSeconds := parseSharedMountWaitSeconds(c)
	if waitSeconds <= 0 || s.sharedMountsLive == nil {
		manifest, err := s.fetchSharedMountLatest(c.Request().Context(), ownerID, mountName)
		if err != nil {
			if errors.Is(err, errSharedMountNotFound) {
				return writeError(c, http.StatusNotFound, "not found")
			}
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		return writeJSON(c, http.StatusOK, manifest)
	}

	expected := strings.TrimSpace(c.QueryParam("ifNoneMatchRevision"))
	if expected == "" {
		expected = strings.TrimSpace(c.Request().Header.Get("If-None-Match"))
	}
	expected = strings.Trim(expected, "\"")

	key := sharedMountLatestKey(ownerID, mountName)
	ch := s.sharedMountsLive.subscribe(key)
	defer s.sharedMountsLive.unsubscribe(key, ch)

	manifest, fetchErr := s.fetchSharedMountLatest(c.Request().Context(), ownerID, mountName)
	found := fetchErr == nil
	if fetchErr != nil && !errors.Is(fetchErr, errSharedMountNotFound) {
		return writeError(c, http.StatusInternalServerError, fetchErr.Error())
	}
	if found && (expected == "" || manifest.Revision != expected) {
		return writeJSON(c, http.StatusOK, manifest)
	}
	// Client expects a revision but the mount has never been published.
	if !found && expected != "" && expected != "*" {
		return writeError(c, http.StatusNotFound, "not found")
	}

	waitCtx, cancel := context.WithTimeout(c.Request().Context(), time.Duration(waitSeconds)*time.Second)
	defer cancel()

	select {
	case <-waitCtx.Done():
		if found {
			return c.NoContent(http.StatusNotModified)
		}
		return writeError(c, http.StatusNotFound, "not found")
	case <-ch:
		latest, err := s.fetchSharedMountLatest(c.Request().Context(), ownerID, mountName)
		if err != nil {
			if errors.Is(err, errSharedMountNotFound) {
				return writeError(c, http.StatusNotFound, "not found")
			}
			return writeError(c, http.StatusInternalServerError, err.Error())
		}
		if expected != "" && latest.Revision == expected {
			return c.NoContent(http.StatusNotModified)
		}
		return writeJSON(c, http.StatusOK, latest)
	}
}

func (s *server) getSharedMountRevision(c echo.Context) error {
	ownerID, mountName, err := s.requireSharedMount(c)
	if err != nil {
		return writeSharedMountError(c, err)
	}
	revision := strings.TrimSpace(c.Param("revision"))
	if err := sharedmounts.ValidateRevision(revision); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	objectPath := s.sharedMountsStore.revisionPath(ownerID, mountName, revision)
	c.Response().Header().Set("Content-Type", "application/gzip")
	if err := s.sharedMountsStore.streamObject(c.Request().Context(), objectPath, c.Response().Writer); err != nil {
		if errors.Is(err, errSharedMountNotFound) {
			return writeError(c, http.StatusNotFound, "not found")
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return nil
}

func (s *server) putSharedMountRevision(c echo.Context) error {
	ownerID, mountName, err := s.requireSharedMount(c)
	if err != nil {
		return writeSharedMountError(c, err)
	}
	revision := strings.TrimSpace(c.Param("revision"))
	if err := sharedmounts.ValidateRevision(revision); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if s.sharedMounts.maxBundleBytes > 0 {
		if c.Request().ContentLength <= 0 {
			return writeError(c, http.StatusLengthRequired, "content-length required")
		}
		if c.Request().ContentLength > s.sharedMounts.maxBundleBytes {
			return writeError(c, http.StatusRequestEntityTooLarge, "bundle exceeds max size")
		}
	}
	objectPath := s.sharedMountsStore.revisionPath(ownerID, mountName, revision)
	if err := s.sharedMountsStore.writeObject(c.Request().Context(), objectPath, c.Request().Body); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	return writeJSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) putSharedMountLatest(c echo.Context) error {
	ownerID, mountName, err := s.requireSharedMount(c)
	if err != nil {
		return writeSharedMountError(c, err)
	}
	var manifest sharedmounts.LatestManifest
	if err := c.Bind(&manifest); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	if err := validateLatestManifest(manifest); err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}
	if err := s.ensureLatestMatch(c.Request().Context(), ownerID, mountName, c.Request()); err != nil {
		return writeSharedMountError(c, err)
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	objectPath := s.sharedMountsStore.latestPath(ownerID, mountName)
	if err := s.sharedMountsStore.writeObject(c.Request().Context(), objectPath, bytes.NewReader(payload)); err != nil {
		return writeError(c, http.StatusInternalServerError, err.Error())
	}
	if s.sharedMountsLive != nil {
		s.sharedMountsLive.notify(sharedMountLatestKey(ownerID, mountName))
	}
	return writeJSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) ownerHasMount(ctx context.Context, ownerID, mountName string) (bool, error) {
	list := &spritzv1.SpritzList{}
	opts := []client.ListOption{
		client.MatchingLabels{ownerLabelKey: ownerLabelValue(ownerID)},
	}
	if s.namespace != "" {
		opts = append(opts, client.InNamespace(s.namespace))
	}
	if err := s.client.List(ctx, list, opts...); err != nil {
		return false, err
	}
	for _, item := range list.Items {
		for _, mount := range item.Spec.SharedMounts {
			if strings.TrimSpace(mount.Name) == mountName {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *server) fetchSharedMountLatest(ctx context.Context, ownerID, mountName string) (sharedmounts.LatestManifest, error) {
	objectPath := s.sharedMountsStore.latestPath(ownerID, mountName)
	data, err := s.sharedMountsStore.readObject(ctx, objectPath)
	if err != nil {
		return sharedmounts.LatestManifest{}, err
	}
	// Treat an empty/whitespace manifest as "not found" so new mounts can
	// bootstrap without requiring an initial publish.
	if len(bytes.TrimSpace(data)) == 0 {
		return sharedmounts.LatestManifest{}, errSharedMountNotFound
	}
	var manifest sharedmounts.LatestManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return sharedmounts.LatestManifest{}, err
	}
	if err := validateLatestManifest(manifest); err != nil {
		return sharedmounts.LatestManifest{}, err
	}
	return manifest, nil
}

func (s *server) ensureLatestMatch(ctx context.Context, ownerID, mountName string, req *http.Request) error {
	expected := strings.TrimSpace(req.URL.Query().Get("ifMatchRevision"))
	if expected == "" {
		expected = strings.TrimSpace(req.Header.Get("If-Match"))
	}
	expected = strings.Trim(expected, "\"")
	current, err := s.fetchSharedMountLatest(ctx, ownerID, mountName)
	if expected == "" {
		if errors.Is(err, errSharedMountNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return sharedMountError{status: http.StatusConflict, message: "if-match required"}
	}
	if expected == "*" {
		if errors.Is(err, errSharedMountNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		return sharedMountError{status: http.StatusConflict, message: "revision mismatch"}
	}
	if err != nil {
		if errors.Is(err, errSharedMountNotFound) {
			return sharedMountError{status: http.StatusConflict, message: "revision mismatch"}
		}
		return err
	}
	if current.Revision != expected {
		return sharedMountError{status: http.StatusConflict, message: "revision mismatch"}
	}
	return nil
}

func validateLatestManifest(manifest sharedmounts.LatestManifest) error {
	if err := sharedmounts.ValidateRevision(manifest.Revision); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.Checksum) == "" {
		return errors.New("checksum is required")
	}
	if err := sharedmounts.ValidateUpdatedAt(manifest.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func writeSharedMountError(c echo.Context, err error) error {
	if err == nil {
		return nil
	}
	switch mountErr := err.(type) {
	case sharedMountError:
		return writeError(c, mountErr.status, mountErr.message)
	case *sharedMountError:
		return writeError(c, mountErr.status, mountErr.message)
	}
	return writeError(c, http.StatusInternalServerError, err.Error())
}

func sharedMountLatestKey(ownerID, mountName string) string {
	return ownerID + "\x00" + mountName
}

func parseSharedMountWaitSeconds(c echo.Context) int {
	raw := strings.TrimSpace(c.QueryParam("waitSeconds"))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	// Avoid pinning connections forever.
	if value > 120 {
		return 120
	}
	return value
}
