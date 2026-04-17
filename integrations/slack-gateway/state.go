package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type oauthStateManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

type statePayloadType string

const (
	statePayloadTypeOAuth          statePayloadType = "oauth"
	statePayloadTypePendingInstall statePayloadType = "pending_install"
)

type pendingInstallState struct {
	RequestID    string            `json:"requestId"`
	Installation slackInstallation `json:"installation"`
}

type oauthStatePayload struct {
	Type           statePayloadType     `json:"type"`
	IssuedAt       int64                `json:"iat"`
	Nonce          string               `json:"nonce"`
	PendingInstall *pendingInstallState `json:"pendingInstall,omitempty"`
}

func newOAuthStateManager(secret string, ttl time.Duration) *oauthStateManager {
	return &oauthStateManager{
		secret: []byte(strings.TrimSpace(secret)),
		ttl:    ttl,
		now:    time.Now,
	}
}

func (m *oauthStateManager) generate() (string, error) {
	return m.generatePayload(oauthStatePayload{Type: statePayloadTypeOAuth})
}

func (m *oauthStateManager) generatePendingInstall(state pendingInstallState) (string, error) {
	return m.generatePayload(oauthStatePayload{
		Type:           statePayloadTypePendingInstall,
		PendingInstall: &state,
	})
}

func (m *oauthStateManager) generatePayload(payload oauthStatePayload) (string, error) {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	payload.IssuedAt = m.now().UTC().Unix()
	payload.Nonce = hex.EncodeToString(nonceBytes)
	if payload.Type == "" {
		payload.Type = statePayloadTypeOAuth
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(m.key())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	sealNonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(sealNonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, sealNonce, encoded, nil)
	token := append(sealNonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func (m *oauthStateManager) validate(raw string) error {
	payload, err := m.parse(raw)
	if err != nil {
		return err
	}
	if payload.Type != statePayloadTypeOAuth {
		return errors.New("state type is invalid")
	}
	return nil
}

func (m *oauthStateManager) parsePendingInstall(raw string) (pendingInstallState, error) {
	payload, err := m.parse(raw)
	if err != nil {
		return pendingInstallState{}, err
	}
	if payload.Type != statePayloadTypePendingInstall || payload.PendingInstall == nil {
		return pendingInstallState{}, errors.New("state type is invalid")
	}
	return *payload.PendingInstall, nil
}

func (m *oauthStateManager) parse(raw string) (oauthStatePayload, error) {
	token, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return oauthStatePayload{}, errors.New("state is invalid")
	}
	block, err := aes.NewCipher(m.key())
	if err != nil {
		return oauthStatePayload{}, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return oauthStatePayload{}, err
	}
	if len(token) < gcm.NonceSize() {
		return oauthStatePayload{}, errors.New("state is invalid")
	}
	payloadBytes, err := gcm.Open(nil, token[:gcm.NonceSize()], token[gcm.NonceSize():], nil)
	if err != nil {
		return oauthStatePayload{}, errors.New("state is invalid")
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return oauthStatePayload{}, errors.New("state payload is invalid")
	}
	if payload.IssuedAt <= 0 || strings.TrimSpace(payload.Nonce) == "" || payload.Type == "" {
		return oauthStatePayload{}, errors.New("state payload is incomplete")
	}
	if issuedAt := time.Unix(payload.IssuedAt, 0).UTC(); m.now().UTC().Sub(issuedAt) > m.ttl {
		return oauthStatePayload{}, fmt.Errorf("state has expired")
	}
	return payload, nil
}

func (m *oauthStateManager) key() []byte {
	sum := sha256.Sum256(m.secret)
	return sum[:]
}
