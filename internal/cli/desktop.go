// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/desktop"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

func newDesktopCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "desktop",
		Short: "Configure Claude Desktop (Chat, Cowork and Code tabs) to use the profile's gateway",
		Long: `Claude Desktop doesn't use apiKeyHelper or Claude Code's settings for its
credential. It talks to a gateway only in its third-party inference mode
("Claude Desktop on 3P"), configured through its own keys. These commands write
that configuration from a ccauth profile:

  ccauth desktop apply -p work     # add/update the "ccauth: work" configuration and apply it
  ccauth desktop personal          # apply an empty configuration: back to your Claude.ai sign-in
  ccauth desktop export -p work    # a file to import, or a .mobileconfig / .reg for IT
  ccauth desktop status            # what Desktop will use at its next launch

The configuration applies to the whole app (not per project) from its next
launch. Quit Claude Desktop before apply/personal: it rewrites its configuration
library while running. An MDM profile or registry policy for Claude Desktop
takes precedence over anything written here.

The Claude Code CLI is unaffected: it keeps using apiKeyHelper / ` + "`ccauth claude`" + `.`,
	}
	c.AddCommand(newDesktopApplyCmd(), newDesktopPersonalCmd(), newDesktopExportCmd(), newDesktopStatusCmd())
	return c
}

// desktopFlags are shared by apply and export.
type desktopFlags struct {
	auth       string
	broker     bool
	helperPath string
	helperTTL  time.Duration
	headers    []string
}

func (f *desktopFlags) register(c *cobra.Command) {
	c.Flags().StringVar(&f.auth, "auth", "auto", "how Desktop gets the token: sso (Desktop signs in to the IdP), helper (Desktop runs `ccauth token`), or auto (sso for passthrough public clients, else helper)")
	c.Flags().BoolVar(&f.broker, "broker", false, "with sso on Entra: sign in through Company Portal / Web Account Manager (satisfies device-based Conditional Access)")
	c.Flags().StringVar(&f.helperPath, "helper-path", "", "absolute path Desktop runs for --auth helper (default: this ccauth, via its upgrade-stable link)")
	c.Flags().DurationVar(&f.helperTTL, "helper-ttl", desktop.DefaultHelperTTL, "how long Desktop caches the helper's token")
	c.Flags().StringArrayVarP(&f.headers, "header", "H", nil, `extra gateway header, "Name: value" (repeatable)`)
}

// build resolves the profile and renders its Desktop configuration.
func (f *desktopFlags) build() (string, config.Profile, desktop.Auth, map[string]any, error) {
	extra, err := parseHeaders(f.headers)
	if err != nil {
		return "", config.Profile{}, "", nil, err
	}
	name, prof, _, err := resolve()
	if err != nil {
		return "", config.Profile{}, "", nil, err
	}
	auth, err := desktop.ResolveAuth(f.auth, prof)
	if err != nil {
		return "", config.Profile{}, "", nil, err
	}
	opt := desktop.Options{Auth: auth, Broker: f.broker, HelperTTL: f.helperTTL, Extra: extra, HelperPath: f.helperPath}
	if auth == desktop.AuthHelper && opt.HelperPath == "" {
		if opt.HelperPath, err = desktop.StableHelperPath(); err != nil {
			return "", config.Profile{}, "", nil, err
		}
	}
	settings, err := desktop.Settings(name, prof, opt)
	if err != nil {
		return "", config.Profile{}, "", nil, err
	}
	return name, prof, auth, settings, nil
}

// guardLibraryWrite refuses to touch the library while Desktop runs, and warns
// when managed policy would override it.
func guardLibraryWrite(force bool) error {
	if !force && desktop.Running() {
		return fmt.Errorf("Claude Desktop is running; quit it first (it rewrites its configuration library while running), or pass --force")
	}
	if m := desktop.ManagedSource(); m != "" {
		ui.Warn(os.Stderr, "Managed policy %s is present: Claude Desktop ignores the local configuration library while it exists. Ask IT to deploy %s instead.", ui.Val(m), ui.Cmd("ccauth desktop export"))
	}
	return nil
}

func newDesktopApplyCmd() *cobra.Command {
	var f desktopFlags
	var force bool
	c := &cobra.Command{
		Use:   "apply",
		Short: "Write the profile's gateway configuration into Claude Desktop and apply it",
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, prof, auth, settings, err := f.build()
			if err != nil {
				return err
			}
			if err := guardLibraryWrite(force); err != nil {
				return err
			}
			id, err := desktop.Apply(name, settings)
			if err != nil {
				return fmt.Errorf("update Claude Desktop's configuration library: %w", err)
			}
			ui.OK(os.Stderr, "Applied Claude Desktop configuration %s %s", ui.Bold("ccauth: "+name), ui.Hint("("+id+")"))
			ui.Info(os.Stderr, "Gateway %s · credential: %s", ui.Val(prof.GatewayOpts.BaseURL), describeDesktopAuth(auth, settings))
			switch auth {
			case desktop.AuthSSO:
				uri, _ := prof.RedirectURIToRegister()
				ui.Info(os.Stderr, "Desktop signs in to your IdP itself; register the redirect URI %s %s", ui.Val(uri), ui.Hint("(Mobile and desktop applications on Entra)"))
			case desktop.AuthHelper:
				if st := store.New(prof.Store); st != nil {
					if _, err := st.Load(name); errors.Is(err, store.ErrNoSession) {
						ui.Warn(os.Stderr, "No ccauth session yet: run %s so Desktop's helper has a token.", ui.Cmd("ccauth login -p "+name))
					}
				}
			}
			ui.Heading(os.Stderr, "Next")
			ui.Println(os.Stderr, "  Start Claude Desktop. "+ui.Hint("If it opens your Claude.ai account, sign out and pick your organization's sign-in."))
			ui.Println(os.Stderr, "  "+ui.Hint("Back to Claude.ai later: ")+ui.Cmd("ccauth desktop personal"))
			return nil
		},
	}
	f.register(c)
	c.Flags().BoolVar(&force, "force", false, "write even if Claude Desktop appears to be running")
	return c
}

