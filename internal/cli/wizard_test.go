// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"io"

	"charm.land/huh/v2"
	"reflect"
	"testing"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

func baseAnswers() setupAnswers {
	return setupAnswers{
		Name: "work", Provider: "entra", Gateway: "generic", Credential: "access",
		Flow: "auth_code", AutoReauth: true, ExStyle: "rfc8693", Use: "launcher",
		TenantID: "tid", ClientID: "cid", BaseURL: "https://gw.example.com", Mode: "passthrough",
	}
}

func TestSetupAnswersProfile(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*setupAnswers)
		check func(*testing.T, config.Profile)
	}{
		{"entra default scope", func(a *setupAnswers) {}, func(t *testing.T, p config.Profile) {
			if !reflect.DeepEqual(p.OAuth.Scopes, []string{"cid/.default"}) || p.OAuth.TenantID != "tid" {
				t.Errorf("oauth = %+v", p.OAuth)
			}
			if p.HelperInteractive == nil || !*p.HelperInteractive || p.GatewayOpts.TTLms != 3000000 {
				t.Errorf("defaults not applied: %+v", p)
			}
		}},
		{"entra explicit scope", func(a *setupAnswers) { a.Scope = " api://gw/.default " }, func(t *testing.T, p config.Profile) {
			if p.OAuth.Scopes[0] != "api://gw/.default" {
				t.Errorf("scope = %v", p.OAuth.Scopes)
			}
		}},
		{"google", func(a *setupAnswers) { a.Provider, a.ClientSecret = "google", "sec" }, func(t *testing.T, p config.Profile) {
			if p.OAuth.ClientSecret != "sec" || len(p.OAuth.Scopes) != 0 {
				t.Errorf("google oauth = %+v", p.OAuth)
			}
		}},
		{"oidc default scopes", func(a *setupAnswers) { a.Provider, a.Issuer, a.Credential = "oidc", "https://idp", "id" }, func(t *testing.T, p config.Profile) {
			if !reflect.DeepEqual(p.OAuth.Scopes, []string{"openid", "profile", "email"}) || p.OAuth.Credential != config.TokenID {
				t.Errorf("oidc oauth = %+v", p.OAuth)
			}
		}},
		{"fixed port only for auth code", func(a *setupAnswers) { a.Port = "8765" }, func(t *testing.T, p config.Profile) {
			if p.OAuth.RedirectPort != 8765 {
				t.Errorf("port = %d", p.OAuth.RedirectPort)
			}
		}},
		{"device flow ignores port", func(a *setupAnswers) { a.Flow, a.Port = "device_code", "8765" }, func(t *testing.T, p config.Profile) {
			if p.OAuth.RedirectPort != 0 {
				t.Errorf("port = %d", p.OAuth.RedirectPort)
			}
		}},
		{"default base url", func(a *setupAnswers) { a.Gateway, a.BaseURL = "litellm", "" }, func(t *testing.T, p config.Profile) {
			if p.GatewayOpts.BaseURL != "http://localhost:4000" {
				t.Errorf("base url = %q", p.GatewayOpts.BaseURL)
			}
		}},
		{"headers and portkey slug", func(a *setupAnswers) {
			a.Gateway, a.PortkeySlug, a.Headers = "portkey", "@anthropic-prod", "x-team: platform\n\nx-cost-center: 42\n"
		}, func(t *testing.T, p config.Profile) {
			want := map[string]string{"x-team": "platform", "x-cost-center": "42", "x-portkey-provider": "@anthropic-prod"}
			if !reflect.DeepEqual(p.GatewayOpts.Headers, want) {
				t.Errorf("headers = %v", p.GatewayOpts.Headers)
			}
		}},
		{"exchange rfc8693", func(a *setupAnswers) {
			a.Mode, a.ExTokenURL, a.ExAudience, a.ExKeyField = "exchange", "https://sts", "bifrost", "ignored"
		}, func(t *testing.T, p config.Profile) {
			if p.Exchange.Style != "rfc8693" || p.Exchange.Audience != "bifrost" || p.Exchange.KeyField != "" {
				t.Errorf("exchange = %+v", p.Exchange)
			}
		}},
		{"exchange broker", func(a *setupAnswers) {
			a.Mode, a.ExStyle, a.ExTokenURL, a.ExKeyField = "exchange", "broker", "https://broker", "key"
		}, func(t *testing.T, p config.Profile) {
			if p.Exchange.Style != "broker" || p.Exchange.KeyField != "key" || p.Exchange.Audience != "" {
				t.Errorf("exchange = %+v", p.Exchange)
			}
		}},
		{"passthrough has no exchange", func(a *setupAnswers) { a.ExTokenURL = "https://sts" }, func(t *testing.T, p config.Profile) {
			if p.Exchange.TokenURL != "" {
				t.Errorf("exchange = %+v", p.Exchange)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := baseAnswers()
			tc.edit(&a)
			p, err := a.profile()
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, p)
		})
	}
}

