// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package config defines the on-disk configuration model for ccauth and the
// preset logic that lets a user supply the minimum amount of information for a
// given identity provider / gateway combination.
//
// Configuration is layered so an IT team can provision everything centrally and
// end users never touch tenant/client IDs. Layers, lowest to highest authority:
//
//	user      ~/.config/ccauth/config.toml         (personal profiles / sessions)
//	embedded  compiled into the binary             (branded self-contained build)
//	managed   /etc/ccauth/config.toml (or MDM)     (IT-owned, overrides user)
//	remote    cached fetch of config_url           (central server, freshest)
//
// Higher layers override lower ones on same-named profiles, and an IT layer can
// set allow_user_profiles = false to drop user profiles entirely (lockdown).
package config

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

//go:embed defaults.toml
var embeddedConfigData string

// Provider is the identity provider that issues the SSO token.
type Provider string

const (
	ProviderEntra  Provider = "entra"
	ProviderGoogle Provider = "google"
	ProviderOIDC   Provider = "oidc"
)

// Gateway is the AI gateway Claude Code will talk to.
type Gateway string

const (
	GatewayLiteLLM Gateway = "litellm"
	GatewayPortkey Gateway = "portkey"
	GatewayBifrost Gateway = "bifrost"
	GatewayGeneric Gateway = "generic"
)

// Mode selects how the SSO token becomes the credential the gateway accepts.
type Mode string

const (
	ModePassthrough Mode = "passthrough"
	ModeExchange    Mode = "exchange"
)

// Flow selects the interactive OAuth flow used by `ccauth login`.
type Flow string

const (
	FlowAuthCode   Flow = "auth_code"
	FlowDeviceCode Flow = "device_code"
)

// TokenKind selects which token is emitted in passthrough mode.
type TokenKind string

const (
	TokenAccess TokenKind = "access"
	TokenID     TokenKind = "id"
)

// Config is a single config layer (or the merged effective config).
type Config struct {
	DefaultProfile    string             `toml:"default_profile"`
	ConfigURL         string             `toml:"config_url"`          // remote config to fetch (IT layers)
	AllowUserProfiles *bool              `toml:"allow_user_profiles"` // IT lockdown; nil = allowed
	Profiles          map[string]Profile `toml:"profiles"`
	Sources           map[string]string  `toml:"-"` // profile name -> layer it came from
}

// Profile is one named gateway+SSO configuration.
type Profile struct {
	Provider          Provider    `toml:"provider"`
	Gateway           Gateway     `toml:"gateway"`
	Mode              Mode        `toml:"mode"`
	Store             string      `toml:"store"`              // auto | keyring | file
	HelperInteractive *bool       `toml:"helper_interactive"` // let `ccauth token` open a browser to re-auth; nil = true
	OAuth             OAuth       `toml:"oauth"`
	GatewayOpts       GatewayOpts `toml:"gateway_opts"`
	Exchange          Exchange    `toml:"exchange"`
}

// OAuth holds the identity-provider settings.
type OAuth struct {
	TenantID     string    `toml:"tenant_id"`
	Issuer       string    `toml:"issuer"`
	ClientID     string    `toml:"client_id"`
	ClientSecret string    `toml:"client_secret"`
	Scopes       []string  `toml:"scopes"`
	Flow         Flow      `toml:"flow"`
	RedirectHost string    `toml:"redirect_host"`
	RedirectPort int       `toml:"redirect_port"`
	Credential   TokenKind `toml:"credential_token"`
}

// GatewayOpts holds the Claude Code wiring for the target gateway.
type GatewayOpts struct {
	BaseURL string            `toml:"base_url"`
	Headers map[string]string `toml:"headers"`
	TTLms   int               `toml:"ttl_ms"`
}

// Exchange configures token-exchange (mode = exchange).
type Exchange struct {
	Style            string    `toml:"style"`
	TokenURL         string    `toml:"token_url"`
	Audience         string    `toml:"audience"`
	Resource         string    `toml:"resource"`
	ClientID         string    `toml:"client_id"`
	ClientSecret     string    `toml:"client_secret"`
	SubjectTokenType TokenKind `toml:"subject_token_type"`
	KeyField         string    `toml:"key_field"`
	ExpiryField      string    `toml:"expiry_field"`
}

// InteractiveHelper reports whether `ccauth token` may launch a browser to
// re-authenticate when the refresh token is gone. Defaults to true.
func (p Profile) InteractiveHelper() bool {
	return p.HelperInteractive == nil || *p.HelperInteractive
}

// ----- paths -------------------------------------------------------------

// Dir returns the directory holding the user config file.
func Dir() string {
	if d := os.Getenv("CCAUTH_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "ccauth")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ccauth")
}

// Path returns the user config file path.
func Path() string { return filepath.Join(Dir(), "config.toml") }

// ManagedPath returns the IT-owned system config path.
func ManagedPath() string {
	if p := os.Getenv("CCAUTH_MANAGED_CONFIG"); p != "" {
		return p
	}
	if runtime.GOOS == "windows" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		return filepath.Join(pd, "ccauth", "config.toml")
	}
	return "/etc/ccauth/config.toml"
}

