package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MicahParks/keyfunc"
	"github.com/golang-jwt/jwt/v4"
	"github.com/labstack/echo/v4"
)

const (
	ownerLabelKey = "spritz.sh/owner"
	nameLabelKey  = "spritz.sh/name"
)

type authMode string

const (
	authModeNone   authMode = "none"
	authModeHeader authMode = "header"
	authModeBearer authMode = "bearer"
	authModeAuto   authMode = "auto"
)

var (
	errUnauthenticated = errors.New("unauthenticated")
	errForbidden       = errors.New("forbidden")
	errInvalidAuthMode = errors.New("invalid auth mode")
)

type authConfig struct {
	mode                      authMode
	headerID                  string
	headerEmail               string
	headerTeams               string
	adminIDs                  map[string]struct{}
	adminTeams                map[string]struct{}
	bearerIntrospectionURL    string
	bearerIntrospectionAuth   string
	bearerMethod              string
	bearerTimeout             time.Duration
	bearerTokenParam          string
	bearerForwardToken        bool
	bearerIDPaths             []string
	bearerEmailPaths          []string
	bearerTeamsPaths          []string
	bearerAuthorizationHeader string
	bearerJWKSURL             string
	bearerJWKSIssuer          string
	bearerJWKSAudiences       []string
	bearerJWKSAlgos           []string
	bearerJWKSLeeway          time.Duration
	bearerJWKSFallback        bool
	bearerJWKSRefreshInterval time.Duration
	bearerJWKSRefreshTimeout  time.Duration
	bearerJWKSRateLimit       time.Duration
	bearerJWKSInitErr         error
	bearerJWKSInitLock        sync.Mutex
	bearerJWKSLastAttempt     time.Time
	bearerJWKS                *keyfunc.JWKS
}

type principal struct {
	ID      string
	Email   string
	Teams   []string
	IsAdmin bool
}

func newAuthConfig() authConfig {
	return authConfig{
		mode:                      normalizeAuthMode(os.Getenv("SPRITZ_AUTH_MODE")),
		headerID:                  envOrDefault("SPRITZ_AUTH_HEADER_ID", "X-Spritz-User-Id"),
		headerEmail:               envOrDefault("SPRITZ_AUTH_HEADER_EMAIL", "X-Spritz-User-Email"),
		headerTeams:               envOrDefault("SPRITZ_AUTH_HEADER_TEAMS", "X-Spritz-User-Teams"),
		adminIDs:                  splitSet(os.Getenv("SPRITZ_AUTH_ADMIN_IDS")),
		adminTeams:                splitSet(os.Getenv("SPRITZ_AUTH_ADMIN_TEAMS")),
		bearerIntrospectionURL:    strings.TrimSpace(os.Getenv("SPRITZ_AUTH_BEARER_INTROSPECTION_URL")),
		bearerIntrospectionAuth:   strings.TrimSpace(os.Getenv("SPRITZ_AUTH_BEARER_INTROSPECTION_AUTH_HEADER")),
		bearerMethod:              strings.ToUpper(envOrDefault("SPRITZ_AUTH_BEARER_METHOD", "GET")),
		bearerTimeout:             parseDurationEnv("SPRITZ_AUTH_BEARER_TIMEOUT", 5*time.Second),
		bearerTokenParam:          envOrDefault("SPRITZ_AUTH_BEARER_TOKEN_PARAM", "token"),
		bearerForwardToken:        parseBoolEnv("SPRITZ_AUTH_BEARER_FORWARD_TOKEN", false),
		bearerIDPaths:             splitListOrDefault(os.Getenv("SPRITZ_AUTH_BEARER_ID_PATHS"), []string{"sub"}),
		bearerEmailPaths:          splitListOrDefault(os.Getenv("SPRITZ_AUTH_BEARER_EMAIL_PATHS"), []string{"email"}),
		bearerTeamsPaths:          splitListOrDefault(os.Getenv("SPRITZ_AUTH_BEARER_TEAMS_PATHS"), nil),
		bearerAuthorizationHeader: envOrDefault("SPRITZ_AUTH_BEARER_HEADER", "Authorization"),
		bearerJWKSURL:             strings.TrimSpace(os.Getenv("SPRITZ_AUTH_BEARER_JWKS_URL")),
		bearerJWKSIssuer:          strings.TrimSpace(os.Getenv("SPRITZ_AUTH_BEARER_ISSUER")),
		bearerJWKSAudiences:       splitList(os.Getenv("SPRITZ_AUTH_BEARER_AUDIENCES")),
		bearerJWKSAlgos:           splitListOrDefault(os.Getenv("SPRITZ_AUTH_BEARER_JWKS_ALGOS"), []string{"RS256"}),
		bearerJWKSLeeway:          parseDurationEnv("SPRITZ_AUTH_BEARER_JWKS_LEEWAY", 0),
		bearerJWKSFallback:        parseBoolEnv("SPRITZ_AUTH_BEARER_JWKS_FALLBACK", false),
		bearerJWKSRefreshInterval: parseDurationEnv("SPRITZ_AUTH_BEARER_JWKS_REFRESH_INTERVAL", 5*time.Minute),
		bearerJWKSRefreshTimeout:  parseDurationEnv("SPRITZ_AUTH_BEARER_JWKS_REFRESH_TIMEOUT", 5*time.Second),
		bearerJWKSRateLimit:       parseDurationEnv("SPRITZ_AUTH_BEARER_JWKS_RATE_LIMIT", 10*time.Second),
	}
}

