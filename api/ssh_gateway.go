package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	sshserver "github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/tools/remotecommand"
	transportspdy "k8s.io/client-go/transport/spdy"

	spritzv1 "spritz.sh/operator/api/v1"
)

const sshPrincipalDelimiter = ":"

type sshPortForwardActivityStartedKey struct{}

type sshLocalForwardChannelData struct {
	DestAddr string
	DestPort uint32

	OriginAddr string
	OriginPort uint32
}

type closeFunc func() error

func (f closeFunc) Close() error {
	return f()
}

func formatSSHPrincipal(prefix, namespace, name string) string {
	return strings.Join([]string{prefix, namespace, name}, sshPrincipalDelimiter)
}

func parseSSHPrincipal(prefix, principal string) (string, string, bool) {
	parts := strings.Split(principal, sshPrincipalDelimiter)
	if len(parts) != 3 {
		return "", "", false
	}
	if parts[0] != prefix {
		return "", "", false
	}
	if parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (s *server) startSSHGateway(ctx context.Context) error {
	cfg := s.sshGateway
	if !cfg.enabled {
		return nil
	}

	server := s.newSSHGatewayServer()
	server.AddHostKey(cfg.hostSigner)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(250 * time.Millisecond):
		log.Printf("spritz ssh gateway listening on %s", cfg.listenAddr)
		return nil
	}
}

func (s *server) newSSHGatewayServer() *sshserver.Server {
	cfg := s.sshGateway
	return &sshserver.Server{
		Addr:             cfg.listenAddr,
		Handler:          s.handleSSHSession,
		PublicKeyHandler: s.handleSSHAuth,
		Version:          "spritz",
		ChannelHandlers: map[string]sshserver.ChannelHandler{
			"session":      sshserver.DefaultSessionHandler,
			"direct-tcpip": s.handleSSHPortForward,
		},
		LocalPortForwardingCallback: s.allowSSHPortForwardDestination,
	}
}

func (s *server) handleSSHAuth(ctx sshserver.Context, key sshserver.PublicKey) bool {
	cert, ok := key.(*gossh.Certificate)
	if !ok {
		log.Printf("spritz ssh: auth failed user=%s reason=missing-cert", ctx.User())
		return false
	}
	if err := s.sshGateway.certChecker.CheckCert(ctx.User(), cert); err != nil {
		log.Printf("spritz ssh: auth failed user=%s key_id=%s err=%v", ctx.User(), cert.KeyId, err)
		return false
	}
	return true
}

func (s *server) handleSSHSession(sess sshserver.Session) {
	principal := sess.User()
	namespace, name, ok := parseSSHPrincipal(s.sshGateway.principalPrefix, principal)
	if !ok {
		log.Printf("spritz ssh: invalid principal value=%s", principal)
		_, _ = io.WriteString(sess, "invalid ssh principal\n")
		_ = sess.Exit(1)
		return
	}
	keyID := ""
	if cert, ok := sess.PublicKey().(*gossh.Certificate); ok {
		keyID = strings.TrimPrefix(cert.KeyId, "spritz:")
	}
	log.Printf("spritz ssh: session start name=%s namespace=%s user_id=%s", name, namespace, keyID)
	defer log.Printf("spritz ssh: session end name=%s namespace=%s user_id=%s", name, namespace, keyID)

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(sess.Context(), clientKey(namespace, name), spritz); err != nil {
		log.Printf("spritz ssh: spritz not found name=%s namespace=%s user_id=%s err=%v", name, namespace, keyID, err)
		_, _ = io.WriteString(sess, "spritz not ready\n")
		_ = sess.Exit(1)
		return
	}

	pod, err := s.findRunningPod(sess.Context(), namespace, name, s.sshGateway.containerName)
	if err != nil {
		log.Printf("spritz ssh: pod not ready name=%s namespace=%s err=%v", name, namespace, err)
		_, _ = io.WriteString(sess, "spritz not ready\n")
		_ = sess.Exit(1)
		return
	}
	s.ensureSSHActivityLoop(sess.Context(), spritz)

	pty, winCh, hasPty := sess.Pty()
	sizeQueue := newTerminalSizeQueue()
	if hasPty {
		sizeQueue.push(uint16(pty.Window.Width), uint16(pty.Window.Height))
		go func() {
			for win := range winCh {
				sizeQueue.push(uint16(win.Width), uint16(win.Height))
			}
		}()
	}

	if err := s.streamSSH(sess.Context(), pod, sess, hasPty, sizeQueue); err != nil {
		log.Printf("spritz ssh: stream failed name=%s namespace=%s err=%v", name, namespace, err)
		_ = sess.Exit(1)
		return
	}
	_ = sess.Exit(0)
}

