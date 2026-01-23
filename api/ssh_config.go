package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	spritzv1 "spritz.sh/operator/api/v1"
)

type sshGatewayConfig struct {
	enabled         bool
	listenAddr      string
	publicHost      string
	publicPort      int
	user            string
	principalPrefix string
	certTTL         time.Duration
	containerName   string
	command         []string
	caSigner        ssh.Signer
	hostSigner      ssh.Signer
	hostPublicKey   ssh.PublicKey
	certChecker     *ssh.CertChecker
}

type sshDefaults struct {
	enabled          bool
	mode             string
	gatewayService   string
	gatewayNamespace string
	gatewayPort      int32
	user             string
}

func newSSHDefaults() sshDefaults {
	return sshDefaults{
		enabled:          parseBoolEnv("SPRITZ_DEFAULT_SSH_ENABLED", false),
		mode:             envOrDefault("SPRITZ_DEFAULT_SSH_MODE", "gateway"),
		gatewayService:   os.Getenv("SPRITZ_DEFAULT_SSH_GATEWAY_SERVICE"),
		gatewayNamespace: os.Getenv("SPRITZ_DEFAULT_SSH_GATEWAY_NAMESPACE"),
		gatewayPort:      int32(parseIntEnv("SPRITZ_DEFAULT_SSH_GATEWAY_PORT", 22)),
		user:             envOrDefault("SPRITZ_DEFAULT_SSH_USER", "spritz"),
	}
}

func newSSHGatewayConfig() (sshGatewayConfig, error) {
	enabled := parseBoolEnv("SPRITZ_SSH_GATEWAY_ENABLED", false)
	if !enabled {
		return sshGatewayConfig{enabled: false}, nil
	}

	caSigner, err := loadSSHSigner("SPRITZ_SSH_CA_KEY", "SPRITZ_SSH_CA_KEY_FILE")
	if err != nil {
		return sshGatewayConfig{}, fmt.Errorf("ssh gateway CA key: %w", err)
	}
	hostSigner, err := loadSSHSigner("SPRITZ_SSH_HOST_KEY", "SPRITZ_SSH_HOST_KEY_FILE")
	if err != nil {
		return sshGatewayConfig{}, fmt.Errorf("ssh gateway host key: %w", err)
	}

	publicHost := strings.TrimSpace(os.Getenv("SPRITZ_SSH_PUBLIC_HOST"))
	publicPort := parseIntEnv("SPRITZ_SSH_PUBLIC_PORT", 22)
	if publicHost == "" {
		return sshGatewayConfig{}, errors.New("SPRITZ_SSH_PUBLIC_HOST is required when SSH gateway is enabled")
	}
	listenPort := parseIntEnv("SPRITZ_SSH_GATEWAY_PORT", 2222)
	user := envOrDefault("SPRITZ_SSH_USER", "spritz")
	principalPrefix := envOrDefault("SPRITZ_SSH_PRINCIPAL_PREFIX", "spritz")
	certTTL := parseDurationEnv("SPRITZ_SSH_CERT_TTL", 15*time.Minute)
	if certTTL <= 0 {
		certTTL = 15 * time.Minute
	}
	containerName := envOrDefault("SPRITZ_SSH_CONTAINER", "spritz")
	command := splitCommand(envOrDefault("SPRITZ_SSH_COMMAND", "bash -l"))

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return keysEqual(auth, caSigner.PublicKey())
		},
	}

	return sshGatewayConfig{
		enabled:         true,
		listenAddr:      fmt.Sprintf(":%d", listenPort),
		publicHost:      publicHost,
		publicPort:      publicPort,
		user:            user,
		principalPrefix: principalPrefix,
		certTTL:         certTTL,
		containerName:   containerName,
		command:         command,
		caSigner:        caSigner,
		hostSigner:      hostSigner,
		hostPublicKey:   hostSigner.PublicKey(),
		certChecker:     checker,
	}, nil
}

func keysEqual(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return a == b
	}
	return bytes.Equal(a.Marshal(), b.Marshal())
}

func loadSSHSigner(valueEnv, fileEnv string) (ssh.Signer, error) {
	if value := strings.TrimSpace(os.Getenv(valueEnv)); value != "" {
		return ssh.ParsePrivateKey([]byte(value))
	}
	path := strings.TrimSpace(os.Getenv(fileEnv))
	if path == "" {
		return nil, fmt.Errorf("%s or %s must be set", valueEnv, fileEnv)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(data)
}

func applySSHDefaults(spec *spritzv1.SpritzSpec, defaults sshDefaults, namespace string) {
	if !defaults.enabled {
		return
	}
	if spec.SSH != nil && !spec.SSH.Enabled {
		return
	}
	if spec.Features != nil && spec.Features.SSH != nil && !*spec.Features.SSH {
		return
	}
	if spec.SSH == nil {
		spec.SSH = &spritzv1.SpritzSSH{Enabled: true}
	} else if !spec.SSH.Enabled {
		spec.SSH.Enabled = true
	}
	if spec.Features == nil {
		spec.Features = &spritzv1.SpritzFeatures{}
	}
	if spec.Features.SSH == nil {
		enabled := true
		spec.Features.SSH = &enabled
	}
	if spec.SSH.Mode == "" {
		spec.SSH.Mode = defaults.mode
	}
	if spec.SSH.User == "" {
		spec.SSH.User = defaults.user
	}
	if strings.EqualFold(spec.SSH.Mode, "gateway") {
		if spec.SSH.GatewayService == "" {
			spec.SSH.GatewayService = defaults.gatewayService
		}
		if spec.SSH.GatewayNamespace == "" {
			spec.SSH.GatewayNamespace = defaults.gatewayNamespace
			if spec.SSH.GatewayNamespace == "" {
				spec.SSH.GatewayNamespace = namespace
			}
		}
		if spec.SSH.GatewayPort == 0 {
			spec.SSH.GatewayPort = defaults.gatewayPort
		}
	}
}
