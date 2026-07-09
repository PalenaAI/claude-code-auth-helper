// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package oidc implements the interactive OAuth flows (`login`) and the silent
// refresh used by the credential helper. It speaks plain OIDC so a single engine
// covers Entra ID, Google, and any generic OIDC provider; provider-specific
// quirks are handled by small presets.
package oidc

import (
	"context"
	"fmt"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
)

// flow bundles everything needed to run an OAuth exchange for a profile.
type flow struct {
	prof          config.Profile
	conf          *oauth2.Config
	provider      *coreoidc.Provider
	deviceAuthURL string
	extraAuth     []oauth2.AuthCodeOption // provider-specific auth params (e.g. Google offline access)
	scopes        []string
}

// discoveryTimeout bounds the OIDC discovery HTTP call.
const discoveryTimeout = 15 * time.Second

// buildFlow performs OIDC discovery and assembles the oauth2 config.
func buildFlow(ctx context.Context, prof config.Profile) (*flow, error) {
	dctx, cancel := context.WithTimeout(ctx, discoveryTimeout)
	defer cancel()

	provider, err := coreoidc.NewProvider(dctx, prof.Issuer())
	if err != nil {
		return nil, fmt.Errorf("OIDC discovery for %s failed: %w", prof.Issuer(), err)
	}

	var disc struct {
		DeviceAuthURL string `json:"device_authorization_endpoint"`
	}
	_ = provider.Claims(&disc)

	scopes := effectiveScopes(prof)
	endpoint := provider.Endpoint()
	endpoint.DeviceAuthURL = disc.DeviceAuthURL

	f := &flow{
		prof:          prof,
		provider:      provider,
		deviceAuthURL: disc.DeviceAuthURL,
		scopes:        scopes,
		conf: &oauth2.Config{
			ClientID:     prof.OAuth.ClientID,
			ClientSecret: prof.OAuth.ClientSecret, // empty for public-client + PKCE
			Endpoint:     endpoint,
			Scopes:       scopes,
		},
	}

	// Google only returns a refresh token when offline access is requested and
	// consent is forced.
	if prof.Provider == config.ProviderGoogle {
		f.extraAuth = append(f.extraAuth,
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("prompt", "consent"),
		)
	}
	return f, nil
}

// effectiveScopes appends the standard OIDC scopes each provider needs.
func effectiveScopes(prof config.Profile) []string {
	set := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || set[s] {
			return
		}
		set[s] = true
		out = append(out, s)
	}
	for _, s := range prof.OAuth.Scopes {
		add(s)
	}
	switch prof.Provider {
	case config.ProviderEntra, config.ProviderOIDC:
		add("openid")
		add("offline_access") // needed for a refresh token
	case config.ProviderGoogle:
		add("openid") // offline access is requested via access_type=offline param
	}
	return out
}

// Login runs the interactive flow and returns a fresh session.
func Login(ctx context.Context, prof config.Profile, forceDevice bool) (*store.Session, error) {
	f, err := buildFlow(ctx, prof)
	if err != nil {
		return nil, err
	}
	useDevice := forceDevice || prof.OAuth.Flow == config.FlowDeviceCode
	var tok *oauth2.Token
	if useDevice {
		tok, err = f.deviceLogin(ctx)
	} else {
		tok, err = f.authCodeLogin(ctx)
	}
	if err != nil {
		return nil, err
	}
	return sessionFromToken(prof, f.scopes, tok), nil
}

// Refresh silently exchanges the stored refresh token for new tokens.
func Refresh(ctx context.Context, prof config.Profile, sess *store.Session) (*store.Session, error) {
	if sess.RefreshToken == "" {
		return nil, fmt.Errorf("no refresh token; run `ccauth login`")
	}
	f, err := buildFlow(ctx, prof)
	if err != nil {
		return nil, err
	}
	ts := f.conf.TokenSource(ctx, &oauth2.Token{RefreshToken: sess.RefreshToken})
	tok, err := ts.Token()
	if err != nil {
		return nil, fmt.Errorf("token refresh failed (re-run `ccauth login`): %w", err)
	}
	ns := sessionFromToken(prof, f.scopes, tok)
	// oauth2 keeps the old refresh token when the provider doesn't rotate it,
	// but guard against an empty value just in case.
	if ns.RefreshToken == "" {
		ns.RefreshToken = sess.RefreshToken
	}
	return ns, nil
}

func sessionFromToken(prof config.Profile, scopes []string, tok *oauth2.Token) *store.Session {
	s := &store.Session{
		Provider:     string(prof.Provider),
		RefreshToken: tok.RefreshToken,
		AccessToken:  tok.AccessToken,
		IDPExpiry:    tok.Expiry,
		Scopes:       scopes,
		ObtainedAt:   time.Now(),
	}
	if id, ok := tok.Extra("id_token").(string); ok {
		s.IDToken = id
	}
	return s
}