func (s *server) handleSSHPortForward(srv *sshserver.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx sshserver.Context) {
	var request sshLocalForwardChannelData
	if err := gossh.Unmarshal(newChan.ExtraData(), &request); err != nil {
		newChan.Reject(gossh.ConnectionFailed, "error parsing forward data: "+err.Error())
		return
	}
	if srv.LocalPortForwardingCallback == nil || !srv.LocalPortForwardingCallback(ctx, request.DestAddr, request.DestPort) {
		newChan.Reject(gossh.Prohibited, "port forwarding is disabled")
		return
	}

	namespace, name, ok := parseSSHPrincipal(s.sshGateway.principalPrefix, ctx.User())
	if !ok {
		log.Printf("spritz ssh: invalid forward principal value=%s", ctx.User())
		newChan.Reject(gossh.Prohibited, "invalid ssh principal")
		return
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(ctx, clientKey(namespace, name), spritz); err != nil {
		log.Printf("spritz ssh: forward spritz not found name=%s namespace=%s err=%v", name, namespace, err)
		newChan.Reject(gossh.ConnectionFailed, "spritz not ready")
		return
	}

	pod, err := s.findSSHGatewayPod(ctx, namespace, name, s.sshGateway.containerName)
	if err != nil {
		log.Printf("spritz ssh: forward pod not ready name=%s namespace=%s err=%v", name, namespace, err)
		newChan.Reject(gossh.ConnectionFailed, "spritz not ready")
		return
	}
	s.ensureSSHActivityLoop(ctx, spritz)

	upstream, cleanup, err := s.openPodPortForward(ctx, pod, request.DestPort)
	if err != nil {
		log.Printf("spritz ssh: forward open failed name=%s namespace=%s port=%d err=%v", name, namespace, request.DestPort, err)
		newChan.Reject(gossh.ConnectionFailed, "port forward unavailable")
		return
	}

	channel, requests, err := newChan.Accept()
	if err != nil {
		_ = upstream.Close()
		_ = cleanup.Close()
		return
	}
	go gossh.DiscardRequests(requests)

	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			_ = channel.Close()
			_ = upstream.Close()
			_ = cleanup.Close()
		})
	}

	go func() {
		defer closeAll()
		_, _ = io.Copy(channel, upstream)
	}()
	go func() {
		defer closeAll()
		_, _ = io.Copy(upstream, channel)
	}()
}

func (s *server) allowSSHPortForwardDestination(ctx sshserver.Context, destinationHost string, destinationPort uint32) bool {
	if !isLoopbackSSHForwardHost(destinationHost) {
		log.Printf("spritz ssh: rejected forward user=%s host=%s port=%d", ctx.User(), destinationHost, destinationPort)
		return false
	}
	return true
}

func isLoopbackSSHForwardHost(host string) bool {
	normalized := strings.TrimSpace(host)
	normalized = strings.TrimPrefix(normalized, "[")
	normalized = strings.TrimSuffix(normalized, "]")
	if normalized == "" {
		return false
	}
	if strings.EqualFold(normalized, "localhost") {
		return true
	}
	ip := net.ParseIP(normalized)
	return ip != nil && ip.IsLoopback()
}

