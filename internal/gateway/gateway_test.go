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
