// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/credential"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
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
			line := func(status, msg string) { fmt.Fprintf(out, "%s %s\n", status, msg) }

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
			fmt.Fprintf(out, "\nClaude Code wiring for %s:\n%s\n", claudeSettingsPath(), js)

			// 6. Gateway-side checklist.
			if gi, found := gateway.Get(prof.Gateway); found && len(gi.SetupNotes) > 0 {
				fmt.Fprintf(out, "\nGateway-side setup for %s:\n", gi.Name)
				for _, n := range gi.SetupNotes {
					fmt.Fprintf(out, "  • %s\n", n)
				}
			}

			// 7. Optional live probe.
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
				fmt.Fprintln(os.Stderr, "\nSome checks need attention (see ✗/⚠ above).")
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
