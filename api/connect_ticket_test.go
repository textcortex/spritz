package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func decodeJSendSuccessData(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var response jsendResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("failed to decode jsend response: %v", err)
	}
	if response.Status != "success" {
		t.Fatalf("expected success response, got %q", response.Status)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected object data payload, got %T", response.Data)
	}
	return data
}

func TestCreateACPConnectTicketReturnsProtocolAndPath(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())

	s := newACPTestServer(t, spritz, conversation)
	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/connect-ticket", s.createACPConnectTicket)

	req := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/connect-ticket", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	req.Header.Set("Origin", "https://console.example.com")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	data := decodeJSendSuccessData(t, rec.Body.Bytes())
	if data["type"] != "connect-ticket" {
		t.Fatalf("expected type connect-ticket, got %#v", data["type"])
	}
	if data["protocol"] != "spritz-acp.v1" {
		t.Fatalf("expected ACP protocol, got %#v", data["protocol"])
	}
	if data["connectPath"] != "/api/acp/conversations/"+conversation.Name+"/connect" {
		t.Fatalf("unexpected connectPath %#v", data["connectPath"])
	}
	if strings.TrimSpace(data["ticket"].(string)) == "" {
		t.Fatalf("expected opaque ticket to be present")
	}
	if strings.TrimSpace(data["expiresAt"].(string)) == "" {
		t.Fatalf("expected expiresAt to be present")
	}
}

func TestOpenACPConversationConnectionAcceptsConnectTicketSubprotocol(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("failed to upgrade instance websocket: %v", err)
		}
		defer conn.Close()

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read instance websocket message: %v", err)
		}
		if err := conn.WriteMessage(msgType, []byte(strings.ToUpper(string(payload)))); err != nil {
			t.Fatalf("failed to write instance websocket message: %v", err)
		}
	}))
	defer instance.Close()

	s := newACPTestServer(t, spritz, conversation)
	s.acp.instanceURL = func(namespace, name string) string {
		return "ws" + strings.TrimPrefix(instance.URL, "http")
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/connect-ticket", s.createACPConnectTicket)
	e.GET("/api/acp/conversations/:id/connect", s.openACPConversationConnection)
	proxy := httptest.NewServer(e)
	defer proxy.Close()

	ticketReq := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/connect-ticket", nil)
	ticketReq.Header.Set("X-Spritz-User-Id", "user-1")
	ticketReq.Header.Set("Origin", proxy.URL)
	ticketRec := httptest.NewRecorder()
	e.ServeHTTP(ticketRec, ticketReq)
	if ticketRec.Code != http.StatusOK {
		t.Fatalf("expected ticket issuance to succeed, got %d: %s", ticketRec.Code, ticketRec.Body.String())
	}

	data := decodeJSendSuccessData(t, ticketRec.Body.Bytes())
	ticket := data["ticket"].(string)
	protocol := data["protocol"].(string)
	connectPath := data["connectPath"].(string)

	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + connectPath
	dialer := websocket.Dialer{
		Subprotocols: []string{protocol, "spritz-ticket.v1." + ticket},
	}
	headers := http.Header{}
	headers.Set("Origin", proxy.URL)
	conn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("expected websocket dial to succeed, got %v", err)
	}
	defer conn.Close()

	if conn.Subprotocol() != protocol {
		t.Fatalf("expected websocket subprotocol %q, got %q", protocol, conn.Subprotocol())
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatalf("failed to write websocket message: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read websocket message: %v", err)
	}
	if string(payload) != "PING" {
		t.Fatalf("expected websocket echo PING, got %q", string(payload))
	}
}

func TestCreateTerminalConnectTicketIncludesSessionInConnectPath(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")

	s := newACPTestServer(t, spritz)
	s.terminal = terminalConfig{enabled: true}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/spritzes/:name/terminal/connect-ticket", s.createTerminalConnectTicket)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/spritzes/"+spritz.Name+"/terminal/connect-ticket",
		strings.NewReader(`{"session":"shared-shell"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Spritz-User-Id", "user-1")
	req.Header.Set("Origin", "https://console.example.com")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	data := decodeJSendSuccessData(t, rec.Body.Bytes())
	if data["protocol"] != "spritz-terminal.v1" {
		t.Fatalf("expected terminal protocol, got %#v", data["protocol"])
	}
	if data["connectPath"] != "/api/spritzes/"+spritz.Name+"/terminal?session=shared-shell" {
		t.Fatalf("unexpected terminal connectPath %#v", data["connectPath"])
	}
}

func TestOpenACPConversationConnectionRejectsReusedConnectTicket(t *testing.T) {
	spritz := readyACPSpritz("tidy-otter", "user-1")
	conversation := conversationFor("tidy-otter-conv", "tidy-otter", "user-1", "Latest", metav1.Now())

	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	instance := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("failed to upgrade instance websocket: %v", err)
		}
		defer conn.Close()
		for {
			msgType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(msgType, payload); err != nil {
				t.Fatalf("failed to write instance websocket message: %v", err)
			}
		}
	}))
	defer instance.Close()

	s := newACPTestServer(t, spritz, conversation)
	s.acp.instanceURL = func(namespace, name string) string {
		return "ws" + strings.TrimPrefix(instance.URL, "http")
	}

	e := echo.New()
	secured := e.Group("", s.authMiddleware())
	secured.POST("/api/acp/conversations/:id/connect-ticket", s.createACPConnectTicket)
	e.GET("/api/acp/conversations/:id/connect", s.openACPConversationConnection)
	proxy := httptest.NewServer(e)
	defer proxy.Close()

	ticketReq := httptest.NewRequest(http.MethodPost, "/api/acp/conversations/"+conversation.Name+"/connect-ticket", nil)
	ticketReq.Header.Set("X-Spritz-User-Id", "user-1")
	ticketReq.Header.Set("Origin", proxy.URL)
	ticketRec := httptest.NewRecorder()
	e.ServeHTTP(ticketRec, ticketReq)
	if ticketRec.Code != http.StatusOK {
		t.Fatalf("expected ticket issuance to succeed, got %d: %s", ticketRec.Code, ticketRec.Body.String())
	}

	data := decodeJSendSuccessData(t, ticketRec.Body.Bytes())
	ticket := data["ticket"].(string)
	protocol := data["protocol"].(string)
	connectPath := data["connectPath"].(string)
	wsURL := "ws" + strings.TrimPrefix(proxy.URL, "http") + connectPath
	headers := http.Header{}
	headers.Set("Origin", proxy.URL)

	dialer := websocket.Dialer{
		Subprotocols: []string{protocol, "spritz-ticket.v1." + ticket},
	}
	firstConn, _, err := dialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("expected first websocket dial to succeed, got %v", err)
	}
	_ = firstConn.Close()

	if _, _, err := dialer.Dial(wsURL, headers); err == nil {
		t.Fatalf("expected reused connect ticket to be rejected")
	}
}
