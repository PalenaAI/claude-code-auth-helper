// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect layered configuration and sync remote config",
		Long:  "Configuration is layered (user < embedded < managed < remote). These subcommands show which layers are active, the merged result, and let you pull the latest remote config.",
	}
	cmd.AddCommand(newConfigPathCmd(), newConfigShowCmd(), newConfigSyncCmd())
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Show configuration layers and which are present",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "LAYER\tPRESENT\tPATH")
			for _, l := range config.LayerStatus() {
				mark := "no"
				if l.Present {
					mark = "yes"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", l.Name, mark, l.Path)
			}
			_ = w.Flush()
			if c, err := config.Load(); err == nil {
				if c.ConfigURL != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "\nremote config_url: %s\n", c.ConfigURL)
				}
				if c.AllowUserProfiles != nil && !*c.AllowUserProfiles {
					fmt.Fprintln(cmd.OutOrStdout(), "user profiles: LOCKED by an IT layer")
				}
			}
			return nil
		},
	}
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the merged effective config (secrets redacted) with per-profile source",
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := config.Load()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(c.Profiles) == 0 {
				fmt.Fprintln(out, "No profiles configured. Run `ccauth setup`, or ask IT to provision one.")
				return nil
			}
			w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
			fmt.Fprintln(w, "PROFILE\tSOURCE\tPROVIDER→GATEWAY\tMODE")
			for name, p := range c.Profiles {
				fmt.Fprintf(w, "%s\t%s\t%s→%s\t%s\n", name, c.Sources[name], p.Provider, p.Gateway, p.Mode)
			}
			_ = w.Flush()
			fmt.Fprintln(out, "\n# effective config (secrets redacted):")
			return toml.NewEncoder(out).Encode(redactConfig(c))
		},
	}
}

func newConfigSyncCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Fetch the remote config_url and cache it locally",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			url, n, err := config.SyncRemote(ctx)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Synced %d profile(s) from %s → %s\n", n, url, config.RemoteCachePath())
			return nil
		},
	}
}

func redactConfig(c *config.Config) *config.Config {
	cp := &config.Config{
		DefaultProfile:    c.DefaultProfile,
		ConfigURL:         c.ConfigURL,
		AllowUserProfiles: c.AllowUserProfiles,
		Profiles:          map[string]config.Profile{},
	}
	for name, p := range c.Profiles {
		if p.OAuth.ClientSecret != "" {
			p.OAuth.ClientSecret = "***redacted***"
		}
		if p.Exchange.ClientSecret != "" {
			p.Exchange.ClientSecret = "***redacted***"
		}
		cp.Profiles[name] = p
	}
	return cp
}
