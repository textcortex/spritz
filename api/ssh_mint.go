package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/ssh"

	spritzv1 "spritz.sh/operator/api/v1"
)

type sshMintRequest struct {
	PublicKey string `json:"public_key"`
}

type sshMintResponse struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	User       string `json:"user"`
	Cert       string `json:"cert"`
	KnownHosts string `json:"known_hosts,omitempty"`
	ExpiresAt  string `json:"expires_at"`
}

func (s *server) mintSSHCert(c echo.Context) error {
	if !s.sshGateway.enabled {
		return writeError(c, http.StatusNotFound, "ssh gateway disabled")
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

	var body sshMintRequest
	if err := c.Bind(&body); err != nil {
		return writeError(c, http.StatusBadRequest, "invalid json")
	}
	if strings.TrimSpace(body.PublicKey) == "" {
		return writeError(c, http.StatusBadRequest, "public_key is required")
	}

	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey))
	if err != nil {
		return writeError(c, http.StatusBadRequest, "invalid public_key")
	}

	spritz := &spritzv1.Spritz{}
	if err := s.client.Get(c.Request().Context(), clientKey(namespace, name), spritz); err != nil {
		log.Printf("spritz ssh: spritz not found name=%s namespace=%s user_id=%s err=%v", name, namespace, principal.ID, err)
		return writeError(c, http.StatusNotFound, "spritz not found")
	}
	if s.auth.enabled() && !principal.IsAdmin && spritz.Spec.Owner.ID != principal.ID {
		log.Printf("spritz ssh: owner mismatch name=%s namespace=%s user_id=%s owner_id=%s", name, namespace, principal.ID, spritz.Spec.Owner.ID)
		return writeError(c, http.StatusForbidden, "owner mismatch")
	}
	if !isSSHEnabled(spritz.Spec) {
		log.Printf("spritz ssh: ssh disabled name=%s namespace=%s user_id=%s", name, namespace, principal.ID)
		return writeError(c, http.StatusNotFound, "ssh disabled")
	}
	if !s.allowSSHMint(principal.ID, namespace, name) {
		log.Printf("spritz ssh: rate limit name=%s namespace=%s user_id=%s", name, namespace, principal.ID)
		return writeError(c, http.StatusTooManyRequests, "rate limit exceeded")
	}

	principalName := formatSSHPrincipal(s.sshGateway.principalPrefix, namespace, name)
	cert, err := s.signSSHCert(pubKey, principalName, principal.ID)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "failed to issue cert")
	}

	knownHosts := formatKnownHosts(s.sshGateway.publicHost, s.sshGateway.publicPort, s.sshGateway.hostPublicKey)
	expiresAt := time.Unix(int64(cert.ValidBefore), 0).UTC().Format(time.RFC3339)
	log.Printf("spritz ssh: cert issued name=%s namespace=%s user_id=%s expires_at=%s", name, namespace, principal.ID, expiresAt)
	resp := sshMintResponse{
		Host:       s.sshGateway.publicHost,
		Port:       s.sshGateway.publicPort,
		User:       principalName,
		Cert:       string(ssh.MarshalAuthorizedKey(cert)),
		KnownHosts: knownHosts,
		ExpiresAt:  expiresAt,
	}
	return writeJSON(c, http.StatusOK, resp)
}

func (s *server) signSSHCert(pubKey ssh.PublicKey, principalName, keyID string) (*ssh.Certificate, error) {
	now := time.Now().UTC()
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	cert := &ssh.Certificate{
		Key:             pubKey,
		Serial:          serial,
		CertType:        ssh.UserCert,
		KeyId:           fmt.Sprintf("spritz:%s", keyID),
		ValidPrincipals: []string{principalName},
		ValidAfter:      uint64(now.Add(-30 * time.Second).Unix()),
		ValidBefore:     uint64(now.Add(s.sshGateway.certTTL).Unix()),
		Permissions: ssh.Permissions{
			Extensions: map[string]string{
				"permit-pty": "",
			},
		},
	}
	if err := cert.SignCert(rand.Reader, s.sshGateway.caSigner); err != nil {
		return nil, err
	}
	return cert, nil
}

func formatKnownHosts(host string, port int, key ssh.PublicKey) string {
	if host == "" || key == nil {
		return ""
	}
	hostValue := host
	if port != 22 {
		hostValue = fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s %s", hostValue, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))))
}

func (s *server) allowSSHMint(principalID, namespace, name string) bool {
	if s.sshMintLimiter == nil {
		return true
	}
	key := fmt.Sprintf("%s:%s/%s", principalID, namespace, name)
	return s.sshMintLimiter.Allow(key)
}

func isSSHEnabled(spec spritzv1.SpritzSpec) bool {
	if spec.SSH != nil && !spec.SSH.Enabled {
		return false
	}
	if spec.Features != nil && spec.Features.SSH != nil && !*spec.Features.SSH {
		return false
	}
	return true
}

func randomSerial() (uint64, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw[:]), nil
}