func (s *server) ensureSSHActivityLoop(ctx sshserver.Context, spritz *spritzv1.Spritz) {
	if s == nil || spritz == nil {
		return
	}
	ctx.Lock()
	defer ctx.Unlock()
	if started, ok := ctx.Value(sshPortForwardActivityStartedKey{}).(bool); ok && started {
		return
	}
	ctx.SetValue(sshPortForwardActivityStartedKey{}, true)
	s.startSSHActivityLoop(ctx, spritz)
}

func (s *server) findSSHGatewayPod(ctx context.Context, namespace, name, container string) (*corev1.Pod, error) {
	if s.findRunningPodFunc != nil {
		return s.findRunningPodFunc(ctx, namespace, name, container)
	}
	return s.findRunningPod(ctx, namespace, name, container)
}

func (s *server) openPodPortForward(ctx context.Context, pod *corev1.Pod, remotePort uint32) (net.Conn, io.Closer, error) {
	if s.openPodPortForwardFunc != nil {
		return s.openPodPortForwardFunc(ctx, pod, remotePort)
	}
	if s.clientset == nil || s.restConfig == nil {
		return nil, nil, errors.New("ssh port forwarding is not configured")
	}

	req := s.clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("portforward")
	transport, upgrader, err := transportspdy.RoundTripperFor(s.restConfig)
	if err != nil {
		return nil, nil, err
	}
	dialer := transportspdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, req.URL())
	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	errCh := make(chan error, 1)
	forwarder, err := portforward.NewOnAddresses(
		dialer,
		[]string{"127.0.0.1"},
		[]string{fmt.Sprintf("0:%d", remotePort)},
		stopCh,
		readyCh,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		close(stopCh)
		return nil, nil, err
	}

	go func() {
		errCh <- forwarder.ForwardPorts()
	}()

	select {
	case <-readyCh:
	case err := <-errCh:
		close(stopCh)
		return nil, nil, err
	case <-ctx.Done():
		close(stopCh)
		return nil, nil, ctx.Err()
	}

	ports, err := forwarder.GetPorts()
	if err != nil {
		close(stopCh)
		return nil, nil, err
	}
	if len(ports) != 1 {
		close(stopCh)
		return nil, nil, fmt.Errorf("unexpected forwarded port count: %d", len(ports))
	}

	localAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(ports[0].Local)))
	upstream, err := (&net.Dialer{}).DialContext(ctx, "tcp", localAddress)
	if err != nil {
		close(stopCh)
		return nil, nil, err
	}

	var once sync.Once
	cleanup := closeFunc(func() error {
		once.Do(func() {
			close(stopCh)
		})
		return nil
	})

	go func() {
		err := <-errCh
		if err == nil || errors.Is(err, portforward.ErrLostConnectionToPod) || errors.Is(err, context.Canceled) {
			return
		}
		log.Printf("spritz ssh: port-forward ended pod=%s namespace=%s remote_port=%d err=%v", pod.Name, pod.Namespace, remotePort, err)
	}()

	return upstream, cleanup, nil
}

func sshActivityRefreshInterval(spec spritzv1.SpritzSpec, fallback time.Duration) time.Duration {
	return spritzActivityRefreshInterval(spec, fallback)
}

func (s *server) startSSHActivityLoop(ctx context.Context, spritz *spritzv1.Spritz) {
	s.startSpritzActivityLoop(ctx, spritz, s.sshGateway.activityRefresh, "ssh")
}

func (s *server) streamSSH(ctx context.Context, pod *corev1.Pod, sess sshserver.Session, hasPty bool, sizeQueue *terminalSizeQueue) error {
	if len(s.sshGateway.command) == 0 {
		return fmt.Errorf("ssh command missing")
	}

	req := s.clientset.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: s.sshGateway.containerName,
			Command:   s.sshGateway.command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    true,
			TTY:       hasPty,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(s.restConfig, "POST", req.URL())
	if err != nil {
		return err
	}

	stdout := sess
	stderr := sess.Stderr()
	if stderr == nil {
		stderr = sess
	}

	return executor.Stream(remotecommand.StreamOptions{
		Stdin:             sess,
		Stdout:            stdout,
		Stderr:            stderr,
		Tty:               hasPty,
		TerminalSizeQueue: sizeQueue,
	})
}
