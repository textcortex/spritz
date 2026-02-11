package sharedmounts

import "testing"

func TestValidateMountsRejectsInvalidSyncMode(t *testing.T) {
	mounts := []MountSpec{
		NormalizeMount(MountSpec{
			Name:      "config",
			MountPath: "/config",
			SyncMode:  "polling",
		}),
	}
	if err := ValidateMounts(mounts); err == nil {
		t.Fatal("expected error for invalid syncMode")
	}
}

func TestValidateMountsAcceptsValidSyncMode(t *testing.T) {
	tests := []MountSpec{
		NormalizeMount(MountSpec{
			Name:      "manual",
			MountPath: "/manual",
			SyncMode:  SyncManual,
		}),
		NormalizeMount(MountSpec{
			Name:      "poll",
			MountPath: "/poll",
			SyncMode:  SyncPoll,
		}),
		NormalizeMount(MountSpec{
			Name:      "snapshot-poll",
			MountPath: "/snapshot-poll",
			Mode:      ModeSnapshot,
			SyncMode:  SyncPoll,
		}),
	}
	if err := ValidateMounts(tests); err != nil {
		t.Fatalf("expected valid syncMode, got error: %v", err)
	}
}

func TestValidateMountsRejectsDuplicatePaths(t *testing.T) {
	mounts := []MountSpec{
		NormalizeMount(MountSpec{
			Name:      "config",
			MountPath: "/config",
		}),
		NormalizeMount(MountSpec{
			Name:      "config-copy",
			MountPath: "/config",
		}),
	}
	if err := ValidateMounts(mounts); err == nil {
		t.Fatal("expected error for duplicate mount paths")
	}
}
