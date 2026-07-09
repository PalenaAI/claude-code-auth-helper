// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package jwtutil reads (but does not verify) claims from a JWT. ccauth parses
// its own tokens only to learn when they expire so it can refresh ahead of time;
// cryptographic verification is the gateway's job, not the client's.
package jwtutil

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims holds the handful of registered claims ccauth cares about.
type Claims struct {
	Expiry   time.Time
	IssuedAt time.Time
	Issuer   string
	Subject  string
	Audience []string
	Email    string
	IsJWT    bool // false for opaque tokens (e.g. Google access tokens)
}

// Parse reads claims from a token without verifying its signature. Opaque
// (non-JWT) tokens return Claims{IsJWT: false} with no error.
func Parse(token string) (Claims, error) {
	c := Claims{}
	if token == "" {
		return c, nil
	}
	mc := jwt.MapClaims{}
	parser := jwt.NewParser(jwt.WithoutClaimsValidation())
	if _, _, err := parser.ParseUnverified(token, mc); err != nil {
		// Not a JWT (opaque token) — not an error for our purposes.
		return c, nil
	}
	c.IsJWT = true
	if exp, err := mc.GetExpirationTime(); err == nil && exp != nil {
		c.Expiry = exp.Time
	}
	if iat, err := mc.GetIssuedAt(); err == nil && iat != nil {
		c.IssuedAt = iat.Time
	}
	if iss, err := mc.GetIssuer(); err == nil {
		c.Issuer = iss
	}
	if sub, err := mc.GetSubject(); err == nil {
		c.Subject = sub
	}
	if aud, err := mc.GetAudience(); err == nil {
		c.Audience = aud
	}
	for _, k := range []string{"email", "preferred_username", "upn"} {
		if v, ok := mc[k].(string); ok && v != "" {
			c.Email = v
			break
		}
	}
	return c, nil
}

// ExpiryOf returns the expiry of a token, or the zero time if it has none or is
// opaque.
func ExpiryOf(token string) time.Time {
	c, _ := Parse(token)
	return c.Expiry
}
