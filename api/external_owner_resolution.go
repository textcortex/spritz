package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	externalOwnerIssuerAnnotationKey      = "spritz.sh/external-owner.issuer"
	externalOwnerProviderAnnotationKey    = "spritz.sh/external-owner.provider"
	externalOwnerTenantAnnotationKey      = "spritz.sh/external-owner.tenant"
	externalOwnerSubjectHashAnnotationKey = "spritz.sh/external-owner.subject-hash"
	externalOwnerResolvedAtAnnotationKey  = "spritz.sh/external-owner.resolved-at"

	defaultExternalOwnerResolverTimeout = 5 * time.Second
)

var externalOwnerProviderTokenPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

type ownerRef struct {
	Type     string `json:"type,omitempty"`
	ID       string `json:"id,omitempty"`
	Provider string `json:"provider,omitempty"`
	Tenant   string `json:"tenant,omitempty"`
	Subject  string `json:"subject,omitempty"`
}

type externalOwnerResolutionStatus string

const (
	externalOwnerResolved    externalOwnerResolutionStatus = "resolved"
	externalOwnerUnresolved  externalOwnerResolutionStatus = "unresolved"
	externalOwnerForbidden   externalOwnerResolutionStatus = "forbidden"
	externalOwnerAmbiguous   externalOwnerResolutionStatus = "ambiguous"
	externalOwnerUnavailable externalOwnerResolutionStatus = "unavailable"
)

type externalOwnerPolicy struct {
	PrincipalID      string
	Issuer           string
	URL              string
	AuthHeader       string
	AllowedProviders map[string]struct{}
	AllowedTenants   map[string]struct{}
	TenantRequired   map[string]struct{}
	Timeout          time.Duration
}

type externalOwnerPolicyInput struct {
	PrincipalID      string   `json:"principalId"`
	Issuer           string   `json:"issuer,omitempty"`
	URL              string   `json:"url"`
	AuthHeader       string   `json:"authHeader,omitempty"`
	AuthHeaderEnv    string   `json:"authHeaderEnv,omitempty"`
	AllowedProviders []string `json:"allowedProviders,omitempty"`
	AllowedTenants   []string `json:"allowedTenants,omitempty"`
	TenantRequired   []string `json:"tenantRequired,omitempty"`
	Timeout          string   `json:"timeout,omitempty"`
}

type externalOwnerResolution struct {
	Status      externalOwnerResolutionStatus `json:"status,omitempty"`
	OwnerID     string                        `json:"ownerId,omitempty"`
	Issuer      string
	Provider    string
	Tenant      string
	SubjectHash string
	ResolvedAt  time.Time
}

type externalOwnerResolver interface {
	ResolveExternalOwner(ctx context.Context, policy externalOwnerPolicy, principal principal, ref ownerRef, requestID string) (externalOwnerResolution, error)
}

type httpExternalOwnerResolver struct{}

type externalOwnerConfig struct {
	subjectHashKey []byte
	policies       map[string]externalOwnerPolicy
	resolver       externalOwnerResolver
}

type externalOwnerResolutionError struct {
	status   int
	code     string
	message  string
	provider string
	tenant   string
	subject  string
}

type externalOwnerResolverRequest struct {
	Issuer   string `json:"issuer"`
	Identity struct {
		Provider string `json:"provider"`
		Tenant   string `json:"tenant,omitempty"`
		Subject  string `json:"subject"`
	} `json:"identity"`
	RequestID string `json:"requestId,omitempty"`
}

type externalOwnerResolverResponse struct {
	Status  externalOwnerResolutionStatus `json:"status,omitempty"`
	OwnerID string                        `json:"ownerId,omitempty"`
}

