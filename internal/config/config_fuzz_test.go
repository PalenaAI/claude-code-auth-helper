// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package config

import "testing"

// FuzzParseAndResolve feeds arbitrary TOML through parsing and profile
// resolution. Remote config is fetched over the network and managed config is
// pushed by MDM, so malformed input must yield an error, never a panic.
func FuzzParseAndResolve(f *testing.F) {
	for _, s := range []string{
		"",
		`default_profile = "p"`,
		"[profiles.p]\nprovider = \"entra\"\ngateway = \"litellm\"\n[profiles.p.oauth]\ntenant_id = \"t\"\nclient_id = \"c\"\nscopes = [\"api://x/.default\"]\n",
		"[profiles.g]\nprovider = \"google\"\ngateway = \"portkey\"\n[profiles.g.gateway_opts]\nmodel_discovery = true\nheaders = { \"x-portkey-provider\" = \"@a\" }\n",
		"[profiles.b]\nprovider = \"oidc\"\ngateway = \"bifrost\"\nmode = \"exchange\"\n[profiles.b.exchange]\nstyle = \"broker\"\n",
		"[profiles]\np = 1",
		"allow_user_profiles = \"yes\"",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		c, err := parseConfig(b, "fuzz.toml")
		if err != nil {
			return
		}
		if c.Profiles == nil {
			t.Fatal("parseConfig returned nil Profiles map")
		}
		for name := range c.Profiles {
			_, _ = c.Resolve(name)
		}
		_ = c.ProfileName("")
	})
}
