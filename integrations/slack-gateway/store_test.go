package main

import (
	"path/filepath"
	"testing"
)

func TestFileInstallationStoreRoundTrip(t *testing.T) {
	store := newFileInstallationStore(filepath.Join(t.TempDir(), "installations.json"))
	record, err := store.Upsert(slackInstallation{
		TeamID:           "T_workspace_1",
		BotAccessToken:   "xoxb-test",
		InstallingUserID: "U_installer",
	})
	if err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if record.ProviderInstallRef == "" {
		t.Fatalf("expected provider install ref")
	}
	loaded, ok, err := store.GetByTeamID("T_workspace_1")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected stored installation")
	}
	if loaded.BotAccessToken != "xoxb-test" {
		t.Fatalf("expected stored token, got %q", loaded.BotAccessToken)
	}
	if err := store.DeleteByTeamID("T_workspace_1"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, ok, err := store.GetByTeamID("T_workspace_1"); err != nil || ok {
		t.Fatalf("expected installation to be deleted, ok=%v err=%v", ok, err)
	}
}
