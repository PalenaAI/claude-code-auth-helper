// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/credential"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

func newDoctorCmd() *cobra.Command {
	var probe bool
	c := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose configuration and print the gateway-side checklist",
		Long:  "Checks the config file, resolves the active profile, reports session state, prints the Claude Code wiring, and lists what the gateway operator must configure. With --probe it actually fetches a credential (redacted) end-to-end.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ok := true
			line := func(status, msg string) { ui.Status(out, status, msg) }

			// 1. Config file.
			if _, err := os.Stat(config.Path()); err != nil {
				line("✗", fmt.Sprintf("config file %s not found — run `ccauth setup` or `ccauth init`", config.Path()))
				return nil
			}
			line("✓", "config file: "+config.Path())

			// 2. Resolve profile.
			name, prof, st, err := resolve()
			if err != nil {
				line("✗", "profile: "+err.Error())
				return nil
			}
			line("✓", fmt.Sprintf("profile %q: %s → %s (%s mode, emits %s token)",
				name, prof.Provider, prof.Gateway, prof.Mode, prof.OAuth.Credential))
			line("•", "issuer: "+prof.Issuer())
			if prof.OAuth.Flow == config.FlowAuthCode {
				uri, portMatters := prof.RedirectURIToRegister()
				note := " (Entra ignores the port; the /callback path must match)"
				if portMatters {
					note = " (port must match; pin it with oauth.redirect_port)"
				}
				line("•", "redirect URI to register: "+uri+note)
			}
			if c, e := config.Load(); e == nil && c.Sources[name] != "" {
				line("•", "config source: "+c.Sources[name]+" layer")
			}
			line("•", fmt.Sprintf("auto browser re-auth: %t", prof.InteractiveHelper()))

			// 3. Native-JWT sanity check.
			if gi, found := gateway.Get(prof.Gateway); found {
				if !gi.NativeJWT && prof.Mode == config.ModePassthrough {
					ok = false
					line("⚠", fmt.Sprintf("%s has no native inbound JWT validation but mode is passthrough — use exchange mode or a gateway plugin", gi.Name))
				}
			}

			// 4. Session state.
			sess, lerr := st.Load(name)
			switch {
			case errors.Is(lerr, store.ErrNoSession):
				ok = false
				line("✗", fmt.Sprintf("no session (store: %s) — run `ccauth login --profile %s`", st.Kind(), name))
			case lerr != nil:
				ok = false
				line("✗", "session load error: "+lerr.Error())
			default:
				line("✓", fmt.Sprintf("session present (store: %s)", sess.Backend))
				if !sess.IDPExpiry.IsZero() {
					line("•", "IdP token expires "+humanUntil(sess.IDPExpiry))
				}
				if sess.RefreshToken == "" {
					line("⚠", "no refresh token stored — silent refresh unavailable, re-login when it expires")
				}
			}

			// 5. Wiring.
			js, _ := settingsJSON(name, prof)
			ui.Heading(out, "Claude Code wiring for "+claudeSettingsPath())
			fmt.Fprintln(out, js)

			// 6. What Claude Code will actually use (file-based settings + env).
			ui.Heading(out, "Claude Code")
			cwd, _ := os.Getwd()
			layers := loadClaudeLayers(claudeManagedDir(), cwd, claudeSettingsPath())
			findings := checkClaudeCode(layers, os.Getenv, name)
			helperWired := false
			for _, l := range layers {
				if strings.Contains(l.str("apiKeyHelper"), "ccauth") {
					helperWired = true
				}
			}
			actx, acancel := context.WithTimeout(context.Background(), 15*time.Second)
			if st, err := runClaudeAuthStatus(actx, cwd); err != nil {
				findings = append(findings, finding{"•", "could not ask Claude Code which credential it uses: " + err.Error()})
			} else {
				findings = append(findings, authStatusFinding(st, helperWired))
			}
			acancel()
			for _, f := range findings {
				if f.Status == "✗" || f.Status == "⚠" {
					ok = false
				}
				line(f.Status, f.Msg)
			}
			if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
				line("•", "managed settings pushed as an MDM profile / registry policy are not inspected; check `/status` in Claude Code")
			}

			// Claude Desktop (third-party mode) is configured separately.
			ui.Heading(out, "Claude Desktop")
			for _, f := range desktopFindings() {
				line(f.Status, f.Msg)
			}

			// 7. Gateway-side checklist.
			if gi, found := gateway.Get(prof.Gateway); found && len(gi.Notes(prof)) > 0 {
				ui.Heading(out, "Gateway-side setup for "+gi.Name)
				for _, n := range gi.Notes(prof) {
					ui.Info(out, "%s", n)
				}
			}

			// 8. Optional live probe.
			if probe {
				fmt.Fprintln(out)
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cred, perr := credential.Emit(ctx, prof, st, name)
				if perr != nil {
					ok = false
					line("✗", "probe: "+perr.Error())
				} else {
					line("✓", "probe: obtained credential "+redact(cred))
				}
			}

			if !ok {
				ui.Println(os.Stderr)
				ui.Warn(os.Stderr, "Some checks need attention (see %s / %s above).", ui.Glyph("✗"), ui.Glyph("⚠"))
			}
			return nil
		},
	}
	c.Flags().BoolVar(&probe, "probe", false, "actually fetch a credential end-to-end (redacted)")
	return c
}

func redact(s string) string {
	if len(s) <= 12 {
		return "****"
	}
	return s[:6] + "…" + s[len(s)-4:] + fmt.Sprintf(" (%d chars)", len(s))
}
