package main

import (
	"net/http"
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

	workspaceConn, _, err := websocket.DefaultDialer.DialContext(c.Request().Context(), s.acpWorkspaceURL(spritz.Namespace, spritz.Name), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = workspaceConn.Close()
	}()

	return proxyWebSockets(browserConn, workspaceConn)
}

func proxyWebSockets(left, right *websocket.Conn) error {
	errCh := make(chan error, 2)
	closeOnce := sync.Once{}
	closeBoth := func() {
		_ = left.Close()
		_ = right.Close()
	}

	go proxyWebSocketDirection(left, right, errCh)
	go proxyWebSocketDirection(right, left, errCh)

	err := <-errCh
	closeOnce.Do(closeBoth)
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return nil
	}
	return err
}

func proxyWebSocketDirection(src, dst *websocket.Conn, errCh chan<- error) {
	for {
		msgType, payload, err := src.ReadMessage()
		if err != nil {
			errCh <- err
			return
		}
		if err := dst.WriteMessage(msgType, payload); err != nil {
			errCh <- err
			return
		}
	}
}