func normalizeAuthMode(raw string) authMode {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", string(authModeNone):
		return authModeNone
	case string(authModeHeader):
		return authModeHeader
	case string(authModeBearer):
		return authModeBearer
	case string(authModeAuto), "header+bearer", "bearer+header", "header,bearer", "bearer,header":
		return authModeAuto
	default:
		return authMode(mode)
	}
}

func (a *authConfig) enabled() bool {
	return a.mode != authModeNone
}

func (a *authConfig) principal(r *http.Request) (principal, error) {
	if !a.enabled() {
		return principal{}, nil
	}

	switch a.mode {
	case authModeHeader:
		id := strings.TrimSpace(r.Header.Get(a.headerID))
		if id == "" {
			return principal{}, errUnauthenticated
		}
		email := strings.TrimSpace(r.Header.Get(a.headerEmail))
		teams := splitList(r.Header.Get(a.headerTeams))
		return principal{
			ID:      id,
			Email:   email,
			Teams:   teams,
			IsAdmin: a.isAdmin(id, teams),
		}, nil
	case authModeAuto:
		id := strings.TrimSpace(r.Header.Get(a.headerID))
		if id != "" {
			email := strings.TrimSpace(r.Header.Get(a.headerEmail))
			teams := splitList(r.Header.Get(a.headerTeams))
			return principal{
				ID:      id,
				Email:   email,
				Teams:   teams,
				IsAdmin: a.isAdmin(id, teams),
			}, nil
		}
		if a.bearerIntrospectionURL == "" && a.bearerJWKSURL == "" {
			return principal{}, errUnauthenticated
		}
		return a.principalFromBearer(r)
	case authModeBearer:
		return a.principalFromBearer(r)
	case authModeNone:
		return principal{}, nil
	default:
		return principal{}, errInvalidAuthMode
	}
}

func (a *authConfig) principalFromBearer(r *http.Request) (principal, error) {
	token := extractBearerToken(r.Header.Get(a.bearerAuthorizationHeader))
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get(a.bearerTokenParam))
	}
	if token == "" {
		return principal{}, errUnauthenticated
	}
	if a.bearerJWKSURL != "" {
		if resolved, err := a.principalFromJWT(r.Context(), token); err == nil {
			return resolved, nil
		} else if !a.bearerJWKSFallback || a.bearerIntrospectionURL == "" {
			return principal{}, err
		}
	}
	if a.bearerIntrospectionURL == "" {
		return principal{}, errInvalidAuthMode
	}
	return a.introspectToken(r.Context(), token)
}

