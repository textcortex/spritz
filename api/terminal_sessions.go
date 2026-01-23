package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	spritzv1 "spritz.sh/operator/api/v1"
)

type terminalSessionsResponse struct {
	Mode           string   `json:"mode"`
	Available      bool     `json:"available"`
	DefaultSession string   `json:"default_session,omitempty"`
	Sessions       []string `json:"sessions,omitempty"`
}

func (s *server) listTerminalSessions(c echo.Context) error {
	if !s.terminal.enabled {
		return writeError(c, http.StatusNotFound, "terminal disabled")
	}

	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}

	namespace := s.namespace
	if namespace == "" {
		namespace = c.QueryParam("namespace")
	}
	if namespace == "" {
		namespace = "default"
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), clientKey(namespace, name), spritz); err != nil {
		log.Printf("spritz terminal sessions: spritz not found name=%s namespace=%s user_id=%s err=%v", name, namespace, principal.ID, err)
		return writeError(c, http.StatusNotFound, "spritz not found")
	}

	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
		log.Printf("spritz terminal sessions: owner mismatch name=%s namespace=%s user_id=%s owner_id=%s", name, namespace, principal.ID, spritz.Spec.Owner.ID)
		return writeError(c, http.StatusForbidden, "owner mismatch")
	}

	pod, err := s.findRunningPod(c.Request().Context(), namespace, name, s.terminal.containerName)
	if err != nil {
		log.Printf("spritz terminal sessions: pod not ready name=%s namespace=%s user_id=%s err=%v", name, namespace, principal.ID, err)
		return writeError(c, http.StatusConflict, "spritz not ready")
	}

	response := terminalSessionsResponse{
		Mode:           string(s.terminal.sessionMode),
		Available:      false,
		DefaultSession: terminalDefaultSession(namespace, name),
	}

	if s.terminal.sessionMode != terminalSessionZmx {
		response.Mode = string(terminalSessionNone)
		return writeJSendSuccess(c, http.StatusOK, response)
	}

	ctx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
	defer cancel()
	available, err := s.zmxAvailable(ctx, pod)
	if err != nil {
		log.Printf("spritz terminal sessions: zmx check failed name=%s namespace=%s err=%v", name, namespace, err)
		return writeError(c, http.StatusInternalServerError, "failed to check terminal sessions")
	}
	response.Available = available
	if !available {
		return writeJSendSuccess(c, http.StatusOK, response)
	}

	sessions, err := s.listZmxSessions(ctx, pod)
	if err != nil {
		log.Printf("spritz terminal sessions: list failed name=%s namespace=%s err=%v", name, namespace, err)
		return writeError(c, http.StatusInternalServerError, "failed to list terminal sessions")
	}
	response.Sessions = sessions
	return writeJSendSuccess(c, http.StatusOK, response)
}
