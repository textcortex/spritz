package main

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

type githubTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

func (r *spritzReconciler) githubAppInstallationToken(ctx context.Context, repo string) (string, *time.Time, error) {
	privateKey, err := r.githubAppPrivateKey(ctx)
	if err != nil {
		return "", nil, err
	}
	jwtToken, err := githubAppJWT(r.Config.AppID, privateKey)
	if err != nil {
		return "", nil, err
	}

	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", strings.TrimRight(r.Config.APIURL, "/"), r.Config.InstallationID)
	repoName := repoNameFromPath(repo)
	payload := struct {
		Repositories []string `json:"repositories,omitempty"`
	}{
		Repositories: []string{repoName},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := r.HTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		payload, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return "", nil, fmt.Errorf("github app token request failed: status=%d (body read error: %w)", resp.StatusCode, readErr)
		}
		return "", nil, fmt.Errorf("github app token request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var parsed githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", nil, err
	}
	if parsed.Token == "" {
		return "", nil, fmt.Errorf("github app token response missing token")
	}
	var expiry *time.Time
	if parsed.ExpiresAt != "" {
		if ts, err := time.Parse(time.RFC3339, parsed.ExpiresAt); err == nil {
			expiry = &ts
		}
	}
	return parsed.Token, expiry, nil
}

func (r *spritzReconciler) githubAppPrivateKey(ctx context.Context) (*rsa.PrivateKey, error) {
	secret := &corev1.Secret{}
	key := types.NamespacedName{
		Name:      r.Config.PrivateKeySecret,
		Namespace: r.Config.PrivateKeyNamespace,
	}
	if err := r.Get(ctx, key, secret); err != nil {
		return nil, err
	}
	raw, ok := secret.Data[r.Config.PrivateKeyKey]
	if !ok {
		return nil, fmt.Errorf("github app private key not found in secret")
	}
	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(raw)
	if err != nil {
		return nil, err
	}
	return privateKey, nil
}

func githubAppJWT(appID int64, privateKey *rsa.PrivateKey) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Issuer:    fmt.Sprintf("%d", appID),
		IssuedAt:  jwt.NewNumericDate(now.Add(-1 * time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(privateKey)
}

func repoNameFromPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}