func TestSetupAnswersProfileErrors(t *testing.T) {
	a := baseAnswers()
	a.Headers = "Authorization: Bearer x"
	if _, err := a.profile(); err == nil {
		t.Error("reserved header must be refused")
	}
	a = baseAnswers()
	a.Port = "70000"
	if _, err := a.profile(); err == nil {
		t.Error("out-of-range port must be refused")
	}
	a = baseAnswers()
	a.Provider = "saml"
	if _, err := a.profile(); err == nil {
		t.Error("unknown provider must be refused")
	}
}

func TestWizardValidators(t *testing.T) {
	for _, ok := range []string{"work", "corp.prod", "a_b-1"} {
		if err := validateProfileName(ok); err != nil {
			t.Errorf("validateProfileName(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "my work", `we"ird`, "50%"} {
		if validateProfileName(bad) == nil {
			t.Errorf("validateProfileName(%q) should fail", bad)
		}
	}
	for s, ok := range map[string]bool{"": true, "8765": true, "0": false, "65536": false, "abc": false} {
		if (validatePort(s) == nil) != ok {
			t.Errorf("validatePort(%q) ok=%v", s, !ok)
		}
	}
	for s, ok := range map[string]bool{"https://gw.example.com": true, "http://localhost:4000": true, "gw.example.com": false, "ftp://x": false, "": false} {
		if (validateHTTPURL(s) == nil) != ok {
			t.Errorf("validateHTTPURL(%q) ok=%v", s, !ok)
		}
	}
}

// lineReader hands out one line per Read, like a terminal does; huh's
// accessible prompts each wrap the reader in a fresh bufio.Scanner.
type lineReader struct{ lines []string }

func (r *lineReader) Read(p []byte) (int, error) {
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.lines[0]+"\n")
	r.lines = r.lines[1:]
	return n, nil
}

func scriptedRunner(t *testing.T, answers ...string) (formRunner, *lineReader) {
	in := &lineReader{lines: answers}
	return func(groups ...*huh.Group) error {
		return huh.NewForm(groups...).WithAccessible(true).WithInput(in).WithOutput(io.Discard).Run()
	}, in
}

func defaultAnswers() setupAnswers {
	return setupAnswers{Name: "default", Provider: "entra", Gateway: "litellm", Credential: "access",
		Flow: "auth_code", AutoReauth: true, ExStyle: "rfc8693", Use: "launcher"}
}

// TestSetupWizardEntra drives the real wizard in huh's accessible (line-based)
// mode: only the questions for Entra + a generic gateway may be asked, or the
// scripted answers would land in the wrong fields.
func TestSetupWizardEntra(t *testing.T) {
	a := defaultAnswers()
	run, in := scriptedRunner(t,
		"work",                   // profile name
		"1",                      // Microsoft Entra ID
		"4",                      // other OIDC-aware gateway
		"tid",                    // tenant
		"cid",                    // client ID
		"",                       // scope: Enter = cid/.default
		"1",                      // browser flow
		"y",                      // auto re-auth
		"",                       // callback port: random
		"https://gw.example.com", // base URL
		"1",                      // passthrough
		"x-team: platform",       // headers
		"",                       // haiku model
		"n",                      // model discovery
		"1",                      // launcher
	)
	if err := runSetupWizard(&a, true, run); err != nil {
		t.Fatal(err)
	}
	if len(in.lines) != 0 {
		t.Errorf("unused answers %v: the wizard asked fewer questions than expected", in.lines)
	}
	p, err := a.profile()
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "work" || p.Provider != config.ProviderEntra || p.Gateway != config.GatewayGeneric ||
		p.OAuth.Scopes[0] != "cid/.default" || p.GatewayOpts.BaseURL != "https://gw.example.com" ||
		p.Mode != config.ModePassthrough || p.GatewayOpts.Headers["x-team"] != "platform" || a.Use != "launcher" {
		t.Errorf("answers = %+v\nprofile = %+v", a, p)
	}
}

// Bifrost defaults to exchange mode; with --write the "where" question is skipped.
func TestSetupWizardBifrostExchange(t *testing.T) {
	a := defaultAnswers()
	run, in := scriptedRunner(t,
		"",               // name: keep "default"
		"1",              // Entra
		"3",              // Bifrost
		"tid", "cid", "", // tenant, client, default scope
		"2",                   // device code
		"n",                   // no auto re-auth (no port question for device code)
		"",                    // base URL: Enter = Bifrost default
		"1",                   // first option = exchange for Bifrost
		"2",                   // broker style
		"https://broker.corp", // broker URL
		"",                    // key field: Enter
		"", "", "n",           // optional step
	)
	if err := runSetupWizard(&a, false, run); err != nil {
		t.Fatal(err)
	}
	if len(in.lines) != 0 {
		t.Errorf("unused answers %v", in.lines)
	}
	p, err := a.profile()
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "default" || p.Mode != config.ModeExchange || p.Exchange.Style != "broker" ||
		p.Exchange.TokenURL != "https://broker.corp" || p.OAuth.Flow != config.FlowDeviceCode ||
		p.GatewayOpts.BaseURL != "http://localhost:8080/anthropic" || *p.HelperInteractive {
		t.Errorf("profile = %+v", p)
	}
}
