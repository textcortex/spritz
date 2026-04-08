package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrlclientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	spritzv1 "spritz.sh/operator/api/v1"
)

func TestSSHActivityRefreshIntervalUsesHalfIdleTTLWhenShorter(t *testing.T) {
	spec := spritzv1.SpritzSpec{IdleTTL: "80ms"}

	interval := sshActivityRefreshInterval(spec, time.Second)
	if interval != 40*time.Millisecond {
		t.Fatalf("expected 40ms interval, got %s", interval)
	}
}

func TestStartSSHActivityLoopRefreshesWhileSessionIsOpen(t *testing.T) {
	var calls atomic.Int32
	s := &server{
		sshGateway: sshGatewayConfig{activityRefresh: 40 * time.Millisecond},
		activityRecorder: func(ctx context.Context, namespace, name string, when time.Time) error {
			calls.Add(1)
			return nil
		},
	}
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ssh-instance",
			Namespace: "spritz-test",
		},
		Spec: spritzv1.SpritzSpec{IdleTTL: "80ms"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.startSSHActivityLoop(ctx, spritz)
	time.Sleep(95 * time.Millisecond)
	cancel()

	if calls.Load() < 2 {
		t.Fatalf("expected repeated activity refreshes, got %d", calls.Load())
	}
}

func TestIsLoopbackSSHForwardHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		host string
		want bool
	}{
		{host: "127.0.0.1", want: true},
		{host: "localhost", want: true},
		{host: "::1", want: true},
		{host: "[::1]", want: true},
		{host: "10.0.0.10", want: false},
		{host: "example.com", want: false},
		{host: "", want: false},
	}

	for _, tc := range cases {
		if got := isLoopbackSSHForwardHost(tc.host); got != tc.want {
			t.Fatalf("host %q => %t, want %t", tc.host, got, tc.want)
		}
	}
}

func TestSSHGatewayPortForwardProxiesToInjectedUpstream(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	if err := spritzv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add spritz scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}

	caSigner := newTestSSHSigner(t)
	hostSigner := newTestSSHSigner(t)
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo: %v", err)
	}
	defer echoListener.Close()
	go func() {
		conn, err := echoListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(conn, conn)
	}()

	var activityCalls atomic.Int32
	var forwardedPort atomic.Int32
	spritz := &spritzv1.Spritz{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ssh-instance",
			Namespace: "spritz-test",
		},
		Spec: spritzv1.SpritzSpec{IdleTTL: "1m"},
	}
	s := &server{
		client: ctrlclientfake.NewClientBuilder().WithScheme(scheme).WithObjects(spritz).Build(),
		sshGateway: sshGatewayConfig{
			enabled:         true,
			listenAddr:      "127.0.0.1:0",
			user:            "spritz",
			principalPrefix: "spritz",
			certTTL:         time.Minute,
			activityRefresh: time.Minute,
			containerName:   "spritz",
			caSigner:        caSigner,
			hostSigner:      hostSigner,
			hostPublicKey:   hostSigner.PublicKey(),
			certChecker: &gossh.CertChecker{
				IsUserAuthority: func(auth gossh.PublicKey) bool {
					return keysEqual(auth, caSigner.PublicKey())
				},
			},
		},
		activityRecorder: func(ctx context.Context, namespace, name string, when time.Time) error {
			activityCalls.Add(1)
			return nil
		},
		findRunningPodFunc: func(ctx context.Context, namespace, name, container string) (*corev1.Pod, error) {
			return &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ssh-instance-pod",
					Namespace: namespace,
				},
			}, nil
		},
		openPodPortForwardFunc: func(ctx context.Context, pod *corev1.Pod, remotePort uint32) (net.Conn, io.Closer, error) {
			forwardedPort.Store(int32(remotePort))
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", echoListener.Addr().String())
			if err != nil {
				return nil, nil, err
			}
			return conn, closeFunc(func() error { return nil }), nil
		},
	}

	sshListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ssh: %v", err)
	}
	defer sshListener.Close()

	sshServer := s.newSSHGatewayServer()
	sshServer.AddHostKey(hostSigner)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- sshServer.Serve(sshListener)
	}()
	defer func() {
		_ = sshServer.Close()
		select {
		case <-serverDone:
		case <-time.After(200 * time.Millisecond):
		}
	}()

	principal := formatSSHPrincipal(s.sshGateway.principalPrefix, "spritz-test", "ssh-instance")
	userSigner := newTestSSHSigner(t)
	cert, err := s.signSSHCert(userSigner.PublicKey(), principal, "user-123")
	if err != nil {
		t.Fatalf("sign cert: %v", err)
	}
	certSigner, err := gossh.NewCertSigner(cert, userSigner)
	if err != nil {
		t.Fatalf("new cert signer: %v", err)
	}

	client, err := gossh.Dial("tcp", sshListener.Addr().String(), &gossh.ClientConfig{
		User:            principal,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(certSigner)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	})
	if err != nil {
		t.Fatalf("dial ssh gateway: %v", err)
	}
	defer client.Close()

	forwardedConn, err := client.Dial("tcp", "127.0.0.1:3000")
	if err != nil {
		t.Fatalf("open direct-tcpip channel: %v", err)
	}
	defer forwardedConn.Close()

	if _, err := forwardedConn.Write([]byte("ping")); err != nil {
		t.Fatalf("write forwarded conn: %v", err)
	}
	buffer := make([]byte, 4)
	if _, err := io.ReadFull(forwardedConn, buffer); err != nil {
		t.Fatalf("read forwarded conn: %v", err)
	}
	if string(buffer) != "ping" {
		t.Fatalf("unexpected echoed payload %q", string(buffer))
	}
	if got := forwardedPort.Load(); got != 3000 {
		t.Fatalf("forwarded remote port = %d, want 3000", got)
	}
	if activityCalls.Load() == 0 {
		t.Fatal("expected port forwarding to mark ssh activity")
	}
}

func newTestSSHSigner(t *testing.T) gossh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	signer, err := gossh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("new ssh signer: %v", err)
	}
	return signer
}
