package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/golang-jwt/jwt/v4"
)

func TestPrincipalFromJWT_Success(t *testing.T) {
	jwks, key, kid := newTestJWKS(t)
	now := time.Now()
	token := signJWT(t, key, kid, jwt.MapClaims{
		"sub":   "user-123",
		"email": "user@example.com",
		"iss":   "https://issuer.test",
		"aud":   "spritz",
		"exp":   now.Add(30 * time.Minute).Unix(),
		"nbf":   now.Add(-1 * time.Minute).Unix(),
	})

	cfg := authConfig{
		bearerJWKS:         jwks,
		bearerJWKSAlgos:    []string{jwt.SigningMethodRS256.Alg()},
		bearerJWKSIssuer:   "https://issuer.test",
		bearerJWKSAudiences: []string{"spritz"},
		bearerIDPaths:      []string{"sub"},
		bearerEmailPaths:   []string{"email"},
	}

	principal, err := cfg.principalFromJWT(context.Background(), token)
	if err != nil {
		t.Fatalf("expected jwt to validate, got error: %v", err)
	}
	if principal.ID != "user-123" {
		t.Fatalf("expected ID user-123, got %q", principal.ID)
	}
	if principal.Email != "user@example.com" {
		t.Fatalf("expected email user@example.com, got %q", principal.Email)
	}
}

func TestPrincipalFromJWT_AudienceMismatch(t *testing.T) {
	jwks, key, kid := newTestJWKS(t)
	now := time.Now()
	token := signJWT(t, key, kid, jwt.MapClaims{
		"sub": "user-123",
		"iss": "https://issuer.test",
		"aud": "other",
		"exp": now.Add(10 * time.Minute).Unix(),
	})

	cfg := authConfig{
		bearerJWKS:         jwks,
		bearerJWKSAlgos:    []string{jwt.SigningMethodRS256.Alg()},
		bearerJWKSIssuer:   "https://issuer.test",
		bearerJWKSAudiences: []string{"spritz"},
		bearerIDPaths:      []string{"sub"},
	}

	if _, err := cfg.principalFromJWT(context.Background(), token); err == nil {
		t.Fatalf("expected audience mismatch to fail")
	}
}

func TestPrincipalFromJWT_Expired(t *testing.T) {
	jwks, key, kid := newTestJWKS(t)
	now := time.Now()
	token := signJWT(t, key, kid, jwt.MapClaims{
		"sub": "user-123",
		"iss": "https://issuer.test",
		"aud": "spritz",
		"exp": now.Add(-5 * time.Minute).Unix(),
	})

	cfg := authConfig{
		bearerJWKS:         jwks,
		bearerJWKSAlgos:    []string{jwt.SigningMethodRS256.Alg()},
		bearerJWKSIssuer:   "https://issuer.test",
		bearerJWKSAudiences: []string{"spritz"},
		bearerIDPaths:      []string{"sub"},
	}

	if _, err := cfg.principalFromJWT(context.Background(), token); err == nil {
		t.Fatalf("expected expired token to fail")
	}
}

func TestVerifyAudience_CaseSensitive(t *testing.T) {
	claims := jwt.MapClaims{"aud": "Spritz"}
	if verifyAudience(claims, []string{"spritz"}) {
		t.Fatalf("expected audience match to be case-sensitive")
	}
	if !verifyAudience(claims, []string{"Spritz"}) {
		t.Fatalf("expected exact audience match to succeed")
	}
}

func newTestJWKS(t *testing.T) (*keyfunc.JWKS, *rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	kid := "test-kid"
	given := map[string]keyfunc.GivenKey{
		kid: keyfunc.NewGivenRSACustomWithOptions(&key.PublicKey, keyfunc.GivenKeyOptions{
			Algorithm: jwt.SigningMethodRS256.Alg(),
		}),
	}
	return keyfunc.NewGiven(given), key, kid
}

func signJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}
