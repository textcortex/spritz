package main

import "testing"

func TestNewSSHMintLimiterAllowsZeroLimit(t *testing.T) {
	t.Setenv("SPRITZ_SSH_MINT_LIMIT", "0")
	t.Setenv("SPRITZ_SSH_MINT_WINDOW", "1m")
	t.Setenv("SPRITZ_SSH_MINT_BURST", "5")

	limiter := newSSHMintLimiter()
	if limiter != nil {
		t.Fatal("expected limiter to be disabled when SPRITZ_SSH_MINT_LIMIT=0")
	}
}
