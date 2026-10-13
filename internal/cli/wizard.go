// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"charm.land/huh/v2"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
	"github.com/PalenaAI/claude-code-auth-helper/internal/ui"
)

// setupAnswers is everything the setup form collects. profile() turns it into a
// config.Profile, so the mapping is testable without a terminal.
type setupAnswers struct {
	Name, Provider, Gateway                     string
	TenantID, ClientID, ClientSecret, Scope     string
	Issuer, Scopes, Credential                  string
	Flow, Port                                  string
	AutoReauth                                  bool
	BaseURL, Mode                               string
	ExStyle, ExTokenURL, ExAudience, ExKeyField string
	PortkeySlug, Headers, HaikuModel            string
	ModelDiscovery                              bool
	Use                                         string
}

// profileNamePattern keeps names usable everywhere ccauth passes them on:
// apiKeyHelper commands, file names, Claude Desktop helper arguments.
var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

func validateProfileName(s string) error {
	if !profileNamePattern.MatchString(s) {
		return errors.New("use letters, digits, '.', '_' or '-'")
	}
	return nil
}

func required(what string) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s is required", what)
		}
		return nil
	}
}

func validateHTTPURL(s string) error {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("enter a full URL, e.g. https://gateway.example.com")
	}
	return nil
}

func validatePort(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	p, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || p < 1 || p > 65535 {
		return errors.New("a number between 1 and 65535, or blank for a random port")
	}
	return nil
}

func parseHeaderLines(text string) (map[string]string, error) {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		h, err := gateway.ParseHeader(line)
		if err != nil {
			return nil, err
		}
		out[h.Name] = h.Value
	}
	return out, nil
}

// entraScope is the scope to use: the typed one, or the single-app default.
func (a setupAnswers) entraScope() string {
	if s := strings.TrimSpace(a.Scope); s != "" {
		return s
	}
	return strings.TrimSpace(a.ClientID) + "/.default"
}

// baseURL is the typed gateway URL, or the gateway's default.
func (a setupAnswers) baseURL() string {
	if s := strings.TrimSpace(a.BaseURL); s != "" {
		return s
	}
	return defaultBaseURL(config.Gateway(a.Gateway))
}

// profile builds the profile the answers describe.
func (a setupAnswers) profile() (config.Profile, error) {
	p := config.Profile{
		Provider: config.Provider(a.Provider),
		Gateway:  config.Gateway(a.Gateway),
		Mode:     config.Mode(a.Mode),
	}
	switch p.Provider {
	case config.ProviderEntra:
		p.OAuth.TenantID = strings.TrimSpace(a.TenantID)
		p.OAuth.ClientID = strings.TrimSpace(a.ClientID)
		p.OAuth.Scopes = []string{a.entraScope()}
	case config.ProviderGoogle:
		p.OAuth.ClientID = strings.TrimSpace(a.ClientID)
		p.OAuth.ClientSecret = strings.TrimSpace(a.ClientSecret)
	case config.ProviderOIDC:
		p.OAuth.Issuer = strings.TrimSpace(a.Issuer)
		p.OAuth.ClientID = strings.TrimSpace(a.ClientID)
		p.OAuth.ClientSecret = strings.TrimSpace(a.ClientSecret)
		scopes := strings.Fields(a.Scopes)
		if len(scopes) == 0 {
			scopes = []string{"openid", "profile", "email"}
		}
		p.OAuth.Scopes = scopes
		p.OAuth.Credential = config.TokenKind(a.Credential)
	default:
		return p, fmt.Errorf("unknown identity provider %q", a.Provider)
	}

	p.OAuth.Flow = config.Flow(a.Flow)
	if p.OAuth.Flow == config.FlowAuthCode && strings.TrimSpace(a.Port) != "" {
		if err := validatePort(a.Port); err != nil {
			return p, err
		}
		p.OAuth.RedirectPort, _ = strconv.Atoi(strings.TrimSpace(a.Port))
	}
	reauth := a.AutoReauth
	p.HelperInteractive = &reauth

	p.GatewayOpts.BaseURL = a.baseURL()
	p.GatewayOpts.TTLms = 3000000 // 50 min; below typical token lifetime, refreshed on demand
	if p.Mode == config.ModeExchange {
		p.Exchange.Style = a.ExStyle
		p.Exchange.TokenURL = strings.TrimSpace(a.ExTokenURL)
		if a.ExStyle == "rfc8693" {
			p.Exchange.Audience = strings.TrimSpace(a.ExAudience)
		} else {
			p.Exchange.KeyField = strings.TrimSpace(a.ExKeyField)
		}
	}
	headers, err := parseHeaderLines(a.Headers)
	if err != nil {
		return p, err
	}
	if p.Gateway == config.GatewayPortkey {
		headers["x-portkey-provider"] = firstNonEmpty(strings.TrimSpace(a.PortkeySlug), "@anthropic-prod")
	}
	if len(headers) > 0 {
		p.GatewayOpts.Headers = headers
	}
	p.GatewayOpts.HaikuModel = strings.TrimSpace(a.HaikuModel)
	p.GatewayOpts.ModelDiscovery = a.ModelDiscovery
	return p, nil
}

