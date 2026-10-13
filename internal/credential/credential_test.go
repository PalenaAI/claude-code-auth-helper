// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package credential

import (
	"testing"
	"time"
)

func TestRequiredLifetimeReadsClaudeTTL(t *testing.T) {
	// The token we emit must outlive Claude Code's cache window, so the
	// required lifetime is the TTL plus our safety skew.
	t.Setenv("CLAUDE_CODE_API_KEY_HELPER_TTL_MS", "600000") // 10 min
	got := requiredLifetime()
	want := 10*time.Minute + defaultSkew
	if got != want {
		t.Errorf("requiredLifetime() = %v, want %v", got, want)
	}
}

func TestRequiredLifetimeDefaultsToSkew(t *testing.T) {
	t.Setenv("CLAUDE_CODE_API_KEY_HELPER_TTL_MS", "")
	if got := requiredLifetime(); got != defaultSkew {
		t.Errorf("requiredLifetime() = %v, want %v", got, defaultSkew)
	}
}

func TestRequiredLifetimeIgnoresGarbage(t *testing.T) {
	t.Setenv("CLAUDE_CODE_API_KEY_HELPER_TTL_MS", "not-a-number")
	if got := requiredLifetime(); got != defaultSkew {
		t.Errorf("requiredLifetime() = %v, want %v", got, defaultSkew)
	}
}

func TestRemainingUsesFallbackForOpaqueToken(t *testing.T) {
	future := time.Now().Add(30 * time.Minute)
	// Opaque token (not a JWT) -> falls back to the recorded session expiry.
	d := remaining("opaque-access-token", future)
	if d < 29*time.Minute || d > 31*time.Minute {
		t.Errorf("remaining() = %v, want ~30m from fallback", d)
	}
	// No token and no fallback -> zero.
	if d := remaining("", time.Time{}); d != 0 {
		t.Errorf("remaining() = %v, want 0", d)
	}
}

func TestLifetimeFor(t *testing.T) {
	t.Setenv("CLAUDE_CODE_API_KEY_HELPER_TTL_MS", "600000") // 10 min → 12 min needed
	if got := lifetimeFor(0); got != 12*time.Minute {
		t.Errorf("lifetimeFor(0) = %v, want Claude Code's 12m", got)
	}
	if got := lifetimeFor(30 * time.Minute); got != 32*time.Minute {
		t.Errorf("lifetimeFor(30m) = %v, want 32m", got)
	}
	if got := lifetimeFor(time.Minute); got != 12*time.Minute {
		t.Errorf("a smaller minLifetime must not lower the requirement, got %v", got)
	}
}
