package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	spritzv1 "spritz.sh/operator/api/v1"
)

type terminalConfig struct {
	enabled        bool
	containerName  string
	command        []string
	allowedOrigins map[string]struct{}
	sessionMode    terminalSessionMode
}

type terminalSessionMode string

const (
	terminalSessionNone terminalSessionMode = "none"
	terminalSessionZmx  terminalSessionMode = "zmx"
)

func newTerminalConfig() terminalConfig {
	return terminalConfig{
		enabled:        parseBoolEnv("SPRITZ_TERMINAL_ENABLED", true),
		containerName:  envOrDefault("SPRITZ_TERMINAL_CONTAINER", "spritz"),
		command:        splitCommand(envOrDefault("SPRITZ_TERMINAL_COMMAND", "bash -l")),
		allowedOrigins: splitSet(os.Getenv("SPRITZ_TERMINAL_ORIGINS")),
		sessionMode:    parseTerminalSessionMode(os.Getenv("SPRITZ_TERMINAL_SESSION_MODE")),
	}
}

func splitCommand(value string) []string {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) == 0 {
		return []string{"bash", "-l"}
	}
	return parts
}

func parseTerminalSessionMode(value string) terminalSessionMode {
	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "":
		return terminalSessionZmx
	case "zmx":
		return terminalSessionZmx
	case "none":
		return terminalSessionNone
	default:
		return terminalSessionNone
	}
}

func (t terminalConfig) allowOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if len(t.allowedOrigins) == 0 {
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
	return hasSetValue(t.allowedOrigins, origin)
}

func hasSetValue(values map[string]struct{}, key string) bool {
	if _, ok := values[key]; ok {
		return true
	}
	for item := range values {
		if strings.EqualFold(item, key) {
			return true
		}
	}
	return false
}

func (s *server) openTerminal(c echo.Context) error {
	if !s.terminal.enabled {
		return writeError(c, http.StatusNotFound, "terminal disabled")
	}

	principal, ok := principalFromContext(c)
	if s.auth.enabled() && (!ok || principal.ID == "") {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}

	name := strings.TrimSpace(c.Param("name"))
	if name == "" {
		return writeError(c, http.StatusBadRequest, "spritz name required")
	}

	namespace := s.namespace
	if namespace == "" {
		namespace = c.QueryParam("namespace")
	}
	if namespace == "" {
		namespace = "default"
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), clientKey(namespace, name), spritz); err != nil {
		log.Printf("spritz terminal: spritz not found name=%s namespace=%s user_id=%s err=%v", name, namespace, principal.ID, err)
		return writeError(c, http.StatusNotFound, "spritz not found")
	}

	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
		log.Printf("spritz terminal: owner mismatch name=%s namespace=%s user_id=%s owner_id=%s", name, namespace, principal.ID, spritz.Spec.Owner.ID)
		return writeError(c, http.StatusForbidden, "owner mismatch")
	}

	pod, err := s.findRunningPod(c.Request().Context(), namespace, name, s.terminal.containerName)
	if err != nil {
		log.Printf("spritz terminal: pod not ready name=%s namespace=%s user_id=%s err=%v", name, namespace, principal.ID, err)
		return writeError(c, http.StatusConflict, "spritz not ready")
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: s.terminal.allowOrigin,
	}
	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Close()
	}()

	session := strings.TrimSpace(c.QueryParam("session"))
	command, resolvedSession, usingZmx, err := s.resolveTerminalCommand(c.Request().Context(), pod, namespace, name, session)
	if err != nil {
		return err
	}
	if usingZmx {
		log.Printf("spritz terminal: zmx attach name=%s namespace=%s session=%s user_id=%s", name, namespace, resolvedSession, principal.ID)
	}
	if err := s.streamTerminal(c.Request().Context(), pod, conn, command); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	return nil
}

func (s *server) findRunningPod(ctx context.Context, namespace, name, container string) (*corev1.Pod, error) {
	list := &corev1.PodList{}
	selector := labels.Set{nameLabelKey: name}
	if err := s.client.List(ctx, list, clientListOptions(namespace, selector)...); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var candidate *corev1.Pod
	for _, pod := range list.Items {
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == container && status.Ready {
				return pod.DeepCopy(), nil
			}
		}
		if candidate == nil {
			candidate = pod.DeepCopy()
		}
	}
	if candidate != nil {
		return candidate, nil
	}
	return nil, fmt.Errorf("spritz not ready")
}

func (s *server) streamTerminal(ctx context.Context, pod *corev1.Pod, conn *websocket.Conn, command []string) error {
	if len(command) == 0 {
		return errors.New("terminal command missing")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	req := s.clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: s.terminal.containerName,
			Command:   command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       true,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(s.restConfig, http.MethodPost, req.URL())
	if err != nil {
		return err
	}

	stdinReader, stdinWriter := io.Pipe()
	sizeQueue := newTerminalSizeQueue()
	wsWriter := &terminalWSWriter{conn: conn}

	readErr := make(chan error, 1)
	go func() {
		readErr <- readTerminalInput(ctx, conn, stdinWriter, sizeQueue)
	}()

	streamErr := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             stdinReader,
		Stdout:            wsWriter,
		Stderr:            wsWriter,
		Tty:               true,
		TerminalSizeQueue: sizeQueue,
	})
	_ = stdinWriter.Close()
	cancel()
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(500*time.Millisecond))
	_ = conn.Close()

	select {
	case err := <-readErr:
		if err != nil && streamErr == nil {
			streamErr = err
		}
	case <-time.After(2 * time.Second):
	}

	return streamErr
}

