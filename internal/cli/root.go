// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package cli wires the ccauth subcommands together.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
)

var profileFlag string

// NewRootCmd builds the root command tree.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "ccauth",
		Short: "OAuth2/SSO credential helper for Claude Code and AI gateways",
		Long: `ccauth authenticates you to an AI gateway (LiteLLM, Portkey, Bifrost, or any
OIDC-aware gateway) using your corporate SSO (Microsoft Entra ID, Google
Workspace, or a generic OIDC provider), and feeds the resulting short-lived
token to Claude Code via its apiKeyHelper hook — so nobody hand-manages API keys.

Typical flow:
  ccauth setup      # one-time: pick provider + gateway, write config + wiring
  ccauth login      # interactive browser/device sign-in, stores a refresh token
  ccauth token      # what Claude Code calls; prints the current credential

Point Claude Code at it in ~/.claude/settings.json:
  { "apiKeyHelper": "ccauth token --profile <name>" }`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&profileFlag, "profile", "p", "",
		"config profile to use (default: the config's default_profile, or \"default\")")

	root.AddCommand(
		newSetupCmd(),
		newInitCmd(),
		newLoginCmd(),
		newTokenCmd(),
		newLogoutCmd(),
		newStatusCmd(),
		newWireCmd(),
		newConfigCmd(),
		newDoctorCmd(),
		newGatewaysCmd(),
	)
	return root
}

// resolve loads config, resolves the active profile, and builds its store.
func resolve() (string, config.Profile, store.Store, error) {
	c, err := config.Load()
	if err != nil {
		return "", config.Profile{}, nil, err
	}
	name := c.ProfileName(profileFlag)
	prof, err := c.Resolve(name)
	if err != nil {
		return name, config.Profile{}, nil, err
	}
	return name, prof, store.New(prof.Store), nil
}
