package main

import (
	"bufio"
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
