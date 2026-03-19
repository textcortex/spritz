package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	defaultExtensionResolverTimeout = 5 * time.Second
	extensionsEnvKey                = "SPRITZ_EXTENSIONS_JSON"
)

type extensionKind string

const (
	extensionKindResolver      extensionKind = "resolver"
	extensionKindAuthProvider  extensionKind = "auth_provider"
	extensionKindLifecycleHook extensionKind = "lifecycle_hook"
)

type extensionOperation string

const (
	extensionOperationOwnerResolve        extensionOperation = "owner.resolve"
	extensionOperationPresetCreateResolve extensionOperation = "preset.create.resolve"
	extensionOperationAuthLoginMetadata   extensionOperation = "auth.login.metadata"
	extensionOperationIdentityLinkResolve extensionOperation = "identity.link.resolve"
	extensionOperationInstanceNotify      extensionOperation = "instance.lifecycle.notify"
)

type extensionResolverStatus string

const (
	extensionStatusResolved    extensionResolverStatus = "resolved"
	extensionStatusUnresolved  extensionResolverStatus = "unresolved"
	extensionStatusForbidden   extensionResolverStatus = "forbidden"
	extensionStatusAmbiguous   extensionResolverStatus = "ambiguous"
	extensionStatusInvalid     extensionResolverStatus = "invalid"
	extensionStatusUnavailable extensionResolverStatus = "unavailable"
)

type extensionTransportType string

const extensionTransportHTTP extensionTransportType = "http"

type extensionRegistry struct {
	resolvers []configuredResolver
}

type extensionConfigInput struct {
	ID        string                  `json:"id"`
	Kind      string                  `json:"kind"`
	Operation string                  `json:"operation"`
	Match     extensionMatchInput     `json:"match,omitempty"`
	Transport extensionTransportInput `json:"transport,omitempty"`
}

type extensionMatchInput struct {
	PrincipalIDs []string `json:"principalIds,omitempty"`
	PresetIDs    []string `json:"presetIds,omitempty"`
}

type extensionTransportInput struct {
	Type          string `json:"type,omitempty"`
	URL           string `json:"url,omitempty"`
	AuthHeader    string `json:"authHeader,omitempty"`
	AuthHeaderEnv string `json:"authHeaderEnv,omitempty"`
	Timeout       string `json:"timeout,omitempty"`
}

type extensionPrincipalPayload struct {
	ID      string   `json:"id,omitempty"`
	Type    string   `json:"type,omitempty"`
	Email   string   `json:"email,omitempty"`
	Subject string   `json:"subject,omitempty"`
	Issuer  string   `json:"issuer,omitempty"`
	Teams   []string `json:"teams,omitempty"`
	Scopes  []string `json:"scopes,omitempty"`
}

type extensionRequestContext struct {
	Namespace       string `json:"namespace,omitempty"`
	PresetID        string `json:"presetId,omitempty"`
	InstanceClassID string `json:"instanceClassId,omitempty"`
}

type extensionResolverRequestEnvelope struct {
	Version     string                    `json:"version"`
	ExtensionID string                    `json:"extensionId"`
	Kind        extensionKind             `json:"kind"`
	Operation   extensionOperation        `json:"operation"`
	RequestID   string                    `json:"requestId,omitempty"`
	Principal   extensionPrincipalPayload `json:"principal"`
	Context     extensionRequestContext   `json:"context,omitempty"`
	Input       any                       `json:"input,omitempty"`
}

type extensionResolverResponseEnvelope struct {
	Status    extensionResolverStatus    `json:"status,omitempty"`
	Output    json.RawMessage            `json:"output,omitempty"`
	Mutations extensionResolverMutations `json:"mutations,omitempty"`
}

type extensionResolverMutations struct {
	Spec        *extensionResolverSpecMutation `json:"spec,omitempty"`
	Annotations map[string]string              `json:"annotations,omitempty"`
	Labels      map[string]string              `json:"labels,omitempty"`
}

type extensionResolverSpecMutation struct {
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
}

type configuredResolver struct {
	id        string
	kind      extensionKind
	operation extensionOperation
	match     extensionMatchRule
	transport configuredHTTPTransport
}

type extensionMatchRule struct {
	principalIDs map[string]struct{}
	presetIDs    map[string]struct{}
}

type configuredHTTPTransport struct {
	url        string
	authHeader string
	timeout    time.Duration
}

type admissionError struct {
	status  int
	message string
	data    any
	err     error
}

func (e *admissionError) Error() string {
	return e.message
}

func (e *admissionError) Unwrap() error {
	return e.err
}

func newAdmissionError(status int, message string, data any, err error) error {
	if err == nil {
		err = errors.New(message)
	}
	return &admissionError{
		status:  status,
		message: message,
		data:    data,
		err:     err,
	}
}