// formRunner runs one form made of the given groups (themed, accessible mode,
// output); tests swap in a scripted runner.
type formRunner func(groups ...*huh.Group) error

// runSetupWizard asks the setup questions as a sequence of small forms, each
// built once the earlier answers are known. Fields that don't apply (another
// provider's, the exchange settings in passthrough mode, …) are never added,
// rather than hidden: huh's accessible mode ignores hidden groups and computed
// options, so this keeps the screen-reader flow identical to the interactive one.
func runSetupWizard(a *setupAnswers, askUse bool, run formRunner) error {
	// Fields with a default accept an empty answer: huh's accessible mode
	// validates the raw input before substituting the default.
	defName := a.Name
	if err := run(huh.NewGroup(
		huh.NewNote().
			Title("ccauth setup").
			Description("Connect Claude Code to your AI gateway with your company sign-in.\nStep 1 of 6 · profile and services"),
		huh.NewInput().Title("Profile name").
			Description("Use it later as `-p <name>`.").
			Value(&a.Name).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return nil // keep the default
				}
				return validateProfileName(strings.TrimSpace(s))
			}),
		huh.NewSelect[string]().Title("Identity provider").
			Options(
				huh.NewOption("Microsoft Entra ID", "entra"),
				huh.NewOption("Google Workspace", "google"),
				huh.NewOption("Generic OIDC (Okta, Auth0, Keycloak, …)", "oidc"),
			).Value(&a.Provider),
		huh.NewSelect[string]().Title("Gateway").
			Options(
				huh.NewOption("LiteLLM", "litellm"),
				huh.NewOption("Portkey", "portkey"),
				huh.NewOption("Bifrost", "bifrost"),
				huh.NewOption("Other OIDC-aware gateway (e.g. Wäg, Envoy, Kong)", "generic"),
			).Value(&a.Gateway),
	)); err != nil {
		return err
	}
	a.Name = firstNonEmpty(strings.TrimSpace(a.Name), defName)

	switch a.Provider {
	case "entra":
		if err := run(huh.NewGroup(
			huh.NewNote().Title("Step 2 of 6 · Microsoft Entra ID").
				Description("From the app registration ccauth signs in with (its Overview page)."),
			huh.NewInput().Title("Directory (tenant) ID").Description("GUID or verified domain, not \"common\".").
				Value(&a.TenantID).Validate(required("the tenant ID")),
			huh.NewInput().Title("Application (client) ID").
				Value(&a.ClientID).Validate(required("the client ID")),
		)); err != nil {
			return err
		}
		def := strings.TrimSpace(a.ClientID) + "/.default"
		if err := run(huh.NewGroup(
			huh.NewInput().Title("API scope the gateway validates").
				Description("Enter = " + def + " (the app is also the gateway's API).\nA separate gateway app: api://<gateway-app-id>/.default").
				Placeholder(def).Value(&a.Scope).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return nil
					}
					return config.ValidateEntraScopes(strings.TrimSpace(a.ClientID), []string{strings.TrimSpace(s)})
				}),
		)); err != nil {
			return err
		}
	case "google":
		if err := run(huh.NewGroup(
			huh.NewNote().Title("Step 2 of 6 · Google Workspace").
				Description("A Desktop-app OAuth client. Google access tokens are opaque,\nso this profile sends the ID token (aud = your client ID)."),
			huh.NewInput().Title("OAuth client ID").Value(&a.ClientID).Validate(required("the client ID")),
			huh.NewInput().Title("OAuth client secret").EchoMode(huh.EchoModePassword).
				Value(&a.ClientSecret).Validate(required("the client secret")),
		)); err != nil {
			return err
		}
	case "oidc":
		if err := run(huh.NewGroup(
			huh.NewNote().Title("Step 2 of 6 · OpenID Connect provider"),
			huh.NewInput().Title("Issuer URL").Placeholder("https://your.okta.com").
				Value(&a.Issuer).Validate(validateHTTPURL),
			huh.NewInput().Title("Client ID").Value(&a.ClientID).Validate(required("the client ID")),
			huh.NewInput().Title("Client secret").Description("Leave blank for a public client with PKCE.").
				EchoMode(huh.EchoModePassword).Value(&a.ClientSecret),
			huh.NewInput().Title("Scopes").Placeholder("openid profile email").Value(&a.Scopes),
			huh.NewSelect[string]().Title("Token to send to the gateway").
				Options(huh.NewOption("Access token", "access"), huh.NewOption("ID token", "id")).
				Value(&a.Credential),
		)); err != nil {
			return err
		}
	}

	if err := run(huh.NewGroup(
		huh.NewNote().Title("Step 3 of 6 · Sign-in"),
		huh.NewSelect[string]().Title("Login flow").
			Options(
				huh.NewOption("Browser (recommended)", "auth_code"),
				huh.NewOption("Device code — for SSH/headless; may be blocked by Conditional Access", "device_code"),
			).Value(&a.Flow),
		huh.NewConfirm().Title("Re-open the browser automatically when the session expires?").
			Affirmative("Yes").Negative("No").Value(&a.AutoReauth),
	)); err != nil {
		return err
	}
	if a.Flow == "auth_code" {
		if err := run(huh.NewGroup(
			huh.NewInput().Title("Fixed callback port").
				Description("Enter = random. Set one for devcontainer/SSH port forwarding,\nor for IdPs that match ports exactly (Entra ignores the port).").
				Value(&a.Port).Validate(validatePort),
		)); err != nil {
			return err
		}
	}

	defURL := defaultBaseURL(config.Gateway(a.Gateway))
	pass := huh.NewOption("Passthrough — send the SSO token; the gateway validates it", "passthrough")
	exch := huh.NewOption("Exchange — trade the SSO token for a gateway key", "exchange")
	modes := []huh.Option[string]{pass, exch}
	if gi, ok := gateway.Get(config.Gateway(a.Gateway)); ok && gi.DefaultMode == config.ModeExchange {
		modes = []huh.Option[string]{exch, pass}
	}
	urlDesc := "The root URL; Claude Code appends /v1/messages."
	if defURL != "" {
		urlDesc += "\nEnter = " + defURL
	}
	if err := run(huh.NewGroup(
		huh.NewNote().Title("Step 4 of 6 · Gateway"),
		huh.NewInput().Title("Gateway base URL").Description(urlDesc).
			Placeholder(defURL).Value(&a.BaseURL).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" && defURL != "" {
					return nil
				}
				return validateHTTPURL(s)
			}),
		huh.NewSelect[string]().Title("Credential mode").Options(modes...).Value(&a.Mode),
	)); err != nil {
		return err
	}
	if a.Mode == "exchange" {
		if err := run(huh.NewGroup(
			huh.NewSelect[string]().Title("Exchange style").
				Options(huh.NewOption("RFC 8693 token exchange", "rfc8693"), huh.NewOption("Custom broker (JSON response)", "broker")).
				Value(&a.ExStyle),
			huh.NewInput().Title("Token-exchange / broker URL").Value(&a.ExTokenURL).Validate(validateHTTPURL),
		)); err != nil {
			return err
		}
		var field *huh.Input
		if a.ExStyle == "rfc8693" {
			field = huh.NewInput().Title("Exchange audience").Description("Identifies the gateway to the token service.").
				Placeholder(a.Gateway).Value(&a.ExAudience)
		} else {
			field = huh.NewInput().Title("JSON field holding the gateway key").Placeholder("key").Value(&a.ExKeyField)
		}
		if err := run(huh.NewGroup(field)); err != nil {
			return err
		}
	}
	if a.Gateway == "portkey" {
		if err := run(huh.NewGroup(
			huh.NewInput().Title("Portkey provider slug").Description("Sent as x-portkey-provider. Enter = @anthropic-prod").
				Placeholder("@anthropic-prod").Value(&a.PortkeySlug),
		)); err != nil {
			return err
		}
	}

	if err := run(huh.NewGroup(
		huh.NewNote().Title("Step 5 of 6 · Optional"),
		huh.NewText().Title("Extra headers for the gateway").
			Description("One \"Name: value\" per line, e.g. x-team: platform. Leave empty for none.").
			Value(&a.Headers).
			Validate(func(s string) error { _, err := parseHeaderLines(s); return err }),
		huh.NewInput().Title("Model for background tasks / the haiku alias").
			Description("Only if the gateway names Haiku differently. Leave empty for Claude Code's default.").
			Value(&a.HaikuModel),
		huh.NewConfirm().Title("Let Claude Code list models via GET /v1/models on the gateway?").
			Affirmative("Yes").Negative("No").Value(&a.ModelDiscovery),
	)); err != nil {
		return err
	}

	if askUse {
		return run(huh.NewGroup(
			huh.NewNote().Title("Step 6 of 6 · Claude Code"),
			huh.NewSelect[string]().Title("Where should Claude Code use this profile?").
				Options(
					huh.NewOption(fmt.Sprintf("Launcher — only sessions started with `ccauth claude -p %s`", a.Name), "launcher"),
					huh.NewOption("This repository — .claude/settings.local.json", "project"),
					huh.NewOption("Every session — ~/.claude/settings.json", "global"),
				).
				Value(&a.Use),
		))
	}
	return nil
}

// setupTheme follows --no-color / NO_COLOR.
func setupTheme() huh.Theme {
	if ui.NoColor() {
		return huh.ThemeFunc(huh.ThemeBase)
	}
	return huh.ThemeFunc(huh.ThemeCharm)
}
