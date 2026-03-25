package main

import (
	"strings"
	"sync"
	"time"
)

type dedupeStore struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]dedupeEntry
}

type dedupeEntry struct {
	seenAt   time.Time
	inFlight bool
}

type slackThreadRootStore struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]slackThreadRootEntry
}

type slackThreadRootEntry struct {
	rootConversationID string
	seenAt             time.Time
}

type dedupeLease struct {
	store *dedupeStore
	key   string
}

type slackMessageDelivery struct {
	messageLease *dedupeLease
	eventLease   *dedupeLease
}

type dedupeState int

const (
	dedupeStateAcquired dedupeState = iota
	dedupeStateDuplicateInFlight
	dedupeStateDuplicateDelivered
)

func newDedupeStore(ttl time.Duration) *dedupeStore {
	return &dedupeStore{
		ttl:     ttl,
		now:     time.Now,
		entries: map[string]dedupeEntry{},
	}
}

func newSlackThreadRootStore(ttl time.Duration) *slackThreadRootStore {
	return &slackThreadRootStore{
		ttl:     ttl,
		now:     time.Now,
		entries: map[string]slackThreadRootEntry{},
	}
}

func (d *dedupeStore) begin(key string) (*dedupeLease, dedupeState) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, dedupeStateAcquired
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now().UTC()
	cutoff := now.Add(-d.ttl)
	for candidate, entry := range d.entries {
		if entry.seenAt.Before(cutoff) {
			delete(d.entries, candidate)
		}
	}
	if entry, ok := d.entries[key]; ok && now.Sub(entry.seenAt) <= d.ttl {
		if entry.inFlight {
			return nil, dedupeStateDuplicateInFlight
		}
		return nil, dedupeStateDuplicateDelivered
	}
	d.entries[key] = dedupeEntry{seenAt: now, inFlight: true}
	return &dedupeLease{store: d, key: key}, dedupeStateAcquired
}

func (s *slackThreadRootStore) remember(teamID, channelID, threadTS, rootConversationID string) {
	key := slackThreadRootKey(teamID, channelID, threadTS)
	rootConversationID = strings.TrimSpace(rootConversationID)
	if key == "" || rootConversationID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked()
	s.entries[key] = slackThreadRootEntry{
		rootConversationID: rootConversationID,
		seenAt:             s.now().UTC(),
	}
}

func (s *slackThreadRootStore) lookup(teamID, channelID, threadTS string) string {
	key := slackThreadRootKey(teamID, channelID, threadTS)
	if key == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpiredLocked()
	entry, ok := s.entries[key]
	if !ok {
		return ""
	}
	return entry.rootConversationID
}

func (s *slackThreadRootStore) pruneExpiredLocked() {
	cutoff := s.now().UTC().Add(-s.ttl)
	for key, entry := range s.entries {
		if entry.seenAt.Before(cutoff) {
			delete(s.entries, key)
		}
	}
}

func slackThreadRootKey(teamID, channelID, threadTS string) string {
	teamID = strings.TrimSpace(teamID)
	channelID = strings.TrimSpace(channelID)
	threadTS = strings.TrimSpace(threadTS)
	if teamID == "" || channelID == "" || threadTS == "" {
		return ""
	}
	return strings.Join([]string{teamID, channelID, threadTS}, ":")
}

func (l *dedupeLease) finish(success bool) {
	if l == nil || l.store == nil || l.key == "" {
		return
	}
	l.store.mu.Lock()
	defer l.store.mu.Unlock()
	entry, ok := l.store.entries[l.key]
	if !ok || !entry.inFlight {
		return
	}
	if !success {
		delete(l.store.entries, l.key)
		return
	}
	entry.inFlight = false
	entry.seenAt = l.store.now().UTC()
	l.store.entries[l.key] = entry
}

func (d *slackMessageDelivery) finish(success bool) {
	if d == nil {
		return
	}
	if d.eventLease != nil {
		d.eventLease.finish(success)
	}
	if d.messageLease != nil {
		d.messageLease.finish(success)
	}
}