type resizeMessage struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func readTerminalInput(ctx context.Context, conn *websocket.Conn, stdin *io.PipeWriter, sizeQueue *terminalSizeQueue) error {
	for {
		select {
		case <-ctx.Done():
			_ = stdin.CloseWithError(ctx.Err())
			return ctx.Err()
		default:
		}
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			_ = stdin.CloseWithError(err)
			return err
		}
		if msgType == websocket.TextMessage {
			if handleResize(payload, sizeQueue) {
				continue
			}
		}
		if msgType == websocket.BinaryMessage || msgType == websocket.TextMessage {
			if _, err := stdin.Write(payload); err != nil {
				return err
			}
		}
	}
}

func handleResize(payload []byte, sizeQueue *terminalSizeQueue) bool {
	var msg resizeMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return false
	}
	if msg.Type != "resize" || msg.Cols <= 0 || msg.Rows <= 0 {
		return false
	}
	sizeQueue.push(uint16(msg.Cols), uint16(msg.Rows))
	return true
}

type terminalWSWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *terminalWSWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

type terminalSizeQueue struct {
	sizes chan remotecommand.TerminalSize
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{sizes: make(chan remotecommand.TerminalSize, 4)}
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	size, ok := <-q.sizes
	if !ok {
		return nil
	}
	return &size
}

func (q *terminalSizeQueue) push(cols, rows uint16) {
	select {
	case q.sizes <- remotecommand.TerminalSize{Width: cols, Height: rows}:
	default:
	}
}

func terminalDefaultSession(namespace, name string) string {
	ns := strings.TrimSpace(namespace)
	if ns == "" {
		ns = "default"
	}
	return fmt.Sprintf("spritz:%s:%s", ns, strings.TrimSpace(name))
}

func (s *server) resolveTerminalCommand(ctx context.Context, pod *corev1.Pod, namespace, name, session string) ([]string, string, bool, error) {
	if len(s.terminal.command) == 0 {
		return nil, "", false, errors.New("terminal command missing")
	}
	if s.terminal.sessionMode != terminalSessionZmx {
		return s.terminal.command, "", false, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	available, err := s.zmxAvailable(checkCtx, pod)
	if err != nil {
		log.Printf("spritz terminal: zmx check failed name=%s namespace=%s err=%v", name, namespace, err)
		return s.terminal.command, "", false, nil
	}
	if !available {
		return s.terminal.command, "", false, nil
	}
	resolved := strings.TrimSpace(session)
	if resolved == "" {
		resolved = terminalDefaultSession(namespace, name)
	}
	if resolved == "" {
		return s.terminal.command, "", false, nil
	}
	command := make([]string, 0, len(s.terminal.command)+3)
	command = append(command, "zmx", "attach", resolved)
	command = append(command, s.terminal.command...)
	return command, resolved, true, nil
}

func (s *server) execInContainer(ctx context.Context, pod *corev1.Pod, command []string) (string, string, error) {
	req := s.clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: s.terminal.containerName,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(s.restConfig, http.MethodPost, req.URL())
	if err != nil {
		return "", "", err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	}); err != nil {
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}

func (s *server) zmxAvailable(ctx context.Context, pod *corev1.Pod) (bool, error) {
	stdout, _, err := s.execInContainer(ctx, pod, []string{"sh", "-lc", "if command -v zmx >/dev/null 2>&1; then echo ready; else echo missing; fi"})
	if err != nil {
		return false, err
	}
	return strings.Contains(stdout, "ready"), nil
}

func parseZmxSessionList(output string) []string {
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil
	}
	sessions := make([]string, 0, len(lines))
	seen := make(map[string]struct{})
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "no sessions found") {
			return nil
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := ""
		for _, field := range fields {
			if strings.HasPrefix(field, "session_name=") {
				name = strings.TrimPrefix(field, "session_name=")
				break
			}
		}
		if name == "" {
			name = fields[0]
		}
		for strings.HasPrefix(name, "session_name=") {
			name = strings.TrimPrefix(name, "session_name=")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		sessions = append(sessions, name)
	}
	return sessions
}

func (s *server) listZmxSessions(ctx context.Context, pod *corev1.Pod) ([]string, error) {
	stdout, stderr, err := s.execInContainer(ctx, pod, []string{"zmx", "list"})
	if err != nil {
		return nil, fmt.Errorf("zmx list failed: %w (stderr=%s)", err, strings.TrimSpace(stderr))
	}
	return parseZmxSessionList(stdout), nil
}

func clientKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}

func clientListOptions(namespace string, selector labels.Set) []client.ListOption {
	opts := []client.ListOption{client.InNamespace(namespace)}
	if len(selector) > 0 {
		opts = append(opts, client.MatchingLabelsSelector{
			Selector: labels.SelectorFromSet(selector),
		})
	}
	return opts
}
