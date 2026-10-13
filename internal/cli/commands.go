// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/credential"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
	"github.com/PalenaAI/claude-code-auth-helper/internal/jwtutil"
	"github.com/PalenaAI/claude-code-auth-helper/internal/oidc"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

func newLoginCmd() *cobra.Command {
	var device bool
	c := &cobra.Command{
		Use:   "login",
		Short: "Sign in to your SSO provider and store the session",
		Long:  "Runs the interactive OAuth flow (browser by default, or --device for headless) and stores the refresh token in your OS keychain.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, prof, st, err := resolve()
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			sess, err := oidc.Login(ctx, prof, device)
			if err != nil {
				return err
			}
			if err := st.Save(name, sess); err != nil {
				return fmt.Errorf("sign-in succeeded but storing the session failed: %w", err)
			}
			who := ""
			if c, _ := jwtutil.Parse(sess.IDToken); c.Email != "" {
				who = " as " + c.Email
			}
			ui.OK(os.Stderr, "Signed in%s on profile %s %s", who, ui.Bold(name),
				ui.Hint(fmt.Sprintf("(%s → %s, session in the %s store)", prof.Provider, prof.Gateway, st.Kind())))
			return nil
		},
	}
	c.Flags().BoolVar(&device, "device", false, "use the device-code flow (headless / SSH / no local browser)")
	return c
}

