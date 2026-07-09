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
			fmt.Fprintf(os.Stderr, "Signed in%s on profile %q (%s → %s). Session stored via %q backend.\n",
				who, name, prof.Provider, prof.Gateway, st.Kind())
			return nil
		},
	}
	c.Flags().BoolVar(&device, "device", false, "use the device-code flow (headless / SSH / no local browser)")
	return c
}

func newTokenCmd() *cobra.Command {
	return &cobra.Command{
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
			cred, err := emitCredential(prof, st, name)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), cred)
			return nil
		},
	}
}

// emitCredential returns the credential, escalating to an interactive browser
// login when silent refresh fails and the profile/environment allow it.
func emitCredential(prof config.Profile, st store.Store, name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cred, err := credential.Emit(ctx, prof, st, name)
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
	return credential.Emit(ctx2, prof, st, name)
}

// canAutoLogin decides whether `ccauth token` may open a browser to re-auth.
func canAutoLogin(prof config.Profile) bool {
	if os.Getenv("CCAUTH_NONINTERACTIVE") != "" || os.Getenv("CI") != "" {
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

	fmt.Fprintln(os.Stderr, "ccauth: your session expired — opening a browser to re-authenticate…")
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
			fmt.Fprintf(os.Stderr, "Cleared session for profile %q.\n", name)
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

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintf(w, "Profile:\t%s\n", out.Profile)
			if out.Source != "" {
				fmt.Fprintf(w, "Config source:\t%s\n", out.Source)
			}
			fmt.Fprintf(w, "Provider:\t%s\n", out.Provider)
			fmt.Fprintf(w, "Gateway:\t%s\n", out.Gateway)
			fmt.Fprintf(w, "Mode:\t%s\n", out.Mode)
			fmt.Fprintf(w, "Base URL:\t%s\n", out.BaseURL)
			fmt.Fprintf(w, "Emits:\t%s token\n", out.Credential)
			fmt.Fprintf(w, "Store:\t%s\n", out.StoreBackend)
			fmt.Fprintf(w, "Auto re-auth:\t%t\n", out.InteractiveHelper)
			fmt.Fprintf(w, "Logged in:\t%t\n", out.LoggedIn)
			if out.Email != "" {
				fmt.Fprintf(w, "Identity:\t%s\n", out.Email)
			}
			if !out.IDPExpiry.IsZero() {
				fmt.Fprintf(w, "IdP token expires:\t%s (%s)\n", out.IDPExpiry.Format(time.RFC3339), humanUntil(out.IDPExpiry))
			}
			if !out.EmitExpiry.IsZero() {
				fmt.Fprintf(w, "Credential expires:\t%s (%s)\n", out.EmitExpiry.Format(time.RFC3339), humanUntil(out.EmitExpiry))
			}
			if out.Message != "" {
				fmt.Fprintf(w, "Note:\t%s\n", out.Message)
			}
			return w.Flush()
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
