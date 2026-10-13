// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"testing"
	"time"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

// Claude Desktop marks each helper run with CLAUDE_HELPER_CONTEXT; only an
// interactive run may open a browser. Without the variable (Claude Code, a
// user) the behavior is unchanged.
func TestCanAutoLoginRespectsDesktopHelperContext(t *testing.T) {
	t.Setenv("CCAUTH_NONINTERACTIVE", "")
	t.Setenv("CI", "")
	t.Setenv("DISPLAY", ":0") // make a browser "available" on Linux CI
	prof := config.Profile{OAuth: config.OAuth{Flow: config.FlowAuthCode}}
	for ctx, want := range map[string]bool{
		"mid-session-refresh": false,
		"scheduled-task":      false,
		"background":          false,
		"setup-test":          false,
	} {
		t.Setenv("CLAUDE_HELPER_CONTEXT", ctx)
		if got := canAutoLogin(prof); got != want {
			t.Errorf("CLAUDE_HELPER_CONTEXT=%s: canAutoLogin = %v, want %v", ctx, got, want)
		}
	}
	t.Setenv("CLAUDE_HELPER_CONTEXT", "interactive")
	interactive := canAutoLogin(prof)
	t.Setenv("CLAUDE_HELPER_CONTEXT", "")
	if unset := canAutoLogin(prof); unset != interactive {
		t.Errorf("interactive (%v) should behave like no helper context (%v)", interactive, unset)
	}
}

func TestEmitTimeoutStaysUnderDesktopRefreshLimit(t *testing.T) {
	t.Setenv("CLAUDE_HELPER_CONTEXT", "mid-session-refresh")
	if d := emitTimeout(); d >= 20*time.Second {
		t.Errorf("mid-session refresh timeout %v must stay under Desktop's 20s", d)
	}
	t.Setenv("CLAUDE_HELPER_CONTEXT", "")
	if d := emitTimeout(); d != 30*time.Second {
		t.Errorf("CLI timeout changed: %v", d)
	}
}
