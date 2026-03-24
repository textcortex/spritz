package main

import (
	"crypto/hmac"
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

type oauthStatePayload struct {
	IssuedAt int64  `json:"iat"`
	Nonce    string `json:"nonce"`
}

func newOAuthStateManager(secret string, ttl time.Duration) *oauthStateManager {
	return &oauthStateManager{
		secret: []byte(strings.TrimSpace(secret)),
		ttl:    ttl,
		now:    time.Now,
	}
}

func (m *oauthStateManager) generate() (string, error) {
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return "", err
	}
	payload := oauthStatePayload{
		IssuedAt: m.now().UTC().Unix(),
		Nonce:    hex.EncodeToString(nonceBytes),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	blob := base64.RawURLEncoding.EncodeToString(encoded)
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(blob))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return blob + "." + signature, nil
}

func (m *oauthStateManager) validate(raw string) error {
	parts := strings.Split(strings.TrimSpace(raw), ".")
	if len(parts) != 2 {
		return errors.New("state is invalid")
	}
	mac := hmac.New(sha256.New, m.secret)
	_, _ = mac.Write([]byte(parts[0]))
	expected := mac.Sum(nil)
	actual, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(expected, actual) {
		return errors.New("state signature is invalid")
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errors.New("state payload is invalid")
	}
	var payload oauthStatePayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return errors.New("state payload is invalid")
	}
	if payload.IssuedAt <= 0 || strings.TrimSpace(payload.Nonce) == "" {
		return errors.New("state payload is incomplete")
	}
	if issuedAt := time.Unix(payload.IssuedAt, 0).UTC(); m.now().UTC().Sub(issuedAt) > m.ttl {
		return fmt.Errorf("state has expired")
	}
	return nil
}
