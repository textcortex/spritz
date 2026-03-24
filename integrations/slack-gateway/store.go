package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type slackInstallation struct {
	ProviderInstallRef string    `json:"providerInstallRef"`
	APIAppID           string    `json:"apiAppId,omitempty"`
	TeamID             string    `json:"teamId"`
	EnterpriseID       string    `json:"enterpriseId,omitempty"`
	InstallingUserID   string    `json:"installingUserId,omitempty"`
	BotUserID          string    `json:"botUserId,omitempty"`
	ScopeSet           []string  `json:"scopeSet,omitempty"`
	BotAccessToken     string    `json:"botAccessToken"`
	InstalledAt        time.Time `json:"installedAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type installationStore interface {
	Upsert(slackInstallation) (slackInstallation, error)
	GetByTeamID(string) (slackInstallation, bool, error)
	DeleteByTeamID(string) error
}

type fileInstallationStore struct {
	path string
	mu   sync.Mutex
}

func newFileInstallationStore(path string) *fileInstallationStore {
	return &fileInstallationStore{path: strings.TrimSpace(path)}
}

func (s *fileInstallationStore) Upsert(record slackInstallation) (slackInstallation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return slackInstallation{}, err
	}
	now := time.Now().UTC()
	if record.ProviderInstallRef == "" {
		record.ProviderInstallRef = "slack-install:" + strings.TrimSpace(record.TeamID)
	}
	if existing, ok := state[strings.TrimSpace(record.TeamID)]; ok && !existing.InstalledAt.IsZero() {
		record.InstalledAt = existing.InstalledAt
	} else if record.InstalledAt.IsZero() {
		record.InstalledAt = now
	}
	record.UpdatedAt = now
	state[strings.TrimSpace(record.TeamID)] = record
	if err := s.saveLocked(state); err != nil {
		return slackInstallation{}, err
	}
	return record, nil
}

func (s *fileInstallationStore) GetByTeamID(teamID string) (slackInstallation, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return slackInstallation{}, false, err
	}
	record, ok := state[strings.TrimSpace(teamID)]
	return record, ok, nil
}

func (s *fileInstallationStore) DeleteByTeamID(teamID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadLocked()
	if err != nil {
		return err
	}
	delete(state, strings.TrimSpace(teamID))
	return s.saveLocked(state)
}

func (s *fileInstallationStore) loadLocked() (map[string]slackInstallation, error) {
	if s.path == "" {
		return nil, fmt.Errorf("installation store path is required")
	}
	if _, err := os.Stat(s.path); err != nil {
		if os.IsNotExist(err) {
			return map[string]slackInstallation{}, nil
		}
		return nil, err
	}
	payload, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 {
		return map[string]slackInstallation{}, nil
	}
	state := map[string]slackInstallation{}
	if err := json.Unmarshal(payload, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func (s *fileInstallationStore) saveLocked(state map[string]slackInstallation) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tempPath := s.path + ".tmp"
	if err := os.WriteFile(tempPath, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}
