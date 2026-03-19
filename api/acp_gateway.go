package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

func (s *server) openACPConversationConnection(c echo.Context) error {
	if !s.acp.enabled {
		return writeError(c, http.StatusNotFound, "acp disabled")
	}
	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if err := authorizeHumanOnly(principal, s.auth.enabled()); err != nil {
		return writeForbidden(c)
	}
	namespace := s.requestNamespace(c)
	conversation, err := s.getAuthorizedConversation(c.Request().Context(), principal, namespace, c.Param("id"))
	if err != nil {
		return s.writeACPConversationError(c, err)
	}
	if namespace == "" {
		namespace = "default"
	}

	spritz, err := s.getAuthorizedACPReadySpritz(c.Request().Context(), principal, namespace, conversation.Spec.SpritzName)
	if err != nil {
		return s.writeACPResourceError(c, err)
	}

	upgrader := websocket.Upgrader{CheckOrigin: s.acp.allowOrigin}
	browserConn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = browserConn.Close()
	}()

	instanceConn, _, err := websocket.DefaultDialer.DialContext(c.Request().Context(), s.acpInstanceURL(spritz.Namespace, spritz.Name), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = instanceConn.Close()
	}()

	return proxyWebSockets(
		browserConn,
		instanceConn,
		func(payload []byte) {
			s.scheduleACPPromptActivity(c.Logger(), spritz.Namespace, spritz.Name, payload)
		},
		nil,
	)
}

func proxyWebSockets(left, right *websocket.Conn, onLeftMessage, onRightMessage func([]byte)) error {
	errCh := make(chan error, 2)
	closeOnce := sync.Once{}
	closeBoth := func() {
		_ = left.Close()
		_ = right.Close()
	}

	go proxyWebSocketDirection(left, right, errCh, onLeftMessage)
	go proxyWebSocketDirection(right, left, errCh, onRightMessage)

	err := <-errCh
	closeOnce.Do(closeBoth)
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return nil
	}
	return err
}

func proxyWebSocketDirection(src, dst *websocket.Conn, errCh chan<- error, onMessage func([]byte)) {
	for {
		msgType, payload, err := src.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if onMessage != nil {
			onMessage(payload)
		}
		if err := dst.WriteMessage(msgType, payload); err != nil {
			errCh <- err
			return
		}
	}
}

func isACPPromptMessage(payload []byte) bool {
	var message struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return false
	}
	return strings.TrimSpace(message.Method) == "session/prompt"
}
