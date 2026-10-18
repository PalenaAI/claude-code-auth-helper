// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

func newSetupCmd() *cobra.Command {
	var write, accessible bool
	c := &cobra.Command{
		Use:   "setup",
		Short: "Interactive wizard: choose a provider + gateway and write the config",
		Long:  "Walks you through picking an identity provider and gateway, collects the required details (Entra tenant/client IDs, Google client ID, OIDC issuer, gateway URL), writes a profile to the config file, and wires it into Claude Code the way you choose.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !isTTY() {
				return fmt.Errorf("setup needs an interactive terminal; run `ccauth init` to write an example config you can edit by hand")
			}
			errw := os.Stderr

			// If IT has already provisioned profiles via a managed/remote/embedded
			// layer, most users just need `ccauth login`.
			if merged, err := config.Load(); err == nil {
				var itProfiles []string
				for pn, src := range merged.Sources {
					if src != "user" {
						itProfiles = append(itProfiles, fmt.Sprintf("%s (%s)", pn, src))
					}
				}
				if merged.AllowUserProfiles != nil && !*merged.AllowUserProfiles {
					return fmt.Errorf("your organization has locked profiles (allow_user_profiles = false); use a provisioned profile with `ccauth login -p <name>`")
				}
				if len(itProfiles) > 0 {
					sort.Strings(itProfiles)
					ui.Info(errw, "Your organization has already provisioned: %s", ui.Val(strings.Join(itProfiles, ", ")))
					ui.Println(errw, "  "+ui.Hint("You can likely skip setup and just run ")+ui.Cmd("ccauth login")+ui.Hint(". Continuing adds a personal profile."))
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

			a := setupAnswers{
				Name: firstNonEmpty(cfg.DefaultProfile, "default"), Provider: "entra", Gateway: "litellm",
				Credential: "access", Flow: "auth_code", AutoReauth: true, ExStyle: "rfc8693", Use: "launcher",
			}
			run := func(groups ...*huh.Group) error {
				return huh.NewForm(groups...).
					WithTheme(setupTheme()).
					WithAccessible(accessible || os.Getenv("ACCESSIBLE") != "").
					WithOutput(errw).
					Run()
			}
			if err := runSetupWizard(&a, !write, run); err != nil {
				if errors.Is(err, huh.ErrUserAborted) {
					return fmt.Errorf("setup cancelled; nothing was written")
				}
				return err
			}
			if write {
				a.Use = "global"
			}

			prof, err := a.profile()
			if err != nil {
				return err
			}
			name := a.Name
			if _, exists := cfg.Profiles[name]; exists {
				ui.Warn(errw, "Replacing the existing profile %s", ui.Val(name))
			}
			cfg.Profiles[name] = prof
			if cfg.DefaultProfile == "" {
				cfg.DefaultProfile = name
			}
			if err := config.Save(cfg); err != nil {
				return err
			}
			ui.Println(errw)
			ui.OK(errw, "Saved profile %s to %s", ui.Bold(name), ui.Val(config.Path()))

			resolved, err := cfg.Resolve(name)
			if err != nil {
				ui.Warn(errw, "The profile needs attention: %v", err)
				resolved = prof
			}

			// Wire it into Claude Code as chosen.
			use := ""
			switch a.Use {
			case "launcher":
				use = ui.Cmd("ccauth claude -p "+name) + ui.Hint("  (settings files untouched)")
			default:
				path := claudeSettingsPath()
				if a.Use == "project" {
					cwd, err := os.Getwd()
					if err != nil {
						return err
					}
					path = projectSettingsPath(cwd)
				}
				js, _ := settingsJSON(name, resolved)
				fmt.Fprintln(cmd.OutOrStdout(), js)
				notes, err := wireFile(path, name, resolved)
				if err != nil {
					return fmt.Errorf("failed to update %s: %w", path, err)
				}
				notes = append(notes, competingElsewhere(path, os.Getenv)...)
				undo := "ccauth unwire"
				if a.Use == "project" {
					undo += " --project"
				}
				ui.OK(errw, "Wired Claude Code in %s %s", ui.Val(path), ui.Hint("(undo: "+undo+")"))
				printNotes(notes)
				use = ui.Val(path)
			}

			// Summary: what was set up and what the user must do.
			gi, _ := gateway.Get(resolved.Gateway)
			rows := [][2]string{
				{"Identity", identityLabel(resolved)},
				{"Gateway", fmt.Sprintf("%s %s", gi.Name, ui.Val(resolved.GatewayOpts.BaseURL))},
				{"Credential", fmt.Sprintf("%s · %s token", resolved.Mode, resolved.OAuth.Credential)},
				{"Claude Code", use},
			}
			if resolved.OAuth.Flow == config.FlowAuthCode {
				uri, portMatters := resolved.RedirectURIToRegister()
				where := "register in your IdP app"
				if resolved.Provider == config.ProviderEntra {
					where = "Entra → Authentication → Mobile and desktop applications"
				}
				note := ""
				if !portMatters {
					note = ui.Hint("  any port matches")
				}
				rows = append(rows, [2]string{"Redirect URI", ui.Val(uri) + note + "\n" + strings.Repeat(" ", len("Redirect URI")+2) + ui.Hint(where)})
			}
			ui.Card(errw, "Profile "+name, ui.KV(rows...)...)

			if notes := gi.Notes(resolved); len(notes) > 0 {
				ui.Heading(errw, "Gateway-side setup for "+gi.Name)
				for _, n := range notes {
					ui.Info(errw, "%s", n)
				}
			}

			ui.Heading(errw, "Next steps")
			ui.Println(errw, "  1. "+ui.Cmd("ccauth login -p "+name)+ui.Hint("   sign in once (browser)"))
			switch a.Use {
			case "launcher":
				ui.Println(errw, "  2. "+ui.Cmd("ccauth claude -p "+name)+ui.Hint("   start Claude Code on the gateway"))
				ui.Println(errw, "     "+ui.Hint("tip: ")+ui.Cmd(fmt.Sprintf("alias claude-work='ccauth claude -p %s'", name))+ui.Hint("  — plain `claude` keeps your own subscription"))
			default:
				ui.Println(errw, "  2. "+ui.Cmd("claude")+ui.Hint("   start Claude Code; `/status` should show apiKeyHelper"))
			}
			ui.Println(errw, "     "+ui.Hint("Claude Desktop too: ")+ui.Cmd("ccauth desktop apply -p "+name))
			return nil
		},
	}
	c.Flags().BoolVar(&write, "write", false, "wire the profile into ~/.claude/settings.json (every session) without asking")
	c.Flags().BoolVar(&accessible, "accessible", false, "plain prompts for screen readers (also: ACCESSIBLE=1)")
	return c
}

