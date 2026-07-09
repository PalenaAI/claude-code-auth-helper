// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Command ccauth is an OAuth2/SSO credential helper for Claude Code and AI
// gateways (LiteLLM, Portkey, Bifrost, and generic OIDC gateways).
package main

import (
	"fmt"
	"os"

	"github.com/PalenaAI/claude-code-auth-helper/internal/cli"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "ccauth: "+err.Error())
		os.Exit(1)
	}
}
