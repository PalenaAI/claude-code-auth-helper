// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package desktop configures Claude Desktop's third-party inference mode
// ("Claude Desktop on 3P") to use a ccauth profile's gateway. Claude Desktop
// does not run apiKeyHelper or read Claude Code's settings for credentials; it
// has its own configuration keys (inferenceProvider, inferenceGatewayBaseUrl,
// …), read from MDM policy or the per-user configLibrary directory.
// See https://claude.com/docs/third-party/claude-desktop/configuration.
package desktop

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
)

// Auth is how Claude Desktop obtains the gateway credential.
type Auth string

const (
	// AuthSSO: Desktop signs in to the IdP itself (inferenceCredentialKind
	// "external-idp", Claude Desktop 2.7032.0+).
	AuthSSO Auth = "sso"
	// AuthHelper: Desktop runs `ccauth token` (inferenceCredentialKind
	// "helper-script"), like Claude Code's apiKeyHelper.
	AuthHelper Auth = "helper"
)

// DefaultHelperTTL is how long Desktop caches the helper's credential. It stays
// well inside Entra's 60–90 minute access-token lifetime; ccauth guarantees the
// token outlives it (token --min-lifetime).
const DefaultHelperTTL = 30 * time.Minute

// helperTimeout lets an interactive re-login (bounded at 3 minutes in ccauth)
// finish before Desktop gives up on the helper. Desktop's default is 60s.
const helperTimeout = 240 * time.Second

// ResolveAuth turns "auto"/"sso"/"helper" into an Auth for the profile. Desktop's
// built-in sign-in is a public client with PKCE, so it can't do Google (a
// Desktop-app client needs its secret) or exchange mode (Desktop would send the
// IdP token, not the exchanged gateway key); those need the helper.
func ResolveAuth(requested string, prof config.Profile) (Auth, error) {
	ssoOK := prof.Mode != config.ModeExchange && prof.Provider != config.ProviderGoogle && prof.OAuth.ClientSecret == ""
	switch requested {
	case "", "auto":
		if ssoOK {
			return AuthSSO, nil
		}
		return AuthHelper, nil
	case string(AuthSSO):
		if !ssoOK {
			return "", fmt.Errorf("--auth sso needs a passthrough profile with a public client (not Google, not exchange mode, no client secret); use --auth helper")
		}
		return AuthSSO, nil
	case string(AuthHelper):
		return AuthHelper, nil
	default:
		return "", fmt.Errorf("--auth %q: want auto, sso or helper", requested)
	}
}

// Options tunes the generated configuration.
type Options struct {
	Auth       Auth
	Broker     bool             // sign in through the OS Entra broker (Company Portal / WAM); Entra + sso only
	HelperPath string           // absolute path Desktop runs for --auth helper
	HelperTTL  time.Duration    // Desktop's cache window for the helper's output
	Extra      []gateway.Header // headers on top of the profile's
}

