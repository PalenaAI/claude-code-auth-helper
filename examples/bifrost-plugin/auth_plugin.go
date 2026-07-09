// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package authplugin is a reference implementation of inbound JWT validation for
// Bifrost, which has no native data-plane JWT/OIDC auth.
//
// It validates an SSO JWT (as emitted by `ccauth` in passthrough mode) against
// your IdP's JWKS at the gateway edge: signature + issuer + audience + expiry.
// Wire Validate() into a Bifrost `HTTPTransportPreHook` (short-circuit with 401
// on failure), or use Middleware() as a standard net/http reverse-proxy guard.
//
// This is a self-contained, portable validator; adapt the adapter section to the
// exact Bifrost plugin interface in your deployment.
package authplugin

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Validator verifies bearer JWTs against an OIDC provider's JWKS.
type Validator struct {
	verifier *oidc.IDTokenVerifier
	issuer   string
}

// NewValidator performs OIDC discovery for issuer and returns a Validator that
// checks the token's audience equals audience.
//
//	Entra ID: issuer = https://login.microsoftonline.com/<tenant>/v2.0
//	          audience = <gateway-app-client-id> (v2 access token aud)
//	Google:   issuer = https://accounts.google.com
//	          audience = <your OAuth client id> (ID token aud)
func NewValidator(ctx context.Context, issuer, audience string) (*Validator, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery for %s: %w", issuer, err)
	}
	cfg := &oidc.Config{ClientID: audience}
	if audience == "" {
		cfg.SkipClientIDCheck = true // not recommended; prefer a real audience
	}
	return &Validator{verifier: provider.Verifier(cfg), issuer: issuer}, nil
}

// Claims are the fields a gateway typically maps to identity/authorization.
type Claims struct {
	Subject  string   `json:"sub"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
	Scope    string   `json:"scp"`
	TenantID string   `json:"tid"`
}

// Validate verifies a raw bearer token and returns its claims.
func (v *Validator) Validate(ctx context.Context, rawToken string) (*Claims, error) {
	idt, err := v.verifier.Verify(ctx, rawToken)
	if err != nil {
		return nil, fmt.Errorf("token verification failed: %w", err)
	}
	var c Claims
	if err := idt.Claims(&c); err != nil {
		return nil, fmt.Errorf("reading claims: %w", err)
	}
	return &c, nil
}

// bearerFromHeader extracts the token from an Authorization or x-api-key header.
// ccauth sends the credential in both.
func bearerFromHeader(h http.Header) string {
	if a := h.Get("Authorization"); a != "" {
		if strings.HasPrefix(strings.ToLower(a), "bearer ") {
			return strings.TrimSpace(a[7:])
		}
	}
	return strings.TrimSpace(h.Get("x-api-key"))
}

// Middleware guards an http.Handler (e.g. a reverse proxy to Bifrost). On a valid
// token it forwards, injecting identity headers Bifrost governance can key on.
func (v *Validator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := bearerFromHeader(r.Header)
		if tok == "" {
			http.Error(w, "missing bearer token", http.StatusUnauthorized)
			return
		}
		claims, err := v.Validate(r.Context(), tok)
		if err != nil {
			http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
			return
		}
		// Hand a trusted identity to Bifrost (map these to a virtual key / team).
		r.Header.Set("X-Auth-Subject", claims.Subject)
		if claims.Email != "" {
			r.Header.Set("X-Auth-Email", claims.Email)
		}
		next.ServeHTTP(w, r)
	})
}

// --- Bifrost HTTPTransportPreHook adapter (sketch) -------------------------
//
// Bifrost loads Go plugins that can intercept raw HTTP before the core. In your
// plugin's PreHook, do the equivalent of Middleware:
//
//	func (p *Plugin) HTTPTransportPreHook(ctx context.Context, req *http.Request) (*bifrost.ShortCircuit, error) {
//	    tok := bearerFromHeader(req.Header)
//	    claims, err := p.validator.Validate(ctx, tok)
//	    if err != nil {
//	        return &bifrost.ShortCircuit{Status: 401, Body: []byte("unauthorized")}, nil
//	    }
//	    req.Header.Set("X-Auth-Subject", claims.Subject) // map to a virtual key downstream
//	    return nil, nil // continue
//	}
//
// Pair this with `enforce_auth_on_inference: true` and a virtual key whose
// team/customer maps to the identity you extract here.