// StateDir returns the directory used by the file store and remote cache.
func StateDir() string {
	if d := os.Getenv("CCAUTH_STATE_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "ccauth")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "ccauth")
}

// RemoteCachePath is where a fetched remote config is cached on disk.
func RemoteCachePath() string { return filepath.Join(StateDir(), "remote-config.toml") }

// ----- layered load ------------------------------------------------------

type layer struct {
	name string
	path string
	cfg  *Config
}

func parseConfig(b []byte, path string) (*Config, error) {
	c := &Config{Profiles: map[string]Profile{}}
	if err := toml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]Profile{}
	}
	return c, nil
}

func (c *Config) isEmpty() bool {
	return len(c.Profiles) == 0 && c.DefaultProfile == "" && c.ConfigURL == "" && c.AllowUserProfiles == nil
}

func readFileLayer(name, path string) (*layer, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cfg, err := parseConfig(b, path)
	if err != nil {
		return nil, err
	}
	return &layer{name: name, path: path, cfg: cfg}, nil
}

// Load reads and merges all configuration layers into the effective config.
func Load() (*Config, error) {
	var layers []*layer

	if cfg, err := parseConfig([]byte(embeddedConfigData), "embedded"); err == nil && !cfg.isEmpty() {
		layers = append(layers, &layer{name: "embedded", path: "embedded", cfg: cfg})
	}
	for _, spec := range []struct{ name, path string }{
		{"managed", ManagedPath()},
		{"remote", RemoteCachePath()},
		{"user", Path()},
	} {
		l, err := readFileLayer(spec.name, spec.path)
		if err != nil {
			return nil, err
		}
		if l != nil {
			layers = append(layers, l)
		}
	}
	return mergeLayers(layers), nil
}

// mergeLayers combines layers with precedence user < embedded < managed < remote.
func mergeLayers(layers []*layer) *Config {
	byName := map[string]*layer{}
	for _, l := range layers {
		byName[l.name] = l
	}

	// Lockdown: an IT layer can forbid user profiles.
	allowUser := true
	for _, n := range []string{"embedded", "managed", "remote"} {
		if l := byName[n]; l != nil && l.cfg.AllowUserProfiles != nil {
			allowUser = *l.cfg.AllowUserProfiles
		}
	}

	merged := &Config{Profiles: map[string]Profile{}, Sources: map[string]string{}}
	for _, n := range []string{"user", "embedded", "managed", "remote"} {
		l := byName[n]
		if l == nil || (n == "user" && !allowUser) {
			continue
		}
		for name, prof := range l.cfg.Profiles {
			merged.Profiles[name] = prof
			merged.Sources[name] = n
		}
		if l.cfg.DefaultProfile != "" {
			merged.DefaultProfile = l.cfg.DefaultProfile
		}
		if l.cfg.ConfigURL != "" {
			merged.ConfigURL = l.cfg.ConfigURL
		}
		if l.cfg.AllowUserProfiles != nil {
			merged.AllowUserProfiles = l.cfg.AllowUserProfiles
		}
	}
	if v := os.Getenv("CCAUTH_CONFIG_URL"); v != "" {
		merged.ConfigURL = v
	}
	return merged
}

// LayerInfo describes one configuration layer and whether it is present.
type LayerInfo struct {
	Name    string
	Path    string
	Present bool
}

// LayerStatus reports which layers exist, for `ccauth config path`.
func LayerStatus() []LayerInfo {
	embPresent := false
	if cfg, err := parseConfig([]byte(embeddedConfigData), "embedded"); err == nil && !cfg.isEmpty() {
		embPresent = true
	}
	out := []LayerInfo{{"embedded", "(compiled-in)", embPresent}}
	for _, spec := range []struct{ name, path string }{
		{"managed", ManagedPath()},
		{"remote", RemoteCachePath()},
		{"user", Path()},
	} {
		_, err := os.Stat(spec.path)
		out = append(out, LayerInfo{spec.name, spec.path, err == nil})
	}
	return out
}

// SyncRemote fetches config_url and writes it to the remote cache. It returns the
// URL used and the number of profiles fetched.
func SyncRemote(ctx context.Context) (string, int, error) {
	url := os.Getenv("CCAUTH_CONFIG_URL")
	if url == "" {
		c, err := Load()
		if err == nil {
			url = c.ConfigURL
		}
	}
	if url == "" {
		return "", 0, fmt.Errorf("no config_url set (in a managed/embedded layer or CCAUTH_CONFIG_URL)")
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return url, 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return url, 0, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return url, 0, fmt.Errorf("fetch %s: HTTP %s", url, resp.Status)
	}
	cfg, err := parseConfig(body, url)
	if err != nil {
		return url, 0, fmt.Errorf("remote config invalid: %w", err)
	}
	if err := os.MkdirAll(StateDir(), 0o700); err != nil {
		return url, 0, err
	}
	if err := os.WriteFile(RemoteCachePath(), body, 0o600); err != nil {
		return url, 0, err
	}
	return url, len(cfg.Profiles), nil
}