func newExternalOwnerConfig() (externalOwnerConfig, error) {
	raw := strings.TrimSpace(os.Getenv("SPRITZ_EXTERNAL_OWNER_POLICIES_JSON"))
	if raw == "" {
		return externalOwnerConfig{}, nil
	}

	var inputs []externalOwnerPolicyInput
	if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
		return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: %w", err)
	}
	if len(inputs) == 0 {
		return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: at least one policy is required")
	}

	hashKey := strings.TrimSpace(os.Getenv("SPRITZ_EXTERNAL_OWNER_SUBJECT_HASH_KEY"))
	if hashKey == "" {
		return externalOwnerConfig{}, fmt.Errorf("SPRITZ_EXTERNAL_OWNER_SUBJECT_HASH_KEY is required when external owner policies are configured")
	}

	policies := make(map[string]externalOwnerPolicy, len(inputs))
	for index, input := range inputs {
		principalID := strings.TrimSpace(input.PrincipalID)
		if principalID == "" {
			return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].principalId is required", index)
		}
		if _, exists := policies[principalID]; exists {
			return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: duplicate principalId %q", principalID)
		}
		urlValue := strings.TrimSpace(input.URL)
		if urlValue == "" {
			return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].url is required", index)
		}
		if _, err := validateExternalOwnerResolverURL(urlValue); err != nil {
			return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].url %v", index, err)
		}
		allowedProviders := normalizeTokenSet(input.AllowedProviders)
		if len(allowedProviders) == 0 {
			return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].allowedProviders is required", index)
		}
		authHeader, err := resolveExternalOwnerAuthHeader(input)
		if err != nil {
			return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].%v", index, err)
		}
		timeout := defaultExternalOwnerResolverTimeout
		if strings.TrimSpace(input.Timeout) != "" {
			parsed, err := time.ParseDuration(strings.TrimSpace(input.Timeout))
			if err != nil {
				return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].timeout is invalid", index)
			}
			if parsed <= 0 {
				return externalOwnerConfig{}, fmt.Errorf("invalid SPRITZ_EXTERNAL_OWNER_POLICIES_JSON: policies[%d].timeout must be greater than zero", index)
			}
			timeout = parsed
		}
		policies[principalID] = externalOwnerPolicy{
			PrincipalID:      principalID,
			Issuer:           strings.TrimSpace(input.Issuer),
			URL:              urlValue,
			AuthHeader:       authHeader,
			AllowedProviders: allowedProviders,
			AllowedTenants:   normalizeTenantSet(input.AllowedTenants),
			TenantRequired:   normalizeTokenSet(input.TenantRequired),
			Timeout:          timeout,
		}
	}

	return externalOwnerConfig{
		subjectHashKey: []byte(hashKey),
		policies:       policies,
		resolver:       httpExternalOwnerResolver{},
	}, nil
}

func normalizeTokenSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		token := strings.ToLower(strings.TrimSpace(value))
		if token == "" {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func resolveExternalOwnerAuthHeader(input externalOwnerPolicyInput) (string, error) {
	literal := strings.TrimSpace(input.AuthHeader)
	envName := strings.TrimSpace(input.AuthHeaderEnv)
	if literal != "" && envName != "" {
		return "", fmt.Errorf("only one of authHeader or authHeaderEnv may be set")
	}
	if envName == "" {
		return literal, nil
	}
	value := strings.TrimSpace(os.Getenv(envName))
	if value == "" {
		return "", fmt.Errorf("authHeaderEnv %q is empty", envName)
	}
	if strings.ContainsAny(value, " \t") {
		return value, nil
	}
	return "Bearer " + value, nil
}

func validateExternalOwnerResolverURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("must use http or https")
	}
	if strings.TrimSpace(parsed.Host) == "" {
		return nil, fmt.Errorf("must include a host")
	}
	return parsed, nil
}

func normalizeStringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		token := strings.TrimSpace(value)
		if token == "" {
			continue
		}
		out[token] = struct{}{}
	}
	return out
}

func normalizeTenantSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		token := strings.TrimSpace(value)
		if token == "" {
			continue
		}
		if parsed, err := uuid.Parse(token); err == nil {
			token = parsed.String()
		}
		out[token] = struct{}{}
	}
	return out
}

func (c externalOwnerConfig) enabled() bool {
	return c.resolver != nil && len(c.policies) > 0
}

func (c externalOwnerConfig) policyForPrincipal(principal principal) (externalOwnerPolicy, bool) {
	if len(c.policies) == 0 {
		return externalOwnerPolicy{}, false
	}
	policy, ok := c.policies[strings.TrimSpace(principal.ID)]
	return policy, ok
}