func (a *authConfig) introspectToken(ctx context.Context, token string) (principal, error) {
	client := &http.Client{Timeout: a.bearerTimeout}
	endpoint := a.bearerIntrospectionURL
	var body io.Reader
	if a.bearerMethod == http.MethodPost {
		data := url.Values{}
		data.Set(a.bearerTokenParam, token)
		body = strings.NewReader(data.Encode())
	} else {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return principal{}, err
		}
		query := parsed.Query()
		query.Set(a.bearerTokenParam, token)
		parsed.RawQuery = query.Encode()
		endpoint = parsed.String()
		body = nil
	}
	req, err := http.NewRequestWithContext(ctx, a.bearerMethod, endpoint, body)
	if err != nil {
		return principal{}, err
	}
	if a.bearerMethod == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if a.bearerIntrospectionAuth != "" {
		req.Header.Set("Authorization", a.bearerIntrospectionAuth)
	} else if a.bearerForwardToken {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return principal{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return principal{}, errUnauthenticated
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return principal{}, fmt.Errorf("introspection failed: %s", resp.Status)
	}

	payload := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return principal{}, err
	}

	id := firstStringPath(payload, a.bearerIDPaths)
	if id == "" {
		return principal{}, errUnauthenticated
	}

	email := firstStringPath(payload, a.bearerEmailPaths)
	teams := firstStringListPath(payload, a.bearerTeamsPaths)

	return principal{
		ID:      id,
		Email:   email,
		Teams:   teams,
		IsAdmin: a.isAdmin(id, teams),
	}, nil
}

func (a *authConfig) jwks() (*keyfunc.JWKS, error) {
	if a.bearerJWKS != nil {
		return a.bearerJWKS, nil
	}
	if a.bearerJWKSURL == "" {
		return nil, errInvalidAuthMode
	}
	a.bearerJWKSInitLock.Lock()
	defer a.bearerJWKSInitLock.Unlock()
	if a.bearerJWKS != nil {
		return a.bearerJWKS, nil
	}
	now := time.Now()
	if !a.bearerJWKSLastAttempt.IsZero() && a.bearerJWKSRateLimit > 0 {
		if now.Sub(a.bearerJWKSLastAttempt) < a.bearerJWKSRateLimit {
			if a.bearerJWKSInitErr != nil {
				return nil, a.bearerJWKSInitErr
			}
		}
	}
	a.bearerJWKSLastAttempt = now
	opts := keyfunc.Options{
		RefreshInterval:   a.bearerJWKSRefreshInterval,
		RefreshRateLimit:  a.bearerJWKSRateLimit,
		RefreshTimeout:    a.bearerJWKSRefreshTimeout,
		RefreshUnknownKID: true,
	}
	jwks, err := keyfunc.Get(a.bearerJWKSURL, opts)
	if err != nil {
		a.bearerJWKSInitErr = err
		return nil, err
	}
	a.bearerJWKSInitErr = nil
	a.bearerJWKS = jwks
	if a.bearerJWKSInitErr != nil {
		return nil, a.bearerJWKSInitErr
	}
	if a.bearerJWKS == nil {
		return nil, errInvalidAuthMode
	}
	return a.bearerJWKS, nil
}

func (a *authConfig) principalFromJWT(ctx context.Context, token string) (principal, error) {
	jwks, err := a.jwks()
	if err != nil {
		return principal{}, err
	}
	parser := &jwt.Parser{
		ValidMethods:         a.bearerJWKSAlgos,
		SkipClaimsValidation: true,
	}
	claims := jwt.MapClaims{}
	parsed, err := parser.ParseWithClaims(token, claims, jwks.Keyfunc)
	if err != nil || !parsed.Valid {
		return principal{}, errUnauthenticated
	}
	if err := validateJWTTimeClaims(claims, a.bearerJWKSLeeway); err != nil {
		return principal{}, errUnauthenticated
	}
	if issuer := a.bearerJWKSIssuer; issuer != "" {
		if claim, ok := claims["iss"].(string); !ok || claim != issuer {
			return principal{}, errUnauthenticated
		}
	}
	if len(a.bearerJWKSAudiences) > 0 && !verifyAudience(claims, a.bearerJWKSAudiences) {
		return principal{}, errUnauthenticated
	}
	id := firstStringPath(claims, a.bearerIDPaths)
	if id == "" {
		return principal{}, errUnauthenticated
	}
	email := firstStringPath(claims, a.bearerEmailPaths)
	teams := firstStringListPath(claims, a.bearerTeamsPaths)
	return principal{
		ID:      id,
		Email:   email,
		Teams:   teams,
		IsAdmin: a.isAdmin(id, teams),
	}, nil
}

