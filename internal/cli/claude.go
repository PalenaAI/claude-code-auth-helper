// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

func newClaudeCmd() *cobra.Command {
	var headers []string
	var isolated bool
	var configDir string
	c := &cobra.Command{
		Use:   "claude [prompt] [-- claude-flags...]",
		Short: "Start Claude Code on this profile's gateway for one session, without touching settings.json",
		Long: `Starts Claude Code wired to the profile's gateway for this session only: it passes
the apiKeyHelper and gateway env with ` + "`claude --settings`" + `, which overrides your
settings files for that session and writes nothing to them. Plain ` + "`claude`" + ` keeps
using your own subscription, and both can run side by side.

Pass Claude Code's own flags after "--", e.g.  ccauth claude -p work -- --resume
Add per-session gateway headers with -H "Name: value" (repeatable); they override
the profile's headers with the same name.

Variables that would take precedence over the helper (ANTHROPIC_AUTH_TOKEN,
ANTHROPIC_API_KEY, CLAUDE_CODE_USE_BEDROCK/VERTEX/FOUNDRY) are removed from the
session's environment.

Tip:  alias claude-work='ccauth claude -p work'`,
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			for _, a := range args {
				if a == "--settings" || strings.HasPrefix(a, "--settings=") {
					return fmt.Errorf("ccauth claude already passes --settings; put those settings in your settings files instead")
				}
			}
			extra, err := parseHeaders(headers)
			if err != nil {
				return err
			}
			name, prof, _, err := resolve()
			if err != nil {
				return err
			}
			bin, err := claudeBinary()
			if err != nil {
				return err
			}
			settingsPath, err := writeLaunchSettings(name, prof, extra)
			if err != nil {
				return err
			}
			if isolated && configDir == "" {
				home, _ := os.UserHomeDir()
				configDir = filepath.Join(home, ".claude-"+name)
			}
			env, dropped := launchEnv(os.Environ(), buildEnv(prof, extra...), configDir)
			for _, k := range dropped {
				ui.Info(os.Stderr, "ccauth: unset %s for this session %s", ui.Val(k), ui.Hint("(it would take precedence over apiKeyHelper)"))
			}
			argv := append([]string{bin, "--settings", settingsPath}, args...)
			return execClaude(bin, argv, env)
		},
	}
	// Stop at the first positional argument or "--" so Claude Code's own flags
	// pass through untouched.
	c.Flags().SetInterspersed(false)
	c.Flags().StringArrayVarP(&headers, "header", "H", nil, `extra gateway header for this session, "Name: value" (repeatable)`)
	c.Flags().BoolVar(&isolated, "isolated", false, "use a separate Claude Code config dir (~/.claude-<profile>): own history, memory, MCP servers and plugins")
	c.Flags().StringVar(&configDir, "config-dir", "", "Claude Code config dir for this session (implies --isolated)")
	return c
}

// parseHeaders parses repeated -H "Name: value" flags.
func parseHeaders(raw []string) ([]gateway.Header, error) {
	out := make([]gateway.Header, 0, len(raw))
	for _, r := range raw {
		h, err := gateway.ParseHeader(r)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, nil
}

// claudeBinary finds the Claude Code CLI; CCAUTH_CLAUDE_BIN overrides the lookup.
func claudeBinary() (string, error) {
	if b := os.Getenv("CCAUTH_CLAUDE_BIN"); b != "" {
		return b, nil
	}
	b, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude not found on PATH (install Claude Code, or set CCAUTH_CLAUDE_BIN)")
	}
	return b, nil
}

// writeLaunchSettings writes the session settings for `claude --settings` to a
// 0600 file in the state dir rather than the command line, so header values
// don't show up in the process list.
func writeLaunchSettings(name string, prof config.Profile, extra []gateway.Header) (string, error) {
	dir := filepath.Join(config.StateDir(), "launch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	settings := wiringSettings(name, prof, extra...)
	// An empty value counts as unset in Claude Code, and --settings outranks
	// every settings file, so this keeps a token or cloud-provider switch from
	// ~/.claude/settings.json or the project from taking over this session.
	env := settings["env"].(map[string]string)
	for _, k := range competingVars() {
		env[k] = ""
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

// launchEnv builds the child environment: it drops variables that outrank
// apiKeyHelper, sets the gateway env (also passed via --settings, so the helper
// sees it either way), and points CLAUDE_CONFIG_DIR at configDir when set.
func launchEnv(base []string, gw map[string]string, configDir string) (env, dropped []string) {
	drop := map[string]bool{}
	for _, k := range competingVars() {
		drop[k] = true
	}
	override := map[string]string{}
	for k, v := range gw {
		override[k] = v
	}
	if configDir != "" {
		override["CLAUDE_CONFIG_DIR"] = configDir
	}
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if drop[k] {
			dropped = append(dropped, k)
			continue
		}
		if _, ok := override[k]; ok {
			continue
		}
		env = append(env, kv)
	}
	keys := make([]string, 0, len(override))
	for k := range override {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+override[k])
	}
	sort.Strings(dropped)
	return env, dropped
}
