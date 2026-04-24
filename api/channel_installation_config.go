package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

const openclawConfigEnvName = "OPENCLAW_CONFIG_JSON"

type channelInstallationConfigPayload struct {
	ChannelPolicies []channelInstallationChannelPolicy `json:"channelPolicies,omitempty"`
}

type channelInstallationChannelPolicy struct {
	ExternalChannelID   string `json:"externalChannelId"`
	ExternalChannelType string `json:"externalChannelType,omitempty"`
	RequireMention      *bool  `json:"requireMention"`
}

func applyChannelInstallationConfigProjection(spec *spritzv1.SpritzSpec, attributes map[string]string, raw json.RawMessage) error {
	if spec == nil {
		return nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil
	}
	var payload channelInstallationConfigPayload
	if !bytes.Equal(trimmed, []byte("null")) {
		decoder := json.NewDecoder(bytes.NewReader(trimmed))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil {
			return errors.New("installationConfig is invalid")
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			return errors.New("installationConfig is invalid")
		}
	}

	provider := strings.TrimSpace(attributes["provider"])
	externalScopeType := strings.TrimSpace(attributes["externalScopeType"])
	externalTenantID := strings.TrimSpace(attributes["externalTenantId"])
	if provider == "" || externalScopeType == "" || externalTenantID == "" {
		return errors.New("installationConfig requires provider route attributes")
	}
	channelPolicies, err := normalizeChannelInstallationPolicies(payload.ChannelPolicies)
	if err != nil {
		return err
	}

	config, err := readOpenClawConfigEnv(spec.Env)
	if err != nil {
		return err
	}
	providers := ensureObjectField(config, "providers")
	providerConfig := ensureObjectField(providers, provider)
	channels := map[string]any{}
	for _, policy := range channelPolicies {
		channels[policy.ExternalChannelID] = map[string]any{
			"allow":          true,
			"requireMention": *policy.RequireMention,
		}
	}
	providerConfig["channels"] = channels

	encoded, err := json.Marshal(config)
	if err != nil {
		return errors.New("OPENCLAW_CONFIG_JSON is invalid")
	}
	setOpenClawConfigEnv(&spec.Env, string(encoded))
	return nil
}

func normalizeChannelInstallationPolicies(policies []channelInstallationChannelPolicy) ([]channelInstallationChannelPolicy, error) {
	seen := map[string]struct{}{}
	normalized := make([]channelInstallationChannelPolicy, 0, len(policies))
	for index, policy := range policies {
		channelID := strings.TrimSpace(policy.ExternalChannelID)
		if channelID == "" {
			return nil, fmt.Errorf("installationConfig.channelPolicies.%d.externalChannelId is required", index)
		}
		if _, exists := seen[channelID]; exists {
			return nil, fmt.Errorf("installationConfig.channelPolicies.%d.externalChannelId is duplicate", index)
		}
		seen[channelID] = struct{}{}
		if policy.RequireMention == nil {
			return nil, fmt.Errorf("installationConfig.channelPolicies.%d.requireMention is required", index)
		}
		policy.ExternalChannelID = channelID
		policy.ExternalChannelType = strings.TrimSpace(policy.ExternalChannelType)
		normalized = append(normalized, policy)
	}
	return normalized, nil
}

func readOpenClawConfigEnv(env []corev1.EnvVar) (map[string]any, error) {
	for _, item := range env {
		if item.Name != openclawConfigEnvName {
			continue
		}
		if item.ValueFrom != nil {
			return nil, errors.New("OPENCLAW_CONFIG_JSON cannot use valueFrom with installationConfig")
		}
		return parseOpenClawConfigJSON(item.Value)
	}
	return map[string]any{}, nil
}

func parseOpenClawConfigJSON(value string) (map[string]any, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, errors.New("OPENCLAW_CONFIG_JSON is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("OPENCLAW_CONFIG_JSON is invalid")
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, nil
}

func ensureObjectField(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	child := map[string]any{}
	parent[key] = child
	return child
}

func setOpenClawConfigEnv(env *[]corev1.EnvVar, value string) {
	for index := range *env {
		if (*env)[index].Name == openclawConfigEnvName {
			(*env)[index].Value = value
			(*env)[index].ValueFrom = nil
			return
		}
	}
	*env = append(*env, corev1.EnvVar{Name: openclawConfigEnvName, Value: value})
}