// identityLabel names the IdP compactly for the summary card.
func identityLabel(p config.Profile) string {
	switch p.Provider {
	case config.ProviderEntra:
		return providerLabel(p.Provider) + "  " + ui.Hint("tenant "+p.OAuth.TenantID)
	case config.ProviderOIDC:
		return providerLabel(p.Provider) + "  " + ui.Hint(p.Issuer())
	default:
		return providerLabel(p.Provider)
	}
}

// providerLabel is a provider's display name.
func providerLabel(p config.Provider) string {
	switch p {
	case config.ProviderEntra:
		return "Microsoft Entra ID"
	case config.ProviderGoogle:
		return "Google Workspace"
	default:
		return "OpenID Connect"
	}
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
			ui.OK(os.Stderr, "Wrote an example config to %s — edit it, then run %s", ui.Val(path), ui.Cmd("ccauth login"))
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite an existing config file")
	return c
}

func newWireCmd() *cobra.Command {
	var write, project bool
	var dir string
	var headers []string
	c := &cobra.Command{
		Use:   "wire",
		Short: "Print, or write into Claude Code settings, the wiring for a profile",
		Long: `Without flags, prints the Claude Code settings for the profile.
  --write    merge it into ~/.claude/settings.json (every Claude Code session)
  --project  merge it into this repository's .claude/settings.local.json
             (Claude Code sessions in this repository only; never committed)
` + "`ccauth unwire`" + ` removes what ccauth wrote. For one-off sessions without touching any
file, use ` + "`ccauth claude`" + `.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if write && project {
				return fmt.Errorf("choose one of --write (all sessions) and --project (this repository)")
			}
			extra, err := parseHeaders(headers)
			if err != nil {
				return err
			}
			name, prof, _, err := resolve()
			if err != nil {
				return err
			}
			if !write && !project {
				js, err := settingsJSON(name, prof, extra...)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), js)
				return nil
			}
			path, err := wireTarget(project, dir)
			if err != nil {
				return err
			}
			notes, err := wireFile(path, name, prof, extra...)
			if err != nil {
				return fmt.Errorf("failed to update %s: %w", path, err)
			}
			notes = append(notes, competingElsewhere(path, os.Getenv)...)
			ui.OK(os.Stderr, "Wired profile %s into %s", ui.Bold(name), ui.Val(path))
			if project {
				ui.Info(os.Stderr, "Claude Code runs a project-level apiKeyHelper only after you trust the folder.")
			}
			printNotes(notes)
			return nil
		},
	}
	c.Flags().BoolVar(&write, "write", false, "merge into ~/.claude/settings.json (every session)")
	c.Flags().BoolVar(&project, "project", false, "merge into this repository's .claude/settings.local.json")
	c.Flags().StringVar(&dir, "dir", "", "with --project: the directory to wire instead of the current one")
	c.Flags().StringArrayVarP(&headers, "header", "H", nil, `extra gateway header, "Name: value" (repeatable)`)
	return c
}

func newUnwireCmd() *cobra.Command {
	var project bool
	var dir string
	c := &cobra.Command{
		Use:   "unwire",
		Short: "Remove ccauth's wiring from Claude Code settings, keeping everything else",
		Long: `Removes the apiKeyHelper and gateway env that ccauth wrote, from
~/.claude/settings.json, or with --project from this repository's
.claude/settings.local.json. Keys you changed afterwards are left in place, and
the rest of the file is untouched, so Claude Code falls back to your own
subscription or API key.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := wireTarget(project, dir)
			if err != nil {
				return err
			}
			removed, notes, err := unwireFile(path, project)
			if err != nil {
				return fmt.Errorf("failed to update %s: %w", path, err)
			}
			if len(removed) == 0 {
				ui.Info(os.Stderr, "No ccauth wiring found in %s", ui.Val(path))
			} else {
				ui.OK(os.Stderr, "Removed %s from %s", strings.Join(removed, ", "), ui.Val(path))
			}
			printNotes(notes)
			return nil
		},
	}
	c.Flags().BoolVar(&project, "project", false, "remove it from this repository's .claude/settings.local.json")
	c.Flags().StringVar(&dir, "dir", "", "with --project: the directory instead of the current one")
	return c
}

// wireTarget returns the user settings file, or the project-local one.
func wireTarget(project bool, dir string) (string, error) {
	if !project {
		if dir != "" {
			return "", fmt.Errorf("--dir only applies together with --project")
		}
		return claudeSettingsPath(), nil
	}
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return projectSettingsPath(abs), nil
}

// printNotes prints wiring notes (migrations, precedence conflicts) to stderr.
func printNotes(notes []string) {
	for _, n := range notes {
		ui.Warn(os.Stderr, "%s", n)
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
