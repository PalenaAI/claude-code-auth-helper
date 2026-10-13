// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package ui

import (
	"bytes"
	"strings"
	"testing"
)

// Output to a non-terminal (pipes, logs, Claude Code capturing stderr) must be
// plain text: no ANSI escape sequences.
func TestNoEscapesWhenNotATerminal(t *testing.T) {
	var b bytes.Buffer
	OK(&b, "saved %s", Val("x"))
	Warn(&b, "careful %s", Cmd("ccauth login"))
	Heading(&b, "Next steps")
	Println(&b, Hint("dim"), Bold("bold"))
	if strings.Contains(b.String(), "\x1b[") {
		t.Errorf("escape sequences in non-TTY output: %q", b.String())
	}
	if !strings.Contains(b.String(), "✓ saved x") || !strings.Contains(b.String(), "⚠ careful ccauth login") {
		t.Errorf("text lost: %q", b.String())
	}
}

func TestCardWithoutColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var b bytes.Buffer
	Card(&b, "Profile work", KV([2]string{"Gateway", "https://gw"}, [2]string{"Redirect URI", "http://localhost/callback"})...)
	got := b.String()
	if strings.ContainsAny(got, "╭╮╰╯") || strings.Contains(got, "\x1b[") {
		t.Errorf("NO_COLOR card should be plain: %q", got)
	}
	if !strings.Contains(got, "Gateway       https://gw") || !strings.Contains(got, "Redirect URI  http://localhost/callback") {
		t.Errorf("rows not aligned: %q", got)
	}
}

func TestCommandsKeepBackticksWhenPlain(t *testing.T) {
	var b bytes.Buffer
	Status(&b, "✗", "no session — run `ccauth login -p work`")
	if got := b.String(); got != "✗ no session — run `ccauth login -p work`\n" {
		t.Errorf("plain output changed: %q", got)
	}
}