// ----- save (user layer only) --------------------------------------------

// Save writes the user config file with 0600 perms. It never writes IT layers.
func Save(c *Config) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(Path(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}

// LoadUserConfig loads only the user layer (for edits by `setup`).
func LoadUserConfig() (*Config, error) {
	l, err := readFileLayer("user", Path())
	if err != nil {
		return nil, err
	}
	if l == nil {
		return &Config{Profiles: map[string]Profile{}}, nil
	}
	return l.cfg, nil
}

// ----- resolution --------------------------------------------------------

func (c *Config) ProfileName(explicit string) string {
	if explicit != "" {
		return explicit
	}
	if c.DefaultProfile != "" {
		return c.DefaultProfile
	}
	return "default"
}

// Resolve returns a validated, preset-applied copy of the named profile.
func (c *Config) Resolve(name string) (Profile, error) {
	p, ok := c.Profiles[name]
	if !ok {
		return Profile{}, fmt.Errorf("profile %q not found (run `ccauth setup`, or ask IT to provision it)", name)
	}
	applyEnvOverrides(&p)
	if err := p.applyPresets(); err != nil {
		return Profile{}, err
	}
	if err := p.validate(); err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", name, err)
	}
	return p, nil
}

func (p *Profile) applyPresets() error {
	if p.Store == "" {
		p.Store = "auto"
	}
	if p.OAuth.Flow == "" {
		p.OAuth.Flow = FlowAuthCode
	}
	if p.OAuth.RedirectHost == "" {
		p.OAuth.RedirectHost = "localhost"
	}
	if p.HelperInteractive == nil {
		b := true
		p.HelperInteractive = &b
	}

	switch p.Provider {
	case ProviderEntra:
		if p.OAuth.Credential == "" {
			p.OAuth.Credential = TokenAccess
		}
	case ProviderGoogle:
		if len(p.OAuth.Scopes) == 0 {
			p.OAuth.Scopes = []string{"openid", "email", "profile"}
		}
		if p.OAuth.Credential == "" {
			p.OAuth.Credential = TokenID
		}
	case ProviderOIDC:
		if p.OAuth.Credential == "" {
			p.OAuth.Credential = TokenAccess
		}
	default:
		return fmt.Errorf("unknown provider %q (want entra|google|oidc)", p.Provider)
	}

	if p.Mode == "" {
		if p.Gateway == GatewayBifrost {
			p.Mode = ModeExchange
		} else {
			p.Mode = ModePassthrough
		}
	}
	if p.Exchange.Style == "" {
		p.Exchange.Style = "rfc8693"
	}
	if p.Exchange.SubjectTokenType == "" {
		p.Exchange.SubjectTokenType = p.OAuth.Credential
	}
	return nil
}

func (p *Profile) validate() error {
	if p.OAuth.ClientID == "" {
		return fmt.Errorf("oauth.client_id is required")
	}
	switch p.Provider {
	case ProviderEntra:
		if p.OAuth.TenantID == "" {
			return fmt.Errorf("oauth.tenant_id is required for Entra")
		}
		if len(p.OAuth.Scopes) == 0 {
			return fmt.Errorf("oauth.scopes is required for Entra (e.g. \"api://<gateway-app-id>/.default\")")
		}
	case ProviderOIDC:
		if p.OAuth.Issuer == "" {
			return fmt.Errorf("oauth.issuer is required for generic OIDC")
		}
	}
	switch p.Gateway {
	case GatewayLiteLLM, GatewayPortkey, GatewayBifrost, GatewayGeneric:
	default:
		return fmt.Errorf("unknown gateway %q", p.Gateway)
	}
	if p.Mode == ModeExchange && p.Exchange.TokenURL == "" {
		return fmt.Errorf("exchange.token_url is required when mode = exchange")
	}
	return nil
}

// Issuer returns the OIDC issuer URL for the profile's provider.
func (p Profile) Issuer() string {
	switch p.Provider {
	case ProviderEntra:
		return "https://login.microsoftonline.com/" + p.OAuth.TenantID + "/v2.0"
	case ProviderGoogle:
		return "https://accounts.google.com"
	default:
		return strings.TrimRight(p.OAuth.Issuer, "/")
	}
}

func applyEnvOverrides(p *Profile) {
	if v := os.Getenv("CCAUTH_TENANT_ID"); v != "" {
		p.OAuth.TenantID = v
	}
	if v := os.Getenv("CCAUTH_CLIENT_ID"); v != "" {
		p.OAuth.ClientID = v
	}
	if v := os.Getenv("CCAUTH_CLIENT_SECRET"); v != "" {
		p.OAuth.ClientSecret = v
	}
	if v := os.Getenv("CCAUTH_ISSUER"); v != "" {
		p.OAuth.Issuer = v
	}
	if v := os.Getenv("CCAUTH_BASE_URL"); v != "" {
		p.GatewayOpts.BaseURL = v
	}
}
