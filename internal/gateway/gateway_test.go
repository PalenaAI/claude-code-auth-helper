// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package gateway

import (
	"testing"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

func TestCustomHeadersSortedAndFormatted(t *testing.T) {
	p := config.Profile{GatewayOpts: config.GatewayOpts{Headers: map[string]string{
		"x-portkey-provider": "@anthropic-prod",
		"x-org-route":        "prod",
	}}}
	got := CustomHeaders(p)
	want := "x-org-route: prod\nx-portkey-provider: @anthropic-prod"
	if got != want {
		t.Errorf("CustomHeaders() = %q, want %q", got, want)
	}
}

func TestCustomHeadersEmpty(t *testing.T) {
	if got := CustomHeaders(config.Profile{}); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestRegistryCoversAllGateways(t *testing.T) {
	for _, g := range []config.Gateway{config.GatewayLiteLLM, config.GatewayPortkey, config.GatewayBifrost, config.GatewayGeneric} {
		if _, ok := Get(g); !ok {
			t.Errorf("gateway %q missing from registry", g)
		}
	}
	// Bifrost is the one without native inbound JWT validation.
	if bf, _ := Get(config.GatewayBifrost); bf.NativeJWT {
		t.Error("Bifrost should be marked NativeJWT=false")
	}
	if ll, _ := Get(config.GatewayLiteLLM); !ll.NativeJWT {
		t.Error("LiteLLM should be marked NativeJWT=true")
	}
}

func TestNotesAddDiscoveryOnlyWhenEnabled(t *testing.T) {
	for _, gi := range All() {
		off := gi.Notes(config.Profile{})
		on := gi.Notes(config.Profile{GatewayOpts: config.GatewayOpts{ModelDiscovery: true}})
		if len(off) != len(gi.SetupNotes) {
			t.Errorf("%s: discovery notes leaked with discovery off", gi.Name)
		}
		if len(gi.DiscoveryNotes) == 0 {
			t.Errorf("%s: missing DiscoveryNotes", gi.Name)
		}
		if len(on) != len(gi.SetupNotes)+len(gi.DiscoveryNotes)+1 {
			t.Errorf("%s: want setup + discovery + common notes, got %d", gi.Name, len(on))
		}
	}
}

func TestParseHeader(t *testing.T) {
	h, err := ParseHeader("  X-Team :  platform eng ")
	if err != nil || h != (Header{"X-Team", "platform eng"}) {
		t.Fatalf("ParseHeader = %+v, %v", h, err)
	}
	for _, bad := range []string{"no-colon", ": value", "Authorization: Bearer x", "x-api-key: k", "bad name: v", "x-a: line\nInjected: 1"} {
		if _, err := ParseHeader(bad); err == nil {
			t.Errorf("ParseHeader(%q) should fail", bad)
		}
	}
}

func TestCustomHeadersExtraOverridesCaseInsensitively(t *testing.T) {
	p := config.Profile{GatewayOpts: config.GatewayOpts{Headers: map[string]string{"X-Team": "a", "x-route": "r"}}}
	got := CustomHeaders(p, Header{"x-team", "b"}, Header{"x-extra", "e"})
	want := "x-extra: e\nx-route: r\nx-team: b"
	if got != want {
		t.Errorf("CustomHeaders = %q, want %q", got, want)
	}
}