func newExtensionRegistry() (extensionRegistry, error) {
	raw := strings.TrimSpace(os.Getenv(extensionsEnvKey))
	if raw == "" {
		return extensionRegistry{}, nil
	}
	var inputs []extensionConfigInput
	if err := json.Unmarshal([]byte(raw), &inputs); err != nil {
		return extensionRegistry{}, fmt.Errorf("invalid %s: %w", extensionsEnvKey, err)
	}
	if len(inputs) == 0 {
		return extensionRegistry{}, fmt.Errorf("invalid %s: at least one extension is required", extensionsEnvKey)
	}
	registry := extensionRegistry{}
	seen := map[string]struct{}{}
	for index, input := range inputs {
		id := strings.TrimSpace(input.ID)
		if id == "" {
			return extensionRegistry{}, fmt.Errorf("invalid %s: extensions[%d].id is required", extensionsEnvKey, index)
		}
		if _, ok := seen[id]; ok {
			return extensionRegistry{}, fmt.Errorf("invalid %s: duplicate extension id %q", extensionsEnvKey, id)
		}
		seen[id] = struct{}{}

		kind := normalizeExtensionKind(input.Kind)
		if kind == "" {
			return extensionRegistry{}, fmt.Errorf("invalid %s: extensions[%d].kind is required", extensionsEnvKey, index)
		}
		operation := normalizeExtensionOperation(input.Operation)
		if operation == "" {
			return extensionRegistry{}, fmt.Errorf("invalid %s: extensions[%d].operation is required and must be supported", extensionsEnvKey, index)
		}
		if kind != extensionKindResolver {
			return extensionRegistry{}, fmt.Errorf("invalid %s: extensions[%d].kind %q is not yet supported", extensionsEnvKey, index, kind)
		}
		match, err := normalizeExtensionMatch(input.Match)
		if err != nil {
			return extensionRegistry{}, fmt.Errorf("invalid %s: extensions[%d].match %v", extensionsEnvKey, index, err)
		}
		transport, err := normalizeExtensionTransport(input.Transport)
		if err != nil {
			return extensionRegistry{}, fmt.Errorf("invalid %s: extensions[%d].transport %v", extensionsEnvKey, index, err)
		}
		registry.resolvers = append(registry.resolvers, configuredResolver{
			id:        id,
			kind:      kind,
			operation: operation,
			match:     match,
			transport: transport,
		})
	}
	return registry, nil
}

func normalizeExtensionKind(raw string) extensionKind {
	switch extensionKind(strings.ToLower(strings.TrimSpace(raw))) {
	case extensionKindResolver:
		return extensionKindResolver
	case extensionKindAuthProvider:
		return extensionKindAuthProvider
	case extensionKindLifecycleHook:
		return extensionKindLifecycleHook
	default:
		return ""
	}
}

func normalizeExtensionOperation(raw string) extensionOperation {
	switch extensionOperation(strings.ToLower(strings.TrimSpace(raw))) {
	case extensionOperationOwnerResolve:
		return extensionOperationOwnerResolve
	case extensionOperationPresetCreateResolve:
		return extensionOperationPresetCreateResolve
	case extensionOperationAuthLoginMetadata:
		return extensionOperationAuthLoginMetadata
	case extensionOperationIdentityLinkResolve:
		return extensionOperationIdentityLinkResolve
	case extensionOperationInstanceNotify:
		return extensionOperationInstanceNotify
	default:
		return ""
	}
}

func normalizeExtensionMatch(input extensionMatchInput) (extensionMatchRule, error) {
	presetIDs, err := normalizePresetIDSet(input.PresetIDs)
	if err != nil {
		return extensionMatchRule{}, err
	}
	return extensionMatchRule{
		principalIDs: normalizeStringTokenSet(input.PrincipalIDs),
		presetIDs:    presetIDs,
	}, nil
}

