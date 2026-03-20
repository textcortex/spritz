package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestInstanceProxyStripsPrefixAndAuthorizesOwner(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload := map[string]any{
			"path":               r.URL.Path,
			"query":              r.URL.RawQuery,
			"forwardedHost":      r.Header.Get("X-Forwarded-Host"),
			"forwardedProto":     r.Header.Get("X-Forwarded-Proto"),
			"forwardedPrefix":    r.Header.Get("X-Forwarded-Prefix"),
			"authHeader":         r.Header.Get("Authorization"),
			"principalHeader":    r.Header.Get("X-Spritz-User-Id"),
			"principalEmail":     r.Header.Get("X-Spritz-User-Email"),
			"oauthRequestHeader": r.Header.Get("X-Auth-Request-User"),
		}
		_ = json.NewEncoder(w).Encode(payload)
	}))
	defer upstream.Close()

	s := newInstanceProxyTestServer(t, "owner-123", upstream.URL)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "https://spritz.example.com/i/openclaw-tide-wind/assets/app.js?theme=dark", nil)
	req.Header.Set("X-Spritz-User-Id", "owner-123")
	req.Header.Set("X-Spritz-User-Email", "owner@example.com")
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("X-Auth-Request-User", "owner-123")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	payload := map[string]string{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if payload["path"] != "/assets/app.js" {
		t.Fatalf("expected upstream path /assets/app.js, got %q", payload["path"])
	}
	if payload["query"] != "theme=dark" {
		t.Fatalf("expected query to be preserved, got %q", payload["query"])
	}
	if payload["forwardedHost"] != "spritz.example.com" {
		t.Fatalf("expected forwarded host spritz.example.com, got %q", payload["forwardedHost"])
	}
	if payload["forwardedProto"] != "https" {
		t.Fatalf("expected forwarded proto https, got %q", payload["forwardedProto"])
	}
	if payload["forwardedPrefix"] != "/i/openclaw-tide-wind" {
		t.Fatalf("expected forwarded prefix /i/openclaw-tide-wind, got %q", payload["forwardedPrefix"])
	}
	if payload["authHeader"] != "" {
		t.Fatalf("expected authorization header to be stripped, got %q", payload["authHeader"])
	}
	if payload["principalHeader"] != "" {
		t.Fatalf("expected principal id header to be stripped, got %q", payload["principalHeader"])
	}
	if payload["principalEmail"] != "" {
		t.Fatalf("expected principal email header to be stripped, got %q", payload["principalEmail"])
	}
	if payload["oauthRequestHeader"] != "" {
		t.Fatalf("expected oauth request header to be stripped, got %q", payload["oauthRequestHeader"])
	}
}

func TestInstanceProxyRejectsUnauthorizedOwner(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("upstream should not be called for unauthorized users")
	}))
	defer upstream.Close()

	s := newInstanceProxyTestServer(t, "owner-123", upstream.URL)
	e := echo.New()
	s.registerRoutes(e)

	req := httptest.NewRequest(http.MethodGet, "/i/openclaw-tide-wind", nil)
	req.Header.Set("X-Spritz-User-Id", "other-user")
	rec := httptest.NewRecorder()

	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestInstanceProxyWebsocketStripsPrefix(t *testing.T) {
	receivedPath := make(chan string, 1)
	receivedQuery := make(chan string, 1)
	receivedAuth := make(chan string, 1)
	receivedPrincipal := make(chan string, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath <- r.URL.Path
		receivedQuery <- r.URL.RawQuery
		receivedAuth <- r.Header.Get("Authorization")
		receivedPrincipal <- r.Header.Get("X-Spritz-User-Id")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("failed to upgrade upstream websocket: %v", err)
		}
		defer conn.Close()

		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read upstream message: %v", err)
		}
		if err := conn.WriteMessage(msgType, []byte(strings.ToUpper(string(payload)))); err != nil {
			t.Fatalf("failed to write upstream message: %v", err)
		}
	}))
	defer upstream.Close()

	s := newInstanceProxyTestServer(t, "owner-123", upstream.URL)
	e := echo.New()
	s.registerRoutes(e)
	proxy := httptest.NewServer(e)
	defer proxy.Close()

	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("failed to parse proxy url: %v", err)
	}
	wsURL := url.URL{
		Scheme:   "ws",
		Host:     proxyURL.Host,
		Path:     "/i/openclaw-tide-wind/socket",
		RawQuery: "stream=1",
	}
	headers := http.Header{}
	headers.Set("X-Spritz-User-Id", "owner-123")
	headers.Set("Authorization", "Bearer websocket-token")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), headers)
	if err != nil {
		t.Fatalf("failed to dial proxied websocket: %v", err)
	}
	defer conn.Close()

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
	if got := <-receivedPath; got != "/socket" {
		t.Fatalf("expected websocket upstream path /socket, got %q", got)
	}
	if got := <-receivedQuery; got != "stream=1" {
		t.Fatalf("expected websocket query stream=1, got %q", got)
	}
	if got := <-receivedAuth; got != "" {
		t.Fatalf("expected websocket authorization header stripped, got %q", got)
	}
	if got := <-receivedPrincipal; got != "" {
		t.Fatalf("expected websocket principal header stripped, got %q", got)
	}
}

func newInstanceProxyTestServer(t *testing.T, ownerID, upstream string) *server {
	t.Helper()

	scheme := newTestSpritzScheme(t)
	routeModel := spritzRouteModelFromEnv()
	base := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(&spritzv1.Spritz{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "openclaw-tide-wind",
				Namespace: "spritz-test",
			},
			Spec: spritzv1.SpritzSpec{
				Image: "example.com/spritz-openclaw:latest",
				Owner: spritzv1.SpritzOwner{ID: ownerID},
			},
		}).
		Build()

	targetURL, err := url.Parse(upstream)
	if err != nil {
		t.Fatalf("failed to parse upstream target: %v", err)
	}

	return &server{
		client:           base,
		scheme:           scheme,
		namespace:        "spritz-test",
		controlNamespace: "spritz-test",
		auth: authConfig{
			mode:              authModeHeader,
			headerID:          "X-Spritz-User-Id",
			headerEmail:       "X-Spritz-User-Email",
			headerType:        "X-Spritz-Principal-Type",
			headerScopes:      "X-Spritz-Principal-Scopes",
			headerDefaultType: principalTypeHuman,
		},
		internalAuth: internalAuthConfig{enabled: false},
		terminal:     terminalConfig{enabled: false},
		routeModel:   routeModel,
		instanceProxy: instanceProxyConfig{
			enabled:     true,
			stripPrefix: true,
		},
		instanceProxyTargetResolver: func(*spritzv1.Spritz) (*url.URL, error) {
			cloned := *targetURL
			return &cloned, nil
		},
		instanceProxyTransport: http.DefaultTransport,
	}
}