func (c externalOwnerConfig) resolve(ctx context.Context, principal principal, ref ownerRef, requestID string) (externalOwnerResolution, error) {
	policy, ok := c.policyForPrincipal(principal)
	if !ok {
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusForbidden,
			code:     "external_identity_forbidden",
			message:  "external identity resolution is not allowed for this principal",
			provider: strings.TrimSpace(ref.Provider),
			tenant:   strings.TrimSpace(ref.Tenant),
			subject:  strings.TrimSpace(ref.Subject),
		}
	}
	normalized, err := normalizeExternalOwnerRef(ref)
	if err != nil {
		return externalOwnerResolution{}, err
	}
	if _, ok := policy.AllowedProviders[normalized.Provider]; !ok {
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusForbidden,
			code:     "external_identity_forbidden",
			message:  "provider is not allowed for this principal",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	}
	tenantRequired := policy.requiresTenant(normalized.Provider)
	if normalized.Tenant == "" && tenantRequired {
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusForbidden,
			code:     "external_identity_forbidden",
			message:  "tenant is required for this principal",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	}
	if len(policy.AllowedTenants) > 0 {
		if normalized.Tenant != "" {
			if _, ok := policy.AllowedTenants[normalized.Tenant]; !ok {
				return externalOwnerResolution{}, externalOwnerResolutionError{
					status:   http.StatusForbidden,
					code:     "external_identity_forbidden",
					message:  "tenant is not allowed for this principal",
					provider: normalized.Provider,
					tenant:   normalized.Tenant,
					subject:  normalized.Subject,
				}
			}
		} else if tenantRequired {
			return externalOwnerResolution{}, externalOwnerResolutionError{
				status:   http.StatusForbidden,
				code:     "external_identity_forbidden",
				message:  "tenant is required for this principal",
				provider: normalized.Provider,
				tenant:   normalized.Tenant,
				subject:  normalized.Subject,
			}
		}
	}

	resolution, err := c.resolver.ResolveExternalOwner(ctx, policy, principal, normalized, requestID)
	if err != nil {
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusServiceUnavailable,
			code:     "external_identity_resolution_unavailable",
			message:  "external identity resolution is unavailable",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	}

	resolution.Issuer = policy.issuer()
	resolution.Provider = normalized.Provider
	resolution.Tenant = normalized.Tenant
	resolution.SubjectHash = c.subjectHash(normalized.Provider, normalized.Tenant, normalized.Subject)
	if resolution.ResolvedAt.IsZero() {
		resolution.ResolvedAt = time.Now().UTC()
	}
	switch resolution.Status {
	case externalOwnerResolved:
		if strings.TrimSpace(resolution.OwnerID) == "" {
			return externalOwnerResolution{}, externalOwnerResolutionError{
				status:   http.StatusServiceUnavailable,
				code:     "external_identity_resolution_unavailable",
				message:  "external identity resolution returned an invalid owner",
				provider: normalized.Provider,
				tenant:   normalized.Tenant,
				subject:  normalized.Subject,
			}
		}
		return resolution, nil
	case externalOwnerUnresolved:
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusConflict,
			code:     "external_identity_unresolved",
			message:  "external identity is unresolved",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	case externalOwnerForbidden:
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusForbidden,
			code:     "external_identity_forbidden",
			message:  "external identity resolution is forbidden",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	case externalOwnerAmbiguous:
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusConflict,
			code:     "external_identity_ambiguous",
			message:  "external identity is ambiguous",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	default:
		return externalOwnerResolution{}, externalOwnerResolutionError{
			status:   http.StatusServiceUnavailable,
			code:     "external_identity_resolution_unavailable",
			message:  "external identity resolution is unavailable",
			provider: normalized.Provider,
			tenant:   normalized.Tenant,
			subject:  normalized.Subject,
		}
	}
}

func (p externalOwnerPolicy) issuer() string {
	if issuer := strings.TrimSpace(p.Issuer); issuer != "" {
		return issuer
	}
	return strings.TrimSpace(p.PrincipalID)
}

func (p externalOwnerPolicy) requiresTenant(provider string) bool {
	if len(p.TenantRequired) == 0 {
		return false
	}
	_, ok := p.TenantRequired[strings.ToLower(strings.TrimSpace(provider))]
	return ok
}

