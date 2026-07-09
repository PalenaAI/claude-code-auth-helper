// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package gateway holds per-gateway knowledge: how to reach each gateway's
// Anthropic-compatible endpoint, which routing headers it needs, and what the
// gateway operator must configure to validate the SSO token ccauth emits.
package gateway

import (
	"fmt"
	"sort"
	"strings"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

// Info describes a gateway for docs, setup, and doctor output.
type Info struct {
	Name string
	// NativeJWT reports whether the gateway can validate an external IdP JWT on
	// the inbound data plane (as opposed to only its own issued key).
	NativeJWT bool
	// DefaultMode is the credential mode that fits this gateway out of the box.
	DefaultMode config.Mode
	// BaseURLHint is guidance shown in setup for ANTHROPIC_BASE_URL.
	BaseURLHint string
	// SetupNotes are gateway-side steps the operator must perform.
	SetupNotes []string
}

var registry = map[config.Gateway]Info{
	config.GatewayLiteLLM: {
		Name:        "LiteLLM",
		NativeJWT:   true,
		DefaultMode: config.ModePassthrough,
		BaseURLHint: "http://localhost:4000  (proxy root; Claude Code appends /v1/messages)",
		SetupNotes: []string{
			"Enterprise: set general_settings.enable_jwt_auth: true in the proxy config.",
			"Set JWT_PUBLIC_KEY_URL to your IdP JWKS; ALWAYS set JWT_AUDIENCE and JWT_ISSUER (unset = checks silently disabled).",
			"Map claims via litellm_jwtauth (user_id_jwt_field, team_id_jwt_field, roles_jwt_field).",
			"For opaque tokens (e.g. Google access tokens) use enable_oauth2_auth + OAUTH_TOKEN_INFO_ENDPOINT instead.",
		},
	},
	config.GatewayPortkey: {
		Name:        "Portkey",
		NativeJWT:   true,
		DefaultMode: config.ModePassthrough,
		BaseURLHint: "https://api.portkey.ai  (NO trailing /v1; Claude Code appends it)",
		SetupNotes: []string{
			"Enterprise: Admin Settings > Organisation > Authentication > add your IdP JWKS URL.",
			"JWTs must be RS256; header must carry a matching kid.",
			"Route to Anthropic via the x-portkey-provider header (e.g. @anthropic-prod from the Model Catalog).",
			"Mode B maps a standard IdP token to org/workspace server-side; Mode A expects Portkey claims in the token.",
		},
	},
	config.GatewayBifrost: {
		Name:        "Bifrost",
		NativeJWT:   false,
		DefaultMode: config.ModeExchange,
		BaseURLHint: "http://localhost:8080/anthropic  (path prefix selects the Anthropic API)",
		SetupNotes: []string{
			"Bifrost has NO native inbound JWT validation; its OIDC/SSO is dashboard-login only.",
			"Use exchange mode: run a broker that validates the IdP token and returns a Bifrost virtual key (sk-bf-*).",
			"Alternatively write a Go HTTPTransportPreHook plugin that validates the JWT (see examples/bifrost-plugin).",
			"Set enforce_auth_on_inference: true so the virtual key is actually required.",
		},
	},
	config.GatewayGeneric: {
		Name:        "Generic OIDC gateway",
		NativeJWT:   true,
		DefaultMode: config.ModePassthrough,
		BaseURLHint: "https://your-gateway.example.com  (must expose Anthropic /v1/messages)",
		SetupNotes: []string{
			"Configure the gateway (Envoy/APISIX/Kong/custom) to validate the bearer JWT against your IdP JWKS.",
			"Validate signature + iss + aud + exp, and enforce required scopes/roles.",
			"Exempt the /v1/messages path from WAF XSS body inspection (Claude prompts trip those rules).",
		},
	},
}

// Get returns the Info for a gateway, or a zero Info and false.
func Get(g config.Gateway) (Info, bool) { i, ok := registry[g]; return i, ok }

// All returns every gateway info, sorted by key, for help/listing.
func All() []Info {
	keys := make([]string, 0, len(registry))
	for k := range registry {
		keys = append(keys, string(k))
	}
	sort.Strings(keys)
	out := make([]Info, 0, len(keys))
	for _, k := range keys {
		out = append(out, registry[config.Gateway(k)])
	}
	return out
}

// CustomHeaders renders a profile's static routing headers into the
// ANTHROPIC_CUSTOM_HEADERS value ("Name: Value" pairs joined by \n).
func CustomHeaders(p config.Profile) string {
	if len(p.GatewayOpts.Headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(p.GatewayOpts.Headers))
	for k := range p.GatewayOpts.Headers {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", k, p.GatewayOpts.Headers[k]))
	}
	return strings.Join(parts, "\n")
}