// Settings builds the Claude Desktop on 3P configuration for a profile, as a
// flat map of documented keys with native JSON values.
func Settings(name string, prof config.Profile, opt Options) (map[string]any, error) {
	if prof.GatewayOpts.BaseURL == "" {
		return nil, fmt.Errorf("profile %q has no gateway_opts.base_url", name)
	}
	s := map[string]any{
		"inferenceProvider":       "gateway",
		"inferenceGatewayBaseUrl": prof.GatewayOpts.BaseURL,
	}
	if h := headerMap(prof, opt.Extra); len(h) > 0 {
		s["inferenceCustomHeaders"] = h
	}
	// Without model discovery, tell Desktop which models the gateway serves.
	if !prof.GatewayOpts.ModelDiscovery {
		var models []string
		for _, m := range []string{prof.GatewayOpts.Model, prof.GatewayOpts.HaikuModel} {
			if m != "" && !contains(models, m) {
				models = append(models, m)
			}
		}
		if len(models) > 0 {
			s["inferenceModels"] = models
		}
	}

	switch opt.Auth {
	case AuthSSO:
		if opt.Broker && prof.Provider != config.ProviderEntra {
			return nil, fmt.Errorf("--broker signs in through the Microsoft Entra broker; profile %q is %s", name, prof.Provider)
		}
		s["inferenceCredentialKind"] = "external-idp"
		s["inferenceIdpOidc"] = idpOIDC(prof)
		if opt.Broker {
			s["inferenceIdpAuthFlow"] = "broker"
		}
	case AuthHelper:
		if !filepath.IsAbs(opt.HelperPath) {
			return nil, fmt.Errorf("the credential helper path must be absolute, got %q", opt.HelperPath)
		}
		ttl := opt.HelperTTL
		if ttl <= 0 {
			ttl = DefaultHelperTTL
		}
		s["inferenceGatewayAuthScheme"] = "bearer"
		s["inferenceCredentialKind"] = "helper-script"
		s["inferenceCredentialHelper"] = opt.HelperPath
		// Desktop refuses arguments containing `"`, `%` or control characters.
		args := []string{"token", "--profile", name, "--min-lifetime", ttl.String()}
		for _, a := range args {
			if strings.ContainsAny(a, "\"%") || strings.IndexFunc(a, func(r rune) bool { return r < 0x20 }) >= 0 {
				return nil, fmt.Errorf("profile name %q can't be passed to Claude Desktop's helper (no quotes, %% or control characters)", name)
			}
		}
		s["inferenceCredentialHelperArgs"] = args
		s["inferenceCredentialHelperTtlSec"] = int(ttl / time.Second)
		s["inferenceCredentialHelperTimeoutSec"] = int(helperTimeout / time.Second)
	default:
		return nil, fmt.Errorf("unknown auth %q", opt.Auth)
	}
	return s, nil
}

// idpOIDC maps the profile's IdP onto Desktop's inferenceIdpOidc object.
func idpOIDC(prof config.Profile) map[string]any {
	o := map[string]any{
		"clientId": prof.OAuth.ClientID,
		"issuer":   prof.Issuer(),
	}
	// Desktop appends offline_access itself in access-token mode; openid and
	// offline_access aren't the gateway's API scope.
	var api []string
	for _, sc := range prof.OAuth.Scopes {
		if sc != "openid" && sc != "offline_access" {
			api = append(api, sc)
		}
	}
	if prof.OAuth.Credential == config.TokenID {
		o["bearerTokenType"] = "id_token"
		if len(prof.OAuth.Scopes) > 0 {
			// With explicit scopes Desktop doesn't add offline_access in
			// id-token mode, and silent refresh needs it.
			sc := append([]string{}, prof.OAuth.Scopes...)
			for _, need := range []string{"openid", "offline_access"} {
				if !contains(sc, need) {
					sc = append(sc, need)
				}
			}
			o["scopes"] = strings.Join(sc, " ")
		}
	} else {
		o["bearerTokenType"] = "access_token"
		o["scopes"] = strings.Join(api, " ")
	}
	if prof.OAuth.RedirectHost == "localhost" || prof.OAuth.RedirectHost == "" {
		// ccauth's registered loopback redirect is http://localhost/callback.
		o["redirectHost"] = "localhost"
	}
	if prof.OAuth.RedirectPort > 0 {
		o["redirectPort"] = prof.OAuth.RedirectPort
	}
	return o
}

func headerMap(prof config.Profile, extra []gateway.Header) map[string]string {
	raw := gateway.CustomHeaders(prof, extra...)
	if raw == "" {
		return nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		k, v, _ := strings.Cut(line, ": ")
		out[k] = v
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// StableHelperPath returns an absolute path to this ccauth binary that survives
// upgrades: a Homebrew Cellar path (…/Cellar/ccauth/<version>/bin/ccauth)
// changes on every upgrade, so it is mapped to the prefix's bin/ccauth link.
func StableHelperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate ccauth: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if i := strings.Index(exe, string(filepath.Separator)+"Cellar"+string(filepath.Separator)); i >= 0 {
		link := filepath.Join(exe[:i], "bin", filepath.Base(exe))
		if fileExists(link) {
			return link, nil
		}
	}
	// Prefer the PATH entry the user runs when it is the same binary.
	if p, err := exec.LookPath(filepath.Base(exe)); err == nil {
		if abs, err := filepath.Abs(p); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil && real == exe {
				return abs, nil
			}
		}
	}
	return exe, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