func normalizeExternalOwnerRef(ref ownerRef) (ownerRef, error) {
	normalized := ownerRef{
		Type:     strings.ToLower(strings.TrimSpace(ref.Type)),
		ID:       strings.TrimSpace(ref.ID),
		Provider: strings.ToLower(strings.TrimSpace(ref.Provider)),
		Tenant:   strings.TrimSpace(ref.Tenant),
		Subject:  strings.TrimSpace(ref.Subject),
	}
	if normalized.Type != "external" {
		return normalized, fmt.Errorf("ownerRef.type must be external")
	}
	if normalized.Provider == "" {
		return normalized, fmt.Errorf("ownerRef.provider is required")
	}
	if !externalOwnerProviderTokenPattern.MatchString(normalized.Provider) {
		return normalized, fmt.Errorf("ownerRef.provider must be a normalized lower-case token")
	}
	if normalized.Subject == "" {
		return normalized, fmt.Errorf("ownerRef.subject is required")
	}
	if normalized.Tenant != "" {
		if tenantID, err := uuid.Parse(normalized.Tenant); err == nil {
			normalized.Tenant = tenantID.String()
		}
	}

	switch normalized.Provider {
	case "msteams":
		if normalized.Tenant == "" {
			return normalized, fmt.Errorf("ownerRef.tenant is required for msteams")
		}
		if _, err := uuid.Parse(normalized.Tenant); err != nil {
			return normalized, fmt.Errorf("ownerRef.tenant must be a valid UUID for msteams")
		}
		subjectID, err := uuid.Parse(normalized.Subject)
		if err != nil {
			return normalized, fmt.Errorf("ownerRef.subject must be a valid UUID for msteams")
		}
		normalized.Subject = subjectID.String()
	}

	return normalized, nil
}

func (r httpExternalOwnerResolver) ResolveExternalOwner(ctx context.Context, policy externalOwnerPolicy, _ principal, ref ownerRef, requestID string) (externalOwnerResolution, error) {
	timeout := policy.Timeout
	if timeout <= 0 {
		timeout = defaultExternalOwnerResolverTimeout
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	requestBody := externalOwnerResolverRequest{
		Issuer:    policy.issuer(),
		RequestID: strings.TrimSpace(requestID),
	}
	requestBody.Identity.Provider = ref.Provider
	requestBody.Identity.Tenant = ref.Tenant
	requestBody.Identity.Subject = ref.Subject

	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return externalOwnerResolution{}, err
	}

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, policy.URL, bytes.NewReader(encoded))
	if err != nil {
		return externalOwnerResolution{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader := strings.TrimSpace(policy.AuthHeader); authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return externalOwnerResolution{}, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusForbidden:
		return externalOwnerResolution{Status: externalOwnerForbidden}, nil
	case http.StatusNotFound:
		return externalOwnerResolution{Status: externalOwnerUnresolved}, nil
	case http.StatusConflict:
		return externalOwnerResolution{Status: externalOwnerAmbiguous}, nil
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return externalOwnerResolution{Status: externalOwnerUnavailable}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return externalOwnerResolution{}, fmt.Errorf("resolver returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return externalOwnerResolution{}, err
	}
	payload := externalOwnerResolverResponse{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return externalOwnerResolution{}, err
	}
	return externalOwnerResolution{
		Status:  payload.Status,
		OwnerID: strings.TrimSpace(payload.OwnerID),
	}, nil
}

func (c externalOwnerConfig) subjectHash(provider, tenant, subject string) string {
	mac := hmac.New(sha256.New, c.subjectHashKey)
	_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(provider))))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(strings.TrimSpace(tenant)))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(strings.TrimSpace(subject)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (e externalOwnerResolutionError) Error() string {
	return e.message
}

func (e externalOwnerResolutionError) responseData() map[string]any {
	data := map[string]any{
		"message": e.message,
		"error":   e.code,
		"identity": map[string]string{
			"provider": e.provider,
			"subject":  e.subject,
		},
	}
	if strings.TrimSpace(e.tenant) != "" {
		data["identity"].(map[string]string)["tenant"] = e.tenant
	}
	return data
}
