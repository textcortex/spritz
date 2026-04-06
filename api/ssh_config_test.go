package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestNewSSHGatewayConfigBindsIPv4ListenAddr(t *testing.T) {
	t.Setenv("SPRITZ_SSH_GATEWAY_ENABLED", "true")
	t.Setenv("SPRITZ_SSH_PUBLIC_HOST", "ssh.example.com")
	t.Setenv("SPRITZ_SSH_GATEWAY_PORT", "2022")
	t.Setenv("SPRITZ_SSH_CA_KEY", newTestSSHPrivateKeyPEM(t))
	t.Setenv("SPRITZ_SSH_HOST_KEY", newTestSSHPrivateKeyPEM(t))

	cfg, err := newSSHGatewayConfig()
	if err != nil {
		t.Fatalf("newSSHGatewayConfig() error = %v", err)
	}
	if cfg.listenAddr != "0.0.0.0:2022" {
		t.Fatalf("listenAddr = %q, want %q", cfg.listenAddr, "0.0.0.0:2022")
	}
}

func newTestSSHPrivateKeyPEM(t *testing.T) string {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal pkcs8 private key: %v", err)
	}

	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: der,
	}))
}
