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
	// DiscoveryNotes apply only when the profile enables model discovery
	// (Claude Code calls GET <base>/v1/models with the same credential).
	DiscoveryNotes []string
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
		DiscoveryNotes: []string{
			"JWT callers may call /v1/models by default (openai_routes/info_routes); if you override team_allowed_routes, keep info_routes or list /v1/models.",
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
		DiscoveryNotes: []string{
			"GET /v1/models needs the completions.write (or models.list) scope on the JWT; it lists the x-portkey-provider's models in OpenAI shape, which Claude Code accepts (it reads data[].id).",
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
		DiscoveryNotes: []string{
			"Bifrost serves GET /anthropic/v1/models (Anthropic format); make sure your exchanged key or auth plugin is also authorized on that route.",
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
		DiscoveryNotes: []string{
			"Serve GET /v1/models (data[].id) at the base URL without redirects, validating the same bearer JWT as /v1/messages.",
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

// discoveryCommonNote applies to every gateway when model discovery is on.
const discoveryCommonNote = "Model discovery keeps only IDs containing \"claude\" or \"anthropic\" and fails silently (3 s timeout) back to the built-in model list."

// Notes returns the gateway-side checklist for a profile: the setup notes plus,
// when the profile enables model discovery, what GET /v1/models needs.
func (i Info) Notes(p config.Profile) []string {
	notes := append([]string{}, i.SetupNotes...)
	if p.GatewayOpts.ModelDiscovery {
		notes = append(notes, i.DiscoveryNotes...)
		notes = append(notes, discoveryCommonNote)
	}
	return notes
}

// Header is one extra gateway header, e.g. from `ccauth claude -H`.
type Header struct{ Name, Value string }

// ParseHeader parses a "Name: value" argument and validates it.
func ParseHeader(s string) (Header, error) {
	name, value, ok := strings.Cut(s, ":")
	if !ok {
		return Header{}, fmt.Errorf("header %q: want \"Name: value\"", s)
	}
	h := Header{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)}
	if err := config.ValidateHeader(h.Name, h.Value); err != nil {
		return Header{}, err
	}
	return h, nil
}

// CustomHeaders renders a profile's static routing headers, plus any extra
// per-session headers, into the ANTHROPIC_CUSTOM_HEADERS value ("Name: Value"
// lines joined by \n). An extra header replaces a profile header with the same
// name (case-insensitively); the output is sorted for stable diffs.
func CustomHeaders(p config.Profile, extra ...Header) string {
	merged := map[string]Header{}
	for k, v := range p.GatewayOpts.Headers {
		merged[strings.ToLower(k)] = Header{k, v}
	}
	for _, h := range extra {
		merged[strings.ToLower(h.Name)] = h
	}
	if len(merged) == 0 {
		return ""
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %s", merged[k].Name, merged[k].Value))
	}
	return strings.Join(parts, "\n")
}
