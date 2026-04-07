package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

const portForwardEOFControl = `{"type":"eof"}`

func TestOpenPortForwardRejectsInvalidRemotePort(t *testing.T) {
	s := newCreateSpritzTestServer(t)
	s.portForward = portForwardConfig{enabled: true, containerName: "spritz"}
	e := echo.New()
	e.GET("/api/spritzes/:name/port-forward", s.openPortForward)

	req := httptest.NewRequest(http.MethodGet, "/api/spritzes/devbox1/port-forward?port=99999", nil)
	req.Header.Set("X-Spritz-User-Id", "user-1")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid port, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid remote port") {
		t.Fatalf("expected invalid remote port error, got %q", rec.Body.String())
	}
}

func TestOpenPortForwardProxiesToInjectedUpstream(t *testing.T) {
	scheme := newTestSpritzScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tidal-falcon",
			Namespace: "spritz-test",
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
	}

	var activityCalls atomic.Int32
	var forwardedPort atomic.Int32
	s := &server{
		client: ctrlclientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(spritz).
			Build(),
		scheme:    scheme,
		namespace: "spritz-test",
		auth: authConfig{
			mode:              authModeHeader,
			headerID:          "X-Spritz-User-Id",
			headerDefaultType: principalTypeHuman,
		},
		internalAuth: internalAuthConfig{enabled: false},
		portForward:  portForwardConfig{enabled: true, containerName: "spritz"},
		activityRecorder: func(ctx context.Context, namespace, name string, when time.Time) error {
			activityCalls.Add(1)
			return nil
		},
		findRunningPodFunc: func(ctx context.Context, namespace, name, container string) (*corev1.Pod, error) {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tidal-falcon-pod",
					Namespace: namespace,
				},
			}, nil
		},
		openPodPortForwardFunc: func(ctx context.Context, pod *corev1.Pod, remotePort uint32) (net.Conn, io.Closer, error) {
			forwardedPort.Store(int32(remotePort))
			clientConn, serverConn := net.Pipe()
			go func() {
				defer serverConn.Close()
				_, _ = io.Copy(serverConn, serverConn)
			}()
			return clientConn, closeFunc(func() error { return nil }), nil
		},
	}

	e := echo.New()
	e.GET("/api/spritzes/:name/port-forward", s.openPortForward)
	srv := httptest.NewServer(e)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/spritzes/tidal-falcon/port-forward?port=3000"
	headers := http.Header{}
	headers.Set("X-Spritz-User-Id", "user-1")
	headers.Set("Origin", srv.URL)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("ping")); err != nil {
		t.Fatalf("write websocket: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket: %v", err)
	}
	if string(payload) != "ping" {
		t.Fatalf("unexpected echoed payload %q", string(payload))
	}
	if got := forwardedPort.Load(); got != 3000 {
		t.Fatalf("forwarded remote port = %d, want 3000", got)
	}
	if activityCalls.Load() == 0 {
		t.Fatal("expected port forwarding to refresh activity")
	}
}

func TestOpenPortForwardPreservesEOFFramedExchange(t *testing.T) {
	scheme := newTestSpritzScheme(t)
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tidal-falcon",
			Namespace: "spritz-test",
		},
		Spec: spritzv1.SpritzSpec{
			Owner: spritzv1.SpritzOwner{ID: "user-1"},
		},
	}

	upstreamListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	defer upstreamListener.Close()

	upstreamDone := make(chan error, 1)
	go func() {
		conn, err := upstreamListener.Accept()
		if err != nil {
			upstreamDone <- err
			return
		}
		defer conn.Close()
		payload, err := io.ReadAll(conn)
		if err != nil {
			upstreamDone <- err
			return
		}
		if string(payload) != "ping" {
			upstreamDone <- io.ErrUnexpectedEOF
			return
		}
		if _, err := conn.Write([]byte("pong")); err != nil {
			upstreamDone <- err
			return
		}
		upstreamDone <- nil
	}()

	s := &server{
		client: ctrlclientfake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(spritz).
			Build(),
		scheme:    scheme,
		namespace: "spritz-test",
		auth: authConfig{
			mode:              authModeHeader,
			headerID:          "X-Spritz-User-Id",
			headerDefaultType: principalTypeHuman,
		},
		internalAuth: internalAuthConfig{enabled: false},
		portForward:  portForwardConfig{enabled: true, containerName: "spritz"},
		findRunningPodFunc: func(ctx context.Context, namespace, name, container string) (*corev1.Pod, error) {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "tidal-falcon-pod",
					Namespace: namespace,
				},
			}, nil
		},
		openPodPortForwardFunc: func(ctx context.Context, pod *corev1.Pod, remotePort uint32) (net.Conn, io.Closer, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", upstreamListener.Addr().String())
			if err != nil {
				return nil, nil, err
			}
			return conn, closeFunc(func() error { return nil }), nil
		},
	}

	e := echo.New()
	e.GET("/api/spritzes/:name/port-forward", s.openPortForward)
	srv := httptest.NewServer(e)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/spritzes/tidal-falcon/port-forward?port=3000"
	headers := http.Header{}
	headers.Set("X-Spritz-User-Id", "user-1")
	headers.Set("Origin", srv.URL)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("ping")); err != nil {
		t.Fatalf("write websocket payload: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(portForwardEOFControl)); err != nil {
		t.Fatalf("write websocket eof: %v", err)
	}

	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket response: %v", err)
	}
	if messageType != websocket.BinaryMessage || string(payload) != "pong" {
		t.Fatalf("unexpected websocket response type=%d payload=%q", messageType, string(payload))
	}

	messageType, payload, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket eof: %v", err)
	}
	if messageType != websocket.TextMessage || string(payload) != portForwardEOFControl {
		t.Fatalf("unexpected websocket eof type=%d payload=%q", messageType, string(payload))
	}

	if err := <-upstreamDone; err != nil {
		t.Fatalf("upstream exchange failed: %v", err)
	}
}