func verifyAudience(claims jwt.MapClaims, audiences []string) bool {
	if len(audiences) == 0 {
		return true
	}
	raw, ok := claims["aud"]
	if !ok {
		return false
	}
	switch value := raw.(type) {
	case string:
		return containsStringExact(audiences, value)
	case []string:
		for _, item := range value {
			if containsStringExact(audiences, item) {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if s, ok := item.(string); ok && containsStringExact(audiences, s) {
				return true
			}
		}
	}
	return false
}

func containsStringExact(values []string, value string) bool {
	for _, item := range values {
		if strings.TrimSpace(item) == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

func validateJWTTimeClaims(claims jwt.MapClaims, leeway time.Duration) error {
	now := time.Now()
	if expRaw, ok := claims["exp"]; ok {
		exp, ok := parseNumericDate(expRaw)
		if !ok {
			return errUnauthenticated
		}
		if now.After(exp.Add(leeway)) {
			return errUnauthenticated
		}
	}
	if nbfRaw, ok := claims["nbf"]; ok {
		nbf, ok := parseNumericDate(nbfRaw)
		if !ok {
			return errUnauthenticated
		}
		if now.Add(leeway).Before(nbf) {
			return errUnauthenticated
		}
	}
	return nil
}

func parseNumericDate(value any) (time.Time, bool) {
	switch raw := value.(type) {
	case float64:
		return time.Unix(int64(raw), 0), true
	case int64:
		return time.Unix(raw, 0), true
	case int:
		return time.Unix(int64(raw), 0), true
	case json.Number:
		parsed, err := raw.Int64()
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(parsed, 0), true
	case string:
		// Some providers return numeric date claims as strings; accept for compatibility.
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return time.Time{}, false
		}
		return time.Unix(parsed, 0), true
	default:
		return time.Time{}, false
	}
}

func extractBearerToken(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func (a *authConfig) isAdmin(id string, teams []string) bool {
	if _, ok := a.adminIDs[id]; ok {
		return true
	}
	for _, team := range teams {
		if _, ok := a.adminTeams[team]; ok {
			return true
		}
	}
	return false
}

func writeAuthError(c echo.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errUnauthenticated) {
		return writeError(c, http.StatusUnauthorized, "unauthenticated")
	}
	if errors.Is(err, errForbidden) {
		return writeError(c, http.StatusForbidden, "forbidden")
	}
	return writeError(c, http.StatusInternalServerError, err.Error())
}

func ownerLabelValue(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("owner-%x", sum[:16])
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func splitSet(value string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range splitList(value) {
		out[item] = struct{}{}
	}
	return out
}

func splitList(value string) []string {
	if value == "" {
		return nil
	}
	raw := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';'
	})
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func splitListOrDefault(value string, fallback []string) []string {
	items := splitList(value)
	if len(items) == 0 {
		return fallback
	}
	return items
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func parseIntEnvAllowZero(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func parseBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstStringPath(payload map[string]any, paths []string) string {
	for _, path := range paths {
		value, ok := lookupPath(payload, path)
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if typed != "" {
				return typed
			}
		}
	}
	return ""
}

func firstStringListPath(payload map[string]any, paths []string) []string {
	for _, path := range paths {
		value, ok := lookupPath(payload, path)
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return typed
		case []any:
			items := make([]string, 0, len(typed))
			for _, item := range typed {
				if s, ok := item.(string); ok && s != "" {
					items = append(items, s)
				}
			}
			if len(items) > 0 {
				return items
			}
		case string:
			return splitList(typed)
		}
	}
	return nil
}

func lookupPath(payload map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	segments := strings.Split(path, ".")
	var current any = payload
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return nil, false
		}
		asMap, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		next, ok := asMap[segment]
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}
