package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestConfigFromEnvBuildsOpenClawArgs(t *testing.T) {
	t.Setenv("SPRITZ_OPENCLAW_ACP_GATEWAY_URL", "ws://127.0.0.1:8080")
	t.Setenv("SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE", "/tmp/token")
	t.Setenv("SPRITZ_OPENCLAW_ACP_VERBOSE", "true")
	t.Setenv("SPRITZ_OPENCLAW_ACP_DEFAULT_SESSION", "agent:main:main")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv() error = %v", err)
	}
	if cfg.ListenAddr != defaultListenAddr {
		t.Fatalf("ListenAddr = %q, want %q", cfg.ListenAddr, defaultListenAddr)
	}
	if cfg.Path != defaultPath {
		t.Fatalf("Path = %q, want %q", cfg.Path, defaultPath)
	}
	joined := strings.Join(cfg.Args, " ")
	for _, part := range []string{"acp", "--url", "ws://127.0.0.1:8080", "--token-file", "/tmp/token", "--verbose", "--session", "agent:main:main"} {
		if !strings.Contains(joined, part) {
			t.Fatalf("expected %q in args: %s", part, joined)
		}
	}
	if len(cfg.Env) != 0 {
		t.Fatalf("Env = %#v, want empty", cfg.Env)
	}
}

func TestConfigFromEnvEnablesPrivateWSOverride(t *testing.T) {
	t.Setenv("SPRITZ_OPENCLAW_ACP_GATEWAY_URL", "ws://10.244.0.25:8080")
	t.Setenv("SPRITZ_OPENCLAW_ACP_ALLOW_INSECURE_PRIVATE_WS", "true")

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv() error = %v", err)
	}
	if len(cfg.Env) != 1 || cfg.Env[0] != "OPENCLAW_ALLOW_INSECURE_PRIVATE_WS=1" {
		t.Fatalf("Env = %#v, want OPENCLAW_ALLOW_INSECURE_PRIVATE_WS=1", cfg.Env)
	}
}

func TestConfigFromEnvParsesGatewayHeadersJSON(t *testing.T) {
	t.Setenv("SPRITZ_OPENCLAW_ACP_GATEWAY_URL", "ws://127.0.0.1:8080")
	t.Setenv(
		"SPRITZ_OPENCLAW_ACP_GATEWAY_HEADERS_JSON",
		`{"x-forwarded-user":"spritz-acp-bridge","x-forwarded-email":"spritz-acp-bridge@example.invalid"}`,
	)

	cfg, err := configFromEnv()
	if err != nil {
		t.Fatalf("configFromEnv() error = %v", err)
	}
	if got := cfg.GatewayHeaders.Get("x-forwarded-user"); got != "spritz-acp-bridge" {
		t.Fatalf("x-forwarded-user = %q, want %q", got, "spritz-acp-bridge")
	}
	if got := cfg.GatewayHeaders.Get("x-forwarded-email"); got != "spritz-acp-bridge@example.invalid" {
		t.Fatalf("x-forwarded-email = %q, want %q", got, "spritz-acp-bridge@example.invalid")
	}
	if !cfg.TrustedProxyControlUI {
		t.Fatalf("TrustedProxyControlUI = %v, want true", cfg.TrustedProxyControlUI)
	}
}

func TestGatewayProxyInjectsHeadersAndProxiesFrames(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstreamSeen := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamSeen <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade upstream: %v", err)
		}
		defer func() {
			_ = conn.Close()
		}()
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read upstream: %v", err)
		}
		if err := conn.WriteMessage(messageType, payload); err != nil {
			t.Fatalf("write upstream: %v", err)
		}
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxyURL, shutdown, err := startGatewayProxy(
		ctx,
		"ws"+strings.TrimPrefix(upstream.URL, "http"),
		http.Header{
			"X-Forwarded-User":  []string{"spritz-acp-bridge"},
			"X-Forwarded-Email": []string{"spritz-acp-bridge@example.invalid"},
		},
		false,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatalf("startGatewayProxy() error = %v", err)
	}
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial(proxyURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0"}`)); err != nil {
		t.Fatalf("write proxy: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read proxy: %v", err)
	}
	if string(payload) != `{"jsonrpc":"2.0"}` {
		t.Fatalf("payload = %q, want echo", string(payload))
	}

	headers := <-upstreamSeen
	if got := headers.Get("X-Forwarded-User"); got != "spritz-acp-bridge" {
		t.Fatalf("X-Forwarded-User = %q, want %q", got, "spritz-acp-bridge")
	}
	if got := headers.Get("X-Forwarded-Email"); got != "spritz-acp-bridge@example.invalid" {
		t.Fatalf("X-Forwarded-Email = %q, want %q", got, "spritz-acp-bridge@example.invalid")
	}
}

