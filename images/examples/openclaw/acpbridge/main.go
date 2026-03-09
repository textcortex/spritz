package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultListenAddr      = "0.0.0.0:2529"
	defaultPath            = "/"
	defaultShutdownTimeout = 5 * time.Second
)

type bridgeConfig struct {
	ListenAddr string
	Path       string
	Command    string
	Args       []string
	Env        []string
}

type pumpResult struct {
	source string
	err    error
}

func main() {
	logger := log.New(os.Stderr, "spritz-openclaw-acp-bridge: ", log.LstdFlags|log.Lmsgprefix)
	cfg, err := configFromEnv()
	if err != nil {
		logger.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runServer(ctx, cfg, logger); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatal(err)
	}
}

func configFromEnv() (bridgeConfig, error) {
	gatewayURL := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_GATEWAY_URL"))
	if gatewayURL == "" {
		return bridgeConfig{}, errors.New("SPRITZ_OPENCLAW_ACP_GATEWAY_URL is required")
	}

	listenAddr := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}

	path := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_PATH"))
	if path == "" {
		path = defaultPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	command := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_COMMAND"))
	if command == "" {
		command = "openclaw"
	}

	args := []string{"acp", "--url", gatewayURL}
	if tokenFile := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_GATEWAY_TOKEN_FILE")); tokenFile != "" {
		args = append(args, "--token-file", tokenFile)
	}
	if passwordFile := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_GATEWAY_PASSWORD_FILE")); passwordFile != "" {
		args = append(args, "--password-file", passwordFile)
	}
	if provenance := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_PROVENANCE")); provenance != "" {
		args = append(args, "--provenance", provenance)
	}
	if sessionKey := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_DEFAULT_SESSION")); sessionKey != "" {
		args = append(args, "--session", sessionKey)
	}
	if sessionLabel := strings.TrimSpace(os.Getenv("SPRITZ_OPENCLAW_ACP_SESSION_LABEL")); sessionLabel != "" {
		args = append(args, "--session-label", sessionLabel)
	}
	if parseBoolEnv("SPRITZ_OPENCLAW_ACP_REQUIRE_EXISTING", false) {
		args = append(args, "--require-existing")
	}
	if parseBoolEnv("SPRITZ_OPENCLAW_ACP_RESET_SESSION", false) {
		args = append(args, "--reset-session")
	}
	if parseBoolEnv("SPRITZ_OPENCLAW_ACP_NO_PREFIX_CWD", false) {
		args = append(args, "--no-prefix-cwd")
	}
	if parseBoolEnv("SPRITZ_OPENCLAW_ACP_VERBOSE", false) {
		args = append(args, "--verbose")
	}

	extraEnv := []string{}
	if parseBoolEnv("SPRITZ_OPENCLAW_ACP_ALLOW_INSECURE_PRIVATE_WS", false) {
		extraEnv = append(extraEnv, "OPENCLAW_ALLOW_INSECURE_PRIVATE_WS=1")
	}

	return bridgeConfig{
		ListenAddr: listenAddr,
		Path:       path,
		Command:    command,
		Args:       args,
		Env:        extraEnv,
	}, nil
}

func parseBoolEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func runServer(ctx context.Context, cfg bridgeConfig, logger *log.Logger) error {
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	defer func() {
		_ = listener.Close()
	}()

	server := &http.Server{Handler: newHandler(cfg, logger)}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaultShutdownTimeout)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	logger.Printf("listening on %s%s", cfg.ListenAddr, cfg.Path)
	return server.Serve(listener)
}

func newHandler(cfg bridgeConfig, logger *log.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(cfg.Path, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		conn, err := websocket.Upgrade(w, r, nil, 64*1024, 64*1024)
		if err != nil {
			logger.Printf("websocket upgrade failed: %v", err)
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		if err := handleConnection(r.Context(), conn, cfg, logger); err != nil {
			logger.Printf("connection failed: %v", err)
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseInternalServerErr, err.Error()), timeNowPlusSecond())
		}
	})
	if cfg.Path != "/" {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return mux
}

func handleConnection(parent context.Context, conn *websocket.Conn, cfg bridgeConfig, logger *log.Logger) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	pumpErrCh := make(chan pumpResult, 2)
	waitCh := make(chan error, 1)
	go func() {
		pumpErrCh <- pumpResult{source: "ws", err: pumpWebSocketToStdin(conn, stdin)}
	}()
	go func() {
		pumpErrCh <- pumpResult{source: "stdout", err: pumpStdoutToWebSocket(conn, stdout)}
	}()
	go logPipe(stderr, logger)
	go func() {
		waitCh <- cmd.Wait()
	}()

	for {
		select {
		case result := <-pumpErrCh:
			cancel()
			_ = stdin.Close()
			waitErr := <-waitCh
			return normalizeBridgeError(result, waitErr)
		case err := <-waitCh:
			cancel()
			_ = stdin.Close()
			if err != nil {
				return err
			}
			return nil
		}
	}
}

func pumpWebSocketToStdin(conn *websocket.Conn, stdin io.WriteCloser) error {
	defer func() {
		_ = stdin.Close()
	}()
	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		if len(payload) == 0 {
			continue
		}
		if _, err := stdin.Write(payload); err != nil {
			return err
		}
		if payload[len(payload)-1] != '\n' {
			if _, err := stdin.Write([]byte{'\n'}); err != nil {
				return err
			}
		}
	}
}

func pumpStdoutToWebSocket(conn *websocket.Conn, stdout io.Reader) error {
	reader := bufio.NewReader(stdout)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			line = bytesTrimRightNewline(line)
			if len(line) > 0 {
				if writeErr := conn.WriteMessage(websocket.TextMessage, line); writeErr != nil {
					return writeErr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func bytesTrimRightNewline(line []byte) []byte {
	for len(line) > 0 {
		last := line[len(line)-1]
		if last != '\n' && last != '\r' {
			return line
		}
		line = line[:len(line)-1]
	}
	return line
}

func logPipe(stderr io.Reader, logger *log.Logger) {
	scanner := bufio.NewScanner(stderr)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		logger.Printf("openclaw: %s", line)
	}
}

func normalizeBridgeError(result pumpResult, waitErr error) error {
	if result.err != nil {
		return result.err
	}
	if result.source == "ws" {
		return nil
	}
	if waitErr != nil {
		return waitErr
	}
	return nil
}

func timeNowPlusSecond() time.Time {
	return time.Now().Add(time.Second)
}