func newTokenCmd() *cobra.Command {
	var minLifetime time.Duration
	c := &cobra.Command{
		Use:   "token",
		Short: "Print the current gateway credential (this is what Claude Code calls)",
		Long: `Prints the credential Claude Code should send to the gateway, to stdout.

This is the command you point apiKeyHelper at. It is fast: it returns a cached
token when one is fresh and silently refreshes when near expiry. When the refresh
token itself is gone (revoked/expired) it will, by default, open a browser to
re-authenticate — so you don't have to drop back to a terminal mid-session. In
headless/CI environments (or with helper_interactive = false) it instead exits
non-zero telling you to run ` + "`ccauth login`" + `.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, prof, st, err := resolve()
			if err != nil {
				return err
			}
			cred, err := emitCredentialFor(prof, st, name, minLifetime)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), cred)
			return nil
		},
	}
	c.Flags().DurationVar(&minLifetime, "min-lifetime", 0,
		"require the token to stay valid this long (Claude Desktop's helper passes its cache TTL); default: Claude Code's CLAUDE_CODE_API_KEY_HELPER_TTL_MS")
	return c
}

// emitCredential returns the credential, escalating to an interactive browser
// login when silent refresh fails and the profile/environment allow it.
func emitCredential(prof config.Profile, st store.Store, name string) (string, error) {
	return emitCredentialFor(prof, st, name, 0)
}

// helperContext is the CLAUDE_HELPER_CONTEXT Claude Desktop sets when it runs
// ccauth as its credential helper ("" when called by Claude Code or a user).
func helperContext() string { return os.Getenv("CLAUDE_HELPER_CONTEXT") }

// emitTimeout bounds a silent refresh. Desktop kills a mid-session refresh after
// 20s, so stay under it there; everyone else keeps 30s.
func emitTimeout() time.Duration {
	if helperContext() == "mid-session-refresh" {
		return 15 * time.Second
	}
	return 30 * time.Second
}

func emitCredentialFor(prof config.Profile, st store.Store, name string, minLifetime time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), emitTimeout())
	defer cancel()
	cred, err := credential.EmitFor(ctx, prof, st, name, minLifetime)
	if err == nil {
		return cred, nil
	}
	if !errors.Is(err, credential.ErrLoginRequired) {
		return "", err
	}
	if !canAutoLogin(prof) {
		return "", fmt.Errorf("no valid session — run `ccauth login --profile %s`", name)
	}
	if lerr := autoLogin(prof, st, name); lerr != nil {
		return "", fmt.Errorf("automatic re-authentication failed (%v) — run `ccauth login --profile %s`", lerr, name)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel2()
	return credential.EmitFor(ctx2, prof, st, name, minLifetime)
}

// canAutoLogin decides whether `ccauth token` may open a browser to re-auth.
func canAutoLogin(prof config.Profile) bool {
	if os.Getenv("CCAUTH_NONINTERACTIVE") != "" || os.Getenv("CI") != "" {
		return false
	}
	// Claude Desktop marks each helper run; it expects a browser only when the
	// user is actively signing in, and a silent path (or a non-zero exit) for
	// mid-session refreshes, scheduled tasks and background runs.
	if c := helperContext(); c != "" && c != "interactive" {
		return false
	}
	if !prof.InteractiveHelper() {
		return false
	}
	if prof.OAuth.Flow == config.FlowDeviceCode {
		return false // device flow needs the user to read a code we can't surface from the helper
	}
	return oidc.BrowserAvailable()
}

// autoLogin runs one interactive browser login under a lock so concurrent helper
// invocations open a single browser tab, not one each.
func autoLogin(prof config.Profile, st store.Store, name string) error {
	release, err := store.AcquireLoginLock(name, 3*time.Minute)
	if err != nil {
		return err
	}
	defer release()

	// Another invocation may have re-authenticated while we waited for the lock.
	recheck, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, e := credential.Emit(recheck, prof, st, name); e == nil {
		return nil
	}

	ui.Info(os.Stderr, "ccauth: your session expired — opening a browser to re-authenticate…")
	ctx, cancelLogin := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancelLogin()
	sess, err := oidc.Login(ctx, prof, false)
	if err != nil {
		return err
	}
	return st.Save(name, sess)
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete the stored session for a profile",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _, st, err := resolve()
			if err != nil {
				return err
			}
			if err := st.Delete(name); err != nil {
				return err
			}
			ui.OK(os.Stderr, "Cleared the session for profile %s", ui.Bold(name))
			return nil
		},
	}
}

func newStatusCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Show the active profile and session state",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, prof, st, err := resolve()
			if err != nil {
				return err
			}
			sess, loadErr := st.Load(name)

			type statusOut struct {
				Profile           string    `json:"profile"`
				Source            string    `json:"config_source,omitempty"`
				Provider          string    `json:"provider"`
				Gateway           string    `json:"gateway"`
				Mode              string    `json:"mode"`
				BaseURL           string    `json:"base_url"`
				Credential        string    `json:"credential_token"`
				StoreBackend      string    `json:"store_backend"`
				InteractiveHelper bool      `json:"interactive_helper"`
				LoggedIn          bool      `json:"logged_in"`
				Email             string    `json:"email,omitempty"`
				IDPExpiry         time.Time `json:"idp_expiry,omitempty"`
				EmitExpiry        time.Time `json:"emit_expiry,omitempty"`
				NeedsLogin        bool      `json:"needs_login"`
				Message           string    `json:"message,omitempty"`
			}
			out := statusOut{
				Profile:           name,
				Provider:          string(prof.Provider),
				Gateway:           string(prof.Gateway),
				Mode:              string(prof.Mode),
				BaseURL:           prof.GatewayOpts.BaseURL,
				Credential:        string(prof.OAuth.Credential),
				StoreBackend:      st.Kind(),
				InteractiveHelper: prof.InteractiveHelper(),
			}
			if c, e := config.Load(); e == nil {
				out.Source = c.Sources[name]
			}
			if loadErr == nil && sess != nil {
				out.LoggedIn = true
				out.IDPExpiry = sess.IDPExpiry
				out.EmitExpiry = sess.EmitExpiry
				if cl, _ := jwtutil.Parse(sess.IDToken); cl.Email != "" {
					out.Email = cl.Email
				}
				if time.Until(sess.IDPExpiry) <= 0 && sess.RefreshToken == "" {
					out.NeedsLogin = true
				}
			} else if errors.Is(loadErr, store.ErrNoSession) {
				out.NeedsLogin = true
				out.Message = "not logged in — run `ccauth login`"
			} else if loadErr != nil {
				out.Message = "session load error: " + loadErr.Error()
			}

			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(out)
			}

			w := cmd.OutOrStdout()
			state := ui.Glyph("✓") + " signed in"
			if !out.LoggedIn {
				state = ui.Glyph("✗") + " not signed in — run " + ui.Cmd("ccauth login -p "+out.Profile)
			} else if out.NeedsLogin {
				state = ui.Glyph("⚠") + " session needs a new sign-in — run " + ui.Cmd("ccauth login -p "+out.Profile)
			}
			rows := [][2]string{{"Profile", ui.Bold(out.Profile)}}
			if out.Source != "" {
				rows = append(rows, [2]string{"Config source", out.Source})
			}
			rows = append(rows,
				[2]string{"Provider", out.Provider},
				[2]string{"Gateway", out.Gateway + "  " + ui.Val(out.BaseURL)},
				[2]string{"Mode", fmt.Sprintf("%s · emits the %s token", out.Mode, out.Credential)},
				[2]string{"Store", out.StoreBackend},
				[2]string{"Auto re-auth", fmt.Sprintf("%t", out.InteractiveHelper)},
				[2]string{"Session", state},
			)
			if out.Email != "" {
				rows = append(rows, [2]string{"Identity", out.Email})
			}
			if !out.IDPExpiry.IsZero() {
				rows = append(rows, [2]string{"IdP token expires", fmt.Sprintf("%s %s", out.IDPExpiry.Format(time.RFC3339), ui.Hint("("+humanUntil(out.IDPExpiry)+")"))})
			}
			if !out.EmitExpiry.IsZero() {
				rows = append(rows, [2]string{"Credential expires", fmt.Sprintf("%s %s", out.EmitExpiry.Format(time.RFC3339), ui.Hint("("+humanUntil(out.EmitExpiry)+")"))})
			}
			if out.Message != "" && out.LoggedIn {
				rows = append(rows, [2]string{"Note", out.Message})
			}
			for _, r := range ui.KV(rows...) {
				ui.Println(w, r)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "output machine-readable JSON")
	return c
}

func newGatewaysCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "gateways",
		Short: "List supported gateways and how each authenticates",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "GATEWAY\tNATIVE JWT\tDEFAULT MODE\tBASE URL HINT")
			for _, g := range gateway.All() {
				native := "no"
				if g.NativeJWT {
					native = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", g.Name, native, g.DefaultMode, g.BaseURLHint)
			}
			return w.Flush()
		},
	}
}

func humanUntil(t time.Time) string {
	d := time.Until(t)
	if d <= 0 {
		return "expired"
	}
	if d < time.Minute {
		return fmt.Sprintf("in %ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("in %dm", int(d.Minutes()))
	}
	return fmt.Sprintf("in %dh%dm", int(d.Hours()), int(d.Minutes())%60)
}