func TestGatewayProxyTrustedProxyRewritesConnectHandshake(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(_ *http.Request) bool { return true },
	}
	upstreamSeenHeaders := make(chan http.Header, 1)
	upstreamSeenPayload := make(chan map[string]any, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamSeenHeaders <- r.Header.Clone()
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade upstream: %v", err)
		}
		defer func() {
			_ = conn.Close()
		}()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read upstream: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatalf("decode upstream payload: %v", err)
		}
		upstreamSeenPayload <- decoded
	}))
	defer upstream.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proxyURL, shutdown, err := startGatewayProxy(
		ctx,
		"ws"+strings.TrimPrefix(upstream.URL, "http"),
		http.Header{
			"X-Forwarded-User":  []string{"spritz-acp-bridge"},
			"X-Forwarded-Email": []string{"spritz-acp-bridge@example.invalid"},
			"X-Forwarded-Proto": []string{"https"},
			"X-Forwarded-Host":  []string{"localhost"},
		},
		true,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatalf("startGatewayProxy() error = %v", err)
	}
	defer shutdown()

	conn, _, err := websocket.DefaultDialer.Dial(proxyURL, nil)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	request := map[string]any{
		"type":   "req",
		"id":     "connect-1",
		"method": "connect",
		"params": map[string]any{
			"role": "operator",
			"client": map[string]any{
				"id":          "cli",
				"displayName": "ACP",
				"version":     "2026.3.8",
				"platform":    "linux",
				"mode":        "cli",
			},
			"auth": map[string]any{
				"token":    "secret-token",
				"password": "secret-password",
			},
			"device": map[string]any{
				"id":        "device-1",
				"publicKey": "pub",
				"signature": "sig",
				"signedAt":  1,
				"nonce":     "nonce-1",
			},
		},
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("write proxy: %v", err)
	}

	headers := <-upstreamSeenHeaders
	if got := headers.Get("Origin"); got != "https://localhost" {
		t.Fatalf("Origin = %q, want %q", got, "https://localhost")
	}

	payload := <-upstreamSeenPayload
	params, ok := payload["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v, want object", payload["params"])
	}
	client, ok := params["client"].(map[string]any)
	if !ok {
		t.Fatalf("client = %#v, want object", params["client"])
	}
	if client["id"] != "openclaw-control-ui" {
		t.Fatalf("client.id = %#v, want %q", client["id"], "openclaw-control-ui")
	}
	if client["mode"] != "webchat" {
		t.Fatalf("client.mode = %#v, want %q", client["mode"], "webchat")
	}
	if _, exists := params["auth"]; exists {
		t.Fatalf("auth = %#v, want omitted", params["auth"])
	}
	if _, exists := params["device"]; exists {
		t.Fatalf("device = %#v, want omitted", params["device"])
	}
}

func TestBridgeProxiesWebSocketFramesToChildProcess(t *testing.T) {
	cfg := bridgeConfig{
		ListenAddr: "127.0.0.1:0",
		Path:       "/",
		Command:    os.Args[0],
		Args:       []string{"-test.run=TestHelperProcess", "--"},
		Env:        []string{"GO_WANT_HELPER_PROCESS=1"},
	}

	server := httptest.NewServer(newHandler(cfg, log.New(io.Discard, "", 0)))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	message := map[string]any{
		"jsonrpc": "2.0",
		"id":      "spritz-initialize",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": 1,
		},
	}
	if err := conn.WriteJSON(message); err != nil {
		t.Fatalf("write initialize: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}

	var response map[string]any
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if response["id"] != "spritz-initialize" {
		t.Fatalf("response id = %#v, want %q", response["id"], "spritz-initialize")
	}
	result, _ := response["result"].(map[string]any)
	if result == nil || result["protocolVersion"] != float64(1) {
		t.Fatalf("unexpected response result: %#v", response["result"])
	}
}

func TestHandlerRejectsWrongPath(t *testing.T) {
	cfg := bridgeConfig{
		ListenAddr: "127.0.0.1:0",
		Path:       "/acp",
		Command:    os.Args[0],
		Args:       []string{"-test.run=TestHelperProcess", "--"},
		Env:        []string{"GO_WANT_HELPER_PROCESS=1"},
	}
	server := httptest.NewServer(newHandler(cfg, log.New(io.Discard, "", 0)))
	defer server.Close()

	res, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		var request map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &request); err != nil {
			_, _ = os.Stderr.WriteString(err.Error() + "\n")
			continue
		}
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      request["id"],
			"result": map[string]any{
				"protocolVersion": 1,
				"agentInfo": map[string]any{
					"name":  "openclaw",
					"title": "OpenClaw",
				},
			},
		}
		payload, _ := json.Marshal(response)
		_, _ = os.Stdout.Write(append(payload, '\n'))
	}
}