func normalizePresetIDSet(values []string) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]struct{}, len(values))
	invalid := make([]string, 0, len(values))
	for _, value := range values {
		raw := strings.TrimSpace(value)
		if raw == "" {
			continue
		}
		token := sanitizeSpritzNameToken(value)
		if token == "" {
			invalid = append(invalid, raw)
			continue
		}
		out[token] = struct{}{}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		return nil, fmt.Errorf("presetIds contains invalid ids: %s", strings.Join(invalid, ", "))
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func normalizeExtensionTransport(input extensionTransportInput) (configuredHTTPTransport, error) {
	kind := extensionTransportType(strings.ToLower(strings.TrimSpace(input.Type)))
	if kind == "" {
		kind = extensionTransportHTTP
	}
	if kind != extensionTransportHTTP {
		return configuredHTTPTransport{}, fmt.Errorf("type must be http")
	}
	urlValue := strings.TrimSpace(input.URL)
	if urlValue == "" {
		return configuredHTTPTransport{}, fmt.Errorf("url is required")
	}
	if _, err := validateExtensionURL(urlValue); err != nil {
		return configuredHTTPTransport{}, err
	}
	authHeader, err := resolveExtensionAuthHeader(input)
	if err != nil {
		return configuredHTTPTransport{}, err
	}
	timeout := defaultExtensionResolverTimeout
	if strings.TrimSpace(input.Timeout) != "" {
		parsed, err := time.ParseDuration(strings.TrimSpace(input.Timeout))
		if err != nil {
			return configuredHTTPTransport{}, fmt.Errorf("timeout is invalid")
		}
		if parsed <= 0 {
			return configuredHTTPTransport{}, fmt.Errorf("timeout must be greater than zero")
		}
		timeout = parsed
	}
	return configuredHTTPTransport{
		url:        urlValue,
		authHeader: authHeader,
		timeout:    timeout,
	}, nil
}

func validateExtensionURL(raw string) (*url.URL, error) {
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

func resolveExtensionAuthHeader(input extensionTransportInput) (string, error) {
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

func normalizeStringTokenSet(values []string) map[string]struct{} {
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
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r extensionRegistry) resolve(
	ctx context.Context,
	operation extensionOperation,
	principal principal,
	requestID string,
	requestContext extensionRequestContext,
	input any,
) (*configuredResolver, extensionResolverResponseEnvelope, error) {
	matches := r.matchingResolvers(operation, principal, requestContext)
	switch len(matches) {
	case 0:
		return nil, extensionResolverResponseEnvelope{}, nil
	case 1:
		response, err := matches[0].resolve(ctx, principal, requestID, requestContext, input)
		return &matches[0], response, err
	default:
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.id)
		}
		sort.Strings(ids)
		return nil, extensionResolverResponseEnvelope{}, fmt.Errorf("multiple resolvers matched %s: %s", operation, strings.Join(ids, ", "))
	}
}

func (r extensionRegistry) matchingResolvers(operation extensionOperation, principal principal, requestContext extensionRequestContext) []configuredResolver {
	if len(r.resolvers) == 0 {
		return nil
	}
	matches := make([]configuredResolver, 0, len(r.resolvers))
	for _, candidate := range r.resolvers {
		if candidate.operation != operation {
			continue
		}
		if !candidate.matches(principal, requestContext) {
			continue
		}
		matches = append(matches, candidate)
	}
	return matches
}

func (r configuredResolver) matches(principal principal, requestContext extensionRequestContext) bool {
	if len(r.match.principalIDs) > 0 {
		if _, ok := r.match.principalIDs[strings.TrimSpace(principal.ID)]; !ok {
			return false
		}
	}
	if len(r.match.presetIDs) > 0 {
		if _, ok := r.match.presetIDs[sanitizeSpritzNameToken(requestContext.PresetID)]; !ok {
			return false
		}
	}
	return true
}

func (r configuredResolver) resolve(
	ctx context.Context,
	principal principal,
	requestID string,
	requestContext extensionRequestContext,
	input any,
) (extensionResolverResponseEnvelope, error) {
	envelope := extensionResolverRequestEnvelope{
		Version:     "v1",
		ExtensionID: r.id,
		Kind:        r.kind,
		Operation:   r.operation,
		RequestID:   strings.TrimSpace(requestID),
		Principal: extensionPrincipalPayload{
			ID:      strings.TrimSpace(principal.ID),
			Type:    string(principal.Type),
			Email:   strings.TrimSpace(principal.Email),
			Subject: strings.TrimSpace(principal.Subject),
			Issuer:  strings.TrimSpace(principal.Issuer),
			Teams:   append([]string(nil), principal.Teams...),
			Scopes:  append([]string(nil), principal.Scopes...),
		},
		Context: requestContext,
		Input:   input,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return extensionResolverResponseEnvelope{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, r.transport.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, r.transport.url, bytes.NewReader(encoded))
	if err != nil {
		return extensionResolverResponseEnvelope{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader := strings.TrimSpace(r.transport.authHeader); authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return extensionResolverResponseEnvelope{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusForbidden:
		return extensionResolverResponseEnvelope{Status: extensionStatusForbidden}, nil
	case http.StatusNotFound:
		return extensionResolverResponseEnvelope{Status: extensionStatusUnresolved}, nil
	case http.StatusConflict:
		return extensionResolverResponseEnvelope{Status: extensionStatusAmbiguous}, nil
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return extensionResolverResponseEnvelope{Status: extensionStatusInvalid}, nil
	case http.StatusServiceUnavailable, http.StatusBadGateway, http.StatusGatewayTimeout:
		return extensionResolverResponseEnvelope{Status: extensionStatusUnavailable}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return extensionResolverResponseEnvelope{}, fmt.Errorf("resolver returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return extensionResolverResponseEnvelope{}, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return extensionResolverResponseEnvelope{Status: extensionStatusResolved}, nil
	}
	var payload extensionResolverResponseEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return extensionResolverResponseEnvelope{}, err
	}
	if payload.Status == "" {
		payload.Status = extensionStatusResolved
	}
	return payload, nil
}
