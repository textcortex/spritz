package main

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

func (s *server) getInternalPreset(c echo.Context) error {
	// Return the rendered internal preset metadata for one preset id.
	presetID := sanitizeSpritzNameToken(c.Param("presetID"))
	if presetID == "" {
		return writeError(c, http.StatusBadRequest, "presetId is required")
	}
	preset, ok := s.presets.get(presetID)
	if !ok {
		return writeError(c, http.StatusNotFound, "not found")
	}
	preset.ID = strings.TrimSpace(preset.ID)
	return writeJSON(c, http.StatusOK, map[string]any{
		"preset": preset,
	})
}
