package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"

	spritzv1 "spritz.sh/operator/api/v1"
)

const (
	openclawConfigEnvName     = "OPENCLAW_CONFIG_JSON"
	openclawConfigB64EnvName  = "OPENCLAW_CONFIG_B64"
	openclawConfigFileEnvName = "OPENCLAW_CONFIG_FILE"
)

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
	channelsConfig := ensureObjectField(config, "channels")
	providerConfig := ensureObjectField(channelsConfig, provider)
	providerChannels := map[string]any{}
	for _, policy := range channelPolicies {
		providerChannels[policy.ExternalChannelID] = map[string]any{
			"allow":          true,
			"requireMention": *policy.RequireMention,
		}
	}
	providerConfig["channels"] = providerChannels

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
	var b64Env *corev1.EnvVar
	var fileEnv *corev1.EnvVar
	for _, item := range env {
		switch item.Name {
		case openclawConfigEnvName:
			if item.ValueFrom != nil {
				return nil, errors.New("OPENCLAW_CONFIG_JSON cannot use valueFrom with installationConfig")
			}
			return parseOpenClawConfigJSON(item.Value, openclawConfigEnvName)
		case openclawConfigB64EnvName:
			copied := item
			b64Env = &copied
		case openclawConfigFileEnvName:
			copied := item
			fileEnv = &copied
		}
	}
	if b64Env != nil {
		if b64Env.ValueFrom != nil {
			return nil, errors.New("OPENCLAW_CONFIG_B64 cannot use valueFrom with installationConfig")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64Env.Value))
		if err != nil {
			return nil, errors.New("OPENCLAW_CONFIG_B64 is invalid")
		}
		return parseOpenClawConfigJSON(string(decoded), openclawConfigB64EnvName)
	}
	if fileEnv != nil {
		return nil, errors.New("OPENCLAW_CONFIG_FILE cannot be merged with installationConfig")
	}
	return defaultOpenClawConfig(), nil
}

func defaultOpenClawConfig() map[string]any {
	return map[string]any{
		"browser": map[string]any{
			"enabled":        true,
			"headless":       true,
			"executablePath": "/usr/bin/chromium",
		},
	}
}

func parseOpenClawConfigJSON(value string, source string) (map[string]any, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%s is invalid", source)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("%s is invalid", source)
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
	filtered := (*env)[:0]
	jsonIndex := -1
	for index := range *env {
		switch (*env)[index].Name {
		case openclawConfigEnvName:
			(*env)[index].Value = value
			(*env)[index].ValueFrom = nil
			jsonIndex = len(filtered)
			filtered = append(filtered, (*env)[index])
		case openclawConfigB64EnvName, openclawConfigFileEnvName:
			continue
		default:
			filtered = append(filtered, (*env)[index])
		}
	}
	if jsonIndex < 0 {
		filtered = append(filtered, corev1.EnvVar{Name: openclawConfigEnvName, Value: value})
	}
	*env = filtered
}
