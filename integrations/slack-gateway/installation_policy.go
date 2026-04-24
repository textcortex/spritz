package main

import (
	"strings"
	"sync"
	"time"
)

type installationConfig struct {
	ChannelPolicies []installationChannelPolicy `json:"channelPolicies,omitempty"`
}

type installationChannelPolicy struct {
	ExternalChannelID   string `json:"externalChannelId"`
	ExternalChannelType string `json:"externalChannelType,omitempty"`
	RequireMention      *bool  `json:"requireMention"`
}

type installationPolicySnapshot struct {
	config    installationConfig
	botUserID string
}

type installationPolicyCacheEntry struct {
	snapshot  installationPolicySnapshot
	expiresAt time.Time
}

type installationPolicyCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]installationPolicyCacheEntry
}

func newInstallationPolicyCache(ttl time.Duration) *installationPolicyCache {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return &installationPolicyCache{
		ttl:     ttl,
		now:     time.Now,
		entries: map[string]installationPolicyCacheEntry{},
	}
}

func (cache *installationPolicyCache) remember(teamID string, snapshot installationPolicySnapshot) {
	if cache == nil {
		return
	}
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.entries[teamID] = installationPolicyCacheEntry{
		snapshot:  snapshot,
		expiresAt: cache.now().Add(cache.ttl),
	}
}

func (cache *installationPolicyCache) lookup(teamID string) (installationPolicySnapshot, bool) {
	if cache == nil {
		return installationPolicySnapshot{}, false
	}
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return installationPolicySnapshot{}, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries[teamID]
	if !ok {
		return installationPolicySnapshot{}, false
	}
	if !entry.expiresAt.After(cache.now()) {
		delete(cache.entries, teamID)
		return installationPolicySnapshot{}, false
	}
	return entry.snapshot, true
}

func (session channelSession) policySnapshot() installationPolicySnapshot {
	return installationPolicySnapshot{
		config:    session.InstallationConfig,
		botUserID: strings.TrimSpace(session.ProviderAuth.BotUserID),
	}
}

func shouldRelaySlackMessageEvent(event slackEventInner, snapshot installationPolicySnapshot) bool {
	eventType := strings.TrimSpace(event.Type)
	if isSlackDirectMessageEvent(event) {
		return eventType == "message"
	}
	if eventType == "app_mention" {
		return true
	}
	if eventType != "message" {
		return false
	}
	if slackTextMentionsBot(event.Text, snapshot.botUserID) {
		return true
	}
	return installationConfigAllowsChannelWithoutMention(
		snapshot.config,
		strings.TrimSpace(event.Channel),
	)
}

func slackTextMentionsBot(text, botUserID string) bool {
	botUserID = strings.TrimSpace(botUserID)
	if botUserID == "" {
		return false
	}
	return strings.Contains(strings.TrimSpace(text), "<@"+botUserID+">")
}

func installationConfigAllowsChannelWithoutMention(config installationConfig, channelID string) bool {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return false
	}
	for _, policy := range config.ChannelPolicies {
		if strings.TrimSpace(policy.ExternalChannelID) != channelID {
			continue
		}
		return policy.RequireMention != nil && !*policy.RequireMention
	}
	return false
}
