// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package config

import "testing"

func base(p Profile) *Config {
	return &Config{Profiles: map[string]Profile{"x": p}}
}

func TestResolveEntraPresets(t *testing.T) {
	c := base(Profile{
		Provider: ProviderEntra,
		Gateway:  GatewayLiteLLM,
		OAuth:    OAuth{TenantID: "tid", ClientID: "cid", Scopes: []string{"api://app/.default"}},
	})
	p, err := c.Resolve("x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.Mode != ModePassthrough {
		t.Errorf("mode = %q, want passthrough", p.Mode)
	}
	if p.OAuth.Credential != TokenAccess {
		t.Errorf("credential = %q, want access", p.OAuth.Credential)
	}
	if p.OAuth.Flow != FlowAuthCode {
		t.Errorf("flow = %q, want auth_code", p.OAuth.Flow)
	}
	if got, want := p.Issuer(), "https://login.microsoftonline.com/tid/v2.0"; got != want {
		t.Errorf("issuer = %q, want %q", got, want)
	}
	if p.Store != "auto" {
		t.Errorf("store = %q, want auto", p.Store)
	}
}

func TestResolveGooglePresets(t *testing.T) {
	c := base(Profile{
		Provider: ProviderGoogle,
		Gateway:  GatewayPortkey,
		OAuth:    OAuth{ClientID: "cid"},
	})
	p, err := c.Resolve("x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Google access tokens are opaque -> emit the ID token by default.
	if p.OAuth.Credential != TokenID {
		t.Errorf("credential = %q, want id", p.OAuth.Credential)
	}
	if len(p.OAuth.Scopes) == 0 {
		t.Error("expected default Google scopes")
	}
	if got := p.Issuer(); got != "https://accounts.google.com" {
		t.Errorf("issuer = %q", got)
	}
}

func TestResolveBifrostDefaultsToExchange(t *testing.T) {
	// Bifrost has no native JWT validation, so mode should default to exchange,
	// and exchange requires a token_url.
	c := base(Profile{
		Provider: ProviderOIDC,
		Gateway:  GatewayBifrost,
		OAuth:    OAuth{Issuer: "https://issuer.example", ClientID: "cid"},
	})
	if _, err := c.Resolve("x"); err == nil {
		t.Fatal("expected error: exchange without token_url")
	}

	c.Profiles["x"] = Profile{
		Provider: ProviderOIDC,
		Gateway:  GatewayBifrost,
		OAuth:    OAuth{Issuer: "https://issuer.example", ClientID: "cid"},
		Exchange: Exchange{TokenURL: "https://broker.example/token"},
	}
	p, err := c.Resolve("x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.Mode != ModeExchange {
		t.Errorf("mode = %q, want exchange", p.Mode)
	}
	if p.Exchange.Style != "rfc8693" {
		t.Errorf("style = %q, want rfc8693 default", p.Exchange.Style)
	}
}

func TestValidateRejectsMissingRequiredFields(t *testing.T) {
	cases := map[string]Profile{
		"entra-no-tenant": {Provider: ProviderEntra, Gateway: GatewayLiteLLM, OAuth: OAuth{ClientID: "c", Scopes: []string{"s"}}},
		"entra-no-scope":  {Provider: ProviderEntra, Gateway: GatewayLiteLLM, OAuth: OAuth{ClientID: "c", TenantID: "t"}},
		"no-client":       {Provider: ProviderGoogle, Gateway: GatewayLiteLLM, OAuth: OAuth{}},
		"oidc-no-issuer":  {Provider: ProviderOIDC, Gateway: GatewayLiteLLM, OAuth: OAuth{ClientID: "c"}},
	}
	for name, prof := range cases {
		t.Run(name, func(t *testing.T) {
			c := base(prof)
			if _, err := c.Resolve("x"); err == nil {
				t.Errorf("expected validation error for %s", name)
			}
		})
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("CCAUTH_CLIENT_ID", "from-env")
	c := base(Profile{
		Provider: ProviderEntra,
		Gateway:  GatewayLiteLLM,
		OAuth:    OAuth{TenantID: "t", ClientID: "from-file", Scopes: []string{"s"}},
	})
	p, err := c.Resolve("x")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if p.OAuth.ClientID != "from-env" {
		t.Errorf("client_id = %q, want from-env override", p.OAuth.ClientID)
	}
}
