// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
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
				// Default to the single-app form (CLI app == gateway API), the setup in
				// docs/identity-providers/entra-id.md. Re-ask until Entra would accept it.
				fmt.Fprintln(os.Stderr, "  API scope (token audience): <client-id>/.default if the CLI app is also the gateway's API,")
				fmt.Fprintln(os.Stderr, "  else api://<gateway-app-id>/.default for a separate gateway app.")
				for {
					scope := ask(r, "API scope your gateway validates (aud)", prof.OAuth.ClientID+"/.default")
					if err := config.ValidateEntraScopes(prof.OAuth.ClientID, []string{scope}); err != nil {
						fmt.Fprintf(os.Stderr, "  %v\n", err)
						continue
					}
					prof.OAuth.Scopes = []string{scope}
					break
				}
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
			if prof.OAuth.Flow == config.FlowAuthCode {
				// A fixed port is only needed when something forwards it (devcontainers,
				// SSH) or the IdP compares ports exactly; Entra ignores it.
				if p := ask(r, "Fixed callback port (blank = random; set one for devcontainers or IdPs that match ports)", ""); p != "" {
					port, err := strconv.Atoi(p)
					if err != nil || port < 1 || port > 65535 {
						return fmt.Errorf("callback port %q: want a number between 1 and 65535", p)
					}
					prof.OAuth.RedirectPort = port
				}
				uri, portMatters := prof.RedirectURIToRegister()
				fmt.Fprintf(os.Stderr, "  Register this redirect URI in your IdP app (platform: Mobile and desktop applications on Entra): %s\n", uri)
				if !portMatters {
					fmt.Fprintln(os.Stderr, "  Entra ignores the port of localhost redirect URIs, so any port matches; only the /callback path must.")
				}
			}

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

			// Background tasks and the haiku alias use Claude Code's default Haiku
			// model ID, which many gateways don't serve under that name.
			prof.GatewayOpts.HaikuModel = ask(r, "Model for background tasks / haiku alias, if your gateway names it differently (blank = Claude Code default)", "")
			prof.GatewayOpts.ModelDiscovery = strings.EqualFold(ask(r, "Let Claude Code discover models via GET /v1/models on the gateway? (yes/no)", "no"), "yes")

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
				notes, err := mergeClaudeSettings(name, resolved)
				if err != nil {
					return fmt.Errorf("failed to update %s: %w", claudeSettingsPath(), err)
				}
				fmt.Fprintf(os.Stderr, "\n✓ Merged wiring into %s\n", claudeSettingsPath())
				printNotes(notes)
			} else {
				fmt.Fprintln(os.Stderr, "\nRe-run with --write to merge that into your Claude Code settings automatically.")
			}

			if len(gi.Notes(resolved)) > 0 {
				fmt.Fprintf(os.Stderr, "\nGateway-side setup for %s:\n", gi.Name)
				for _, n := range gi.Notes(resolved) {
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
				notes, err := mergeClaudeSettings(name, prof)
				if err != nil {
					return fmt.Errorf("failed to update %s: %w", claudeSettingsPath(), err)
				}
				fmt.Fprintf(os.Stderr, "Merged wiring for %q into %s\n", name, claudeSettingsPath())
				printNotes(notes)
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

// printNotes prints wiring notes (migrations, precedence conflicts) to stderr.
func printNotes(notes []string) {
	for _, n := range notes {
		fmt.Fprintf(os.Stderr, "  ⚠ %s\n", n)
	}
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