func describeDesktopAuth(auth desktop.Auth, s map[string]any) string {
	if auth == desktop.AuthHelper {
		return fmt.Sprintf("helper %v %s", s["inferenceCredentialHelper"], strings.Join(s["inferenceCredentialHelperArgs"].([]string), " "))
	}
	if s["inferenceIdpAuthFlow"] == "broker" {
		return "Desktop's built-in SSO via the Entra broker"
	}
	return "Desktop's built-in SSO (browser)"
}

func newDesktopPersonalCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "personal",
		Short: "Apply an empty Claude Desktop configuration so it signs in to Claude.ai again",
		Long: `Applies an empty configuration in Claude Desktop's library, which takes the app out
of third-party mode at its next launch. ccauth's gateway configurations stay in
the library; ` + "`ccauth desktop apply`" + ` switches back.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := guardLibraryWrite(force); err != nil {
				return err
			}
			id, err := desktop.Personal()
			if err != nil {
				return fmt.Errorf("update Claude Desktop's configuration library: %w", err)
			}
			ui.OK(os.Stderr, "Applied an empty Claude Desktop configuration %s: the next launch uses your Claude.ai sign-in.", ui.Hint("("+id+")"))
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "write even if Claude Desktop appears to be running")
	return c
}

func newDesktopExportCmd() *cobra.Command {
	var f desktopFlags
	var format, out string
	var machine bool
	c := &cobra.Command{
		Use:   "export",
		Short: "Write the profile's Claude Desktop configuration to a file (import, MDM or registry)",
		Long: `Formats:
  json          for Developer → Configure Third-Party Inference… → Import configuration,
                or as a bootstrap-server response body
  mobileconfig  macOS configuration profile for Jamf or any Apple MDM
  reg           Windows registry policy (HKCU, or HKLM with --machine) for GPO/Intune

For --auth helper, pass --helper-path with the path ccauth has on the target
machines (e.g. /opt/homebrew/bin/ccauth, or C:\Program Files\ccauth\ccauth.exe).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, _, _, settings, err := f.build()
			if err != nil {
				return err
			}
			var b []byte
			switch format {
			case "json":
				b, err = desktop.ExportJSON(settings)
			case "mobileconfig":
				var pu, du string
				if pu, err = desktop.NewUUID(); err == nil {
					if du, err = desktop.NewUUID(); err == nil {
						b, err = desktop.ExportMobileconfig(name, settings, pu, du)
					}
				}
			case "reg":
				b, err = desktop.ExportReg(settings, machine)
			default:
				return fmt.Errorf("--format %q: want json, mobileconfig or reg", format)
			}
			if err != nil {
				return err
			}
			if out == "" || out == "-" {
				_, err = cmd.OutOrStdout().Write(b)
				return err
			}
			if err := os.WriteFile(out, b, 0o644); err != nil {
				return err
			}
			ui.OK(os.Stderr, "Wrote %s", ui.Val(out))
			return nil
		},
	}
	f.register(c)
	c.Flags().StringVar(&format, "format", "json", "json, mobileconfig or reg")
	c.Flags().StringVarP(&out, "output", "o", "", "output file (default: stdout)")
	c.Flags().BoolVar(&machine, "machine", false, "with --format reg: write machine policy (HKLM) instead of user policy (HKCU)")
	return c
}

func newDesktopStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which configuration Claude Desktop will use at its next launch",
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, f := range desktopFindings() {
				ui.Status(cmd.OutOrStdout(), f.Status, f.Msg)
			}
			return nil
		},
	}
}

// desktopFindings summarizes Claude Desktop's configuration for status/doctor.
func desktopFindings() []finding {
	st, err := desktop.ReadStatus()
	var out []finding
	if st.Managed != "" {
		out = append(out, finding{"•", "managed policy " + st.Managed + " configures Claude Desktop; the local library below is ignored"})
	}
	if err != nil {
		return append(out, finding{"⚠", "Claude Desktop configuration library: " + err.Error()})
	}
	switch {
	case st.AppliedID == "" || st.Applied == nil:
		out = append(out, finding{"•", "Claude Desktop: no third-party configuration applied (signs in to Claude.ai)"})
	case st.AppliedEmpty:
		out = append(out, finding{"•", fmt.Sprintf("Claude Desktop: empty configuration %q applied (signs in to Claude.ai)", st.AppliedName)})
	default:
		who := "written outside ccauth"
		if st.AppliedByUs {
			who = "written by ccauth"
		}
		out = append(out, finding{"•", fmt.Sprintf("Claude Desktop: %q applied (%s): provider %v, gateway %v, credential %v",
			st.AppliedName, who, st.Applied["inferenceProvider"], st.Applied["inferenceGatewayBaseUrl"], st.Applied["inferenceCredentialKind"])})
	}
	if len(st.Entries) > 0 {
		names := append([]string{}, st.Entries...)
		sort.Strings(names)
		out = append(out, finding{"•", "Claude Desktop configurations: " + strings.Join(names, ", ") + " (" + st.Dir + ")"})
	}
	return out
}
