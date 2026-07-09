// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
)

func newSetupCmd() *cobra.Command {
	var write bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Interactive wizard: choose a provider + gateway and write the config",
		Long:  "Walks you through picking an identity provider and gateway, collects the required details (Entra tenant/client IDs, Google client ID, OIDC issuer, gateway URL), writes a profile to the config file, and prints the Claude Code wiring.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY() {
				return fmt.Errorf("setup needs an interactive terminal; run `ccauth init` to write an example config you can edit by hand")
			}
			r := bufio.NewReader(os.Stdin)

			// If IT has already provisioned profiles via a managed/remote/embedded
			// layer, most users just need `ccauth login`.
			if merged, err := config.Load(); err == nil {
				var itProfiles []string
				for pn, src := range merged.Sources {
					if src != "user" {
						itProfiles = append(itProfiles, fmt.Sprintf("%s (%s)", pn, src))
					}
				}
				if len(itProfiles) > 0 {
					fmt.Fprintf(os.Stderr, "Your organization has already provisioned: %s\n", strings.Join(itProfiles, ", "))
					fmt.Fprintf(os.Stderr, "You can likely skip setup and just run `ccauth login`. Continuing will add a personal profile.\n\n")
				}
				if merged.AllowUserProfiles != nil && !*merged.AllowUserProfiles {
					return fmt.Errorf("your organization has locked profiles (allow_user_profiles = false); use a provisioned profile with `ccauth login -p <name>`")
				}
			}

			// setup edits only the user layer.
			cfg, err := config.LoadUserConfig()
			if err != nil {
				return err
			}
			if cfg.Profiles == nil {
				cfg.Profiles = map[string]config.Profile{}
			}

			name := ask(r, "Profile name", firstNonEmpty(cfg.DefaultProfile, "default"))

			provider := askChoice(r, "Identity provider", []string{"entra", "google", "oidc"}, "entra")
			gw := askChoice(r, "Gateway", []string{"litellm", "portkey", "bifrost", "generic"}, "litellm")

			prof := config.Profile{
				Provider: config.Provider(provider),
				Gateway:  config.Gateway(gw),
			}

			switch provider {
			case "entra":
				prof.OAuth.TenantID = ask(r, "Entra tenant ID (GUID or verified domain)", "")
				prof.OAuth.ClientID = ask(r, "App registration (client) ID of your CLI app", "")
				scope := ask(r, "API scope your gateway validates (aud)", "api://<gateway-app-id>/.default")
				prof.OAuth.Scopes = []string{scope}
			case "google":
				prof.OAuth.ClientID = ask(r, "OAuth client ID (Desktop app type)", "")
				prof.OAuth.ClientSecret = ask(r, "OAuth client secret", "")
				fmt.Fprintln(os.Stderr, "  note: Google access tokens are opaque, so this profile emits the ID token (aud = your client ID).")
			case "oidc":
				prof.OAuth.Issuer = ask(r, "OIDC issuer URL (e.g. https://your.okta.com)", "")
				prof.OAuth.ClientID = ask(r, "Client ID", "")
				prof.OAuth.ClientSecret = ask(r, "Client secret (blank for public client + PKCE)", "")
				scopes := ask(r, "Scopes (space separated)", "openid profile email")
				prof.OAuth.Scopes = strings.Fields(scopes)
				prof.OAuth.Credential = config.TokenKind(askChoice(r, "Emit which token to the gateway", []string{"access", "id"}, "access"))
			}

			prof.OAuth.Flow = config.Flow(askChoice(r, "Login flow", []string{"auth_code", "device_code"}, "auth_code"))

			// Let the helper re-open a browser automatically when the session
			// expires, so users don't have to drop to a terminal mid-session.
			interactive := strings.EqualFold(ask(r, "Auto re-authenticate in a browser when the session expires? (yes/no)", "yes"), "yes")
			prof.HelperInteractive = &interactive

			gi, _ := gateway.Get(config.Gateway(gw))
			prof.GatewayOpts.BaseURL = ask(r, "Gateway base URL", defaultBaseURL(config.Gateway(gw)))
			prof.GatewayOpts.TTLms = 3000000 // 50 min; below typical token lifetime, refreshed on demand

			prof.Mode = config.Mode(askChoice(r, "Credential mode", []string{"passthrough", "exchange"}, string(gi.DefaultMode)))
			if prof.Mode == config.ModeExchange {
				prof.Exchange.Style = askChoice(r, "Exchange style", []string{"rfc8693", "broker"}, "rfc8693")
				prof.Exchange.TokenURL = ask(r, "Token-exchange / broker URL", "")
				if prof.Exchange.Style == "rfc8693" {
					prof.Exchange.Audience = ask(r, "Exchange audience (identifies the gateway)", string(gw))
				} else {
					prof.Exchange.KeyField = ask(r, "JSON field in the broker response holding the gateway key", "key")
				}
			}

			if gw == "portkey" {
				slug := ask(r, "Portkey provider slug (x-portkey-provider)", "@anthropic-prod")
				prof.GatewayOpts.Headers = map[string]string{"x-portkey-provider": slug}
			}

			cfg.Profiles[name] = prof
			if cfg.DefaultProfile == "" {
				cfg.DefaultProfile = name
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "\n✓ Wrote profile %q to %s\n", name, config.Path())

			resolved, err := cfg.Resolve(name)
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠ profile needs attention: %v\n", err)
				resolved = prof
			}

			js, _ := settingsJSON(name, resolved)
			fmt.Fprintf(os.Stderr, "\nClaude Code wiring (%s):\n", claudeSettingsPath())
			fmt.Fprintln(cmd.OutOrStdout(), js)

			if write {
				if err := mergeClaudeSettings(name, resolved); err != nil {
					return fmt.Errorf("failed to update %s: %w", claudeSettingsPath(), err)
				}
				fmt.Fprintf(os.Stderr, "\n✓ Merged wiring into %s\n", claudeSettingsPath())
			} else {
				fmt.Fprintln(os.Stderr, "\nRe-run with --write to merge that into your Claude Code settings automatically.")
			}

			if len(gi.SetupNotes) > 0 {
				fmt.Fprintf(os.Stderr, "\nGateway-side setup for %s:\n", gi.Name)
				for _, n := range gi.SetupNotes {
					fmt.Fprintf(os.Stderr, "  • %s\n", n)
				}
			}
			fmt.Fprintf(os.Stderr, "\nNext: run `ccauth login --profile %s`, then start Claude Code.\n", name)
			return nil
		},
	}
	c.Flags().BoolVar(&write, "write", false, "also merge the wiring into ~/.claude/settings.json")
	return c
}

func newInitCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write a commented example config file you can edit by hand",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := config.Path()
			if _, err := os.Stat(path); err == nil && !force {
				return fmt.Errorf("%s already exists (use --force to overwrite)", path)
			}
			if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(path, []byte(exampleConfig), 0o600); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "Wrote example config to %s — edit it, then run `ccauth login`.\n", path)
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return c
}

func newWireCmd() *cobra.Command {
	var write bool
	c := &cobra.Command{
		Use:   "wire",
		Short: "Print (or --write) the Claude Code settings.json wiring for a profile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, prof, _, err := resolve()
			if err != nil {
				return err
			}
			if write {
				if err := mergeClaudeSettings(name, prof); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "Merged wiring for %q into %s\n", name, claudeSettingsPath())
				return nil
			}
			js, err := settingsJSON(name, prof)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), js)
			return nil
		},
	}
	c.Flags().BoolVar(&write, "write", false, "merge into ~/.claude/settings.json instead of printing")
	return c
}

// ----- prompt helpers ----------------------------------------------------

func isTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func ask(r *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", prompt, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", prompt)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askChoice(r *bufio.Reader, prompt string, choices []string, def string) string {
	for {
		v := ask(r, fmt.Sprintf("%s (%s)", prompt, strings.Join(choices, "/")), def)
		for _, c := range choices {
			if strings.EqualFold(v, c) {
				return c
			}
		}
		fmt.Fprintf(os.Stderr, "  please choose one of: %s\n", strings.Join(choices, ", "))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
