// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Command ccauth is an OAuth2/SSO credential helper for Claude Code and AI
// gateways (LiteLLM, Portkey, Bifrost, and generic OIDC gateways).
package main

import (
	"os"

	"github.com/PalenaAI/claude-code-auth-helper/internal/cli"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		// Colors are stripped automatically when stderr isn't a terminal
		// (e.g. when Claude Code runs ccauth as its apiKeyHelper).
		ui.Fail(os.Stderr, "ccauth: %s", err)
		os.Exit(1)
	}
}
