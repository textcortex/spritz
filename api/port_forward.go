package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type portForwardConfig struct {
	enabled         bool
	containerName   string
	allowedOrigins  map[string]struct{}
	activityRefresh time.Duration
}

type portForwardControlMessage struct {
	Type string `json:"type"`
}

func newPortForwardConfig() portForwardConfig {
	return portForwardConfig{
		enabled:         parseBoolEnv("SPRITZ_PORT_FORWARD_ENABLED", true),
		containerName:   envOrDefault("SPRITZ_PORT_FORWARD_CONTAINER", "spritz"),
		allowedOrigins:  splitSet(os.Getenv("SPRITZ_PORT_FORWARD_ORIGINS")),
		activityRefresh: parseDurationEnv("SPRITZ_PORT_FORWARD_ACTIVITY_REFRESH", time.Minute),
	}
}

func (p portForwardConfig) allowOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if len(p.allowedOrigins) == 0 {
		if origin == "" {
			return false
		}
		parsed, err := url.Parse(origin)
		if err != nil {
			return false
		}
		return strings.EqualFold(parsed.Host, r.Host)
	}
	if origin == "" {
		return false
	}
	return hasSetValue(p.allowedOrigins, origin)
}

func parsePortForwardQueryPort(c echo.Context) (uint32, error) {
	value := strings.TrimSpace(c.QueryParam("port"))
	if value == "" {
		return 0, fmt.Errorf("remote port required")
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid remote port")
	}
	return uint32(port), nil
}

func (s *server) openPortForward(c echo.Context) error {
	if !s.portForward.enabled {
		return writeError(c, http.StatusNotFound, "port forward disabled")
	}
	principal, err := requestPrincipal(c, s.auth)
	if err != nil {
		return writeAuthError(c, err)
	}
	if err := ensureAuthenticated(principal, s.auth.enabled()); err != nil {
		return writeAuthError(c, err)
	}

	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}
	remotePort, err := parsePortForwardQueryPort(c)
	if err != nil {
		return writeError(c, http.StatusBadRequest, err.Error())
	}

	namespace := s.requestNamespace(c)
	if namespace == "" {
		namespace = "default"
	}
	spritz, err := s.getAuthorizedSpritz(c.Request().Context(), principal, namespace, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return writeError(c, http.StatusNotFound, "spritz not found")
		}
		if errors.Is(err, errForbidden) {
			return writeForbidden(c)
		}
		return writeError(c, http.StatusInternalServerError, err.Error())
	}

	pod, err := s.findPortForwardPod(c.Request().Context(), namespace, name, s.portForward.containerName)
	if err != nil {
		log.Printf("spritz port-forward: pod not ready name=%s namespace=%s user_id=%s err=%v", name, namespace, principal.ID, err)
		return writeError(c, http.StatusConflict, "spritz not ready")
	}

	upgrader := websocket.Upgrader{CheckOrigin: s.portForward.allowOrigin}
	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close()
	}()

	ctx, cancel := context.WithCancel(c.Request().Context())
	defer cancel()
	s.startSpritzActivityLoop(ctx, spritz, s.portForward.activityRefresh, "port-forward")

	upstream, cleanup, err := s.openPodPortForward(ctx, pod, remotePort)
	if err != nil {
		log.Printf("spritz port-forward: open failed name=%s namespace=%s port=%d user_id=%s err=%v", name, namespace, remotePort, principal.ID, err)
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "port forward unavailable"), time.Now().Add(500*time.Millisecond))
		return nil
	}
	defer func() {
		_ = upstream.Close()
		_ = cleanup.Close()
	}()

	if err := proxyWebSocketNetConn(conn, upstream); err != nil {
		if errors.Is(err, context.Canceled) || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			return nil
		}
		return err
	}
	return nil
}

func proxyWebSocketNetConn(ws *websocket.Conn, upstream net.Conn) error {
	errCh := make(chan error, 2)
	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			_ = ws.Close()
			_ = upstream.Close()
		})
	}

	go func() {
		errCh <- copyWebSocketToNetConn(ws, upstream)
	}()
	go func() {
		errCh <- copyNetConnToWebSocket(upstream, ws)
	}()

	var firstErr error
	for completed := 0; completed < 2; completed++ {
		err := <-errCh
		if err == nil || errors.Is(err, io.EOF) || websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
			continue
		}
		if ne, ok := err.(net.Error); ok && ne.Timeout() {
			continue
		}
		if firstErr == nil {
			firstErr = err
			closeAll()
		}
	}
	closeAll()
	return firstErr
}

func (s *server) findPortForwardPod(ctx context.Context, namespace, name, container string) (*corev1.Pod, error) {
	if s.findRunningPodFunc != nil {
		return s.findRunningPodFunc(ctx, namespace, name, container)
	}
	return s.findRunningPod(ctx, namespace, name, container)
}

func copyWebSocketToNetConn(ws *websocket.Conn, upstream net.Conn) error {
	for {
		msgType, payload, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		if msgType == websocket.TextMessage {
			control, err := parsePortForwardControl(payload)
			if err != nil {
				return err
			}
			if control.Type == "eof" {
				if err := closeConnWrite(upstream); err != nil {
					return err
				}
				return nil
			}
			continue
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := upstream.Write(payload); err != nil {
			return err
		}
	}
}

func copyNetConnToWebSocket(upstream net.Conn, ws *websocket.Conn) error {
	buffer := make([]byte, 32*1024)
	for {
		n, err := upstream.Read(buffer)
		if n > 0 {
			if writeErr := ws.WriteMessage(websocket.BinaryMessage, buffer[:n]); writeErr != nil {
				return writeErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if writeErr := ws.WriteMessage(websocket.TextMessage, mustMarshalPortForwardControl(portForwardControlMessage{Type: "eof"})); writeErr != nil {
					return writeErr
				}
			}
			return err
		}
	}
}

func parsePortForwardControl(payload []byte) (portForwardControlMessage, error) {
	var message portForwardControlMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return portForwardControlMessage{}, fmt.Errorf("invalid port-forward control: %w", err)
	}
	return message, nil
}

func mustMarshalPortForwardControl(message portForwardControlMessage) []byte {
	payload, err := json.Marshal(message)
	if err != nil {
		panic(err)
	}
	return payload
}

func closeConnWrite(conn net.Conn) error {
	type closeWriter interface {
		CloseWrite() error
	}
	if writer, ok := conn.(closeWriter); ok {
		return writer.CloseWrite()
	}
	return conn.Close()
}
