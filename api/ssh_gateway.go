package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	sshserver "github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

const sshPrincipalDelimiter = ":"

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

	server := &sshserver.Server{
		Addr:             cfg.listenAddr,
		Handler:          s.handleSSHSession,
		PublicKeyHandler: s.handleSSHAuth,
		Version:          "spritz",
	}
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

	pod, err := s.findRunningPod(sess.Context(), namespace, name, s.sshGateway.containerName)
	if err != nil {
		log.Printf("spritz ssh: pod not ready name=%s namespace=%s err=%v", name, namespace, err)
		_, _ = io.WriteString(sess, "spritz not ready\n")
		_ = sess.Exit(1)
		return
	}

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
