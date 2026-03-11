package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestReadTerminalInputInvokesActivityCallbackOnInput(t *testing.T) {
	upgrader := websocket.Upgrader{}
	serverConn := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade failed: %v", err)
		}
		serverConn <- conn
	}))
	defer srv.Close()

	wsURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("failed to parse server url: %v", err)
	}
	wsURL.Scheme = "ws"
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer clientConn.Close()

	conn := <-serverConn
	defer conn.Close()

	reader, writer := io.Pipe()
	defer reader.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var callbacks atomic.Int32
	done := make(chan error, 1)
	go func() {
		done <- readTerminalInput(ctx, conn, writer, newTerminalSizeQueue(), func() {
			callbacks.Add(1)
		})
	}()

	if err := clientConn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		t.Fatalf("failed to send resize message: %v", err)
	}
	if callbacks.Load() != 0 {
		t.Fatalf("expected resize message to skip activity callback, got %d", callbacks.Load())
	}

	if err := clientConn.WriteMessage(websocket.TextMessage, []byte("ls\n")); err != nil {
		t.Fatalf("failed to send terminal input: %v", err)
	}

	buf := make([]byte, 3)
	if _, err := io.ReadFull(reader, buf); err != nil {
		t.Fatalf("failed to read stdin payload: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for callbacks.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("expected one activity callback for terminal input, got %d", callbacks.Load())
	}

	cancel()
	_ = clientConn.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for terminal reader to exit")
	}
}

func TestDebounceTerminalActivityCoalescesRapidInput(t *testing.T) {
	var callbacks atomic.Int32
	report := debounceTerminalActivity(50*time.Millisecond, func() {
		callbacks.Add(1)
	})

	report()
	report()
	report()
	if callbacks.Load() != 1 {
		t.Fatalf("expected rapid calls to coalesce into one activity write, got %d", callbacks.Load())
	}

	time.Sleep(60 * time.Millisecond)
	report()
	if callbacks.Load() != 2 {
		t.Fatalf("expected a second activity write after debounce window, got %d", callbacks.Load())
	}
}
