// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package jwtutil

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func signed(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte("test-key"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func TestParseJWTClaims(t *testing.T) {
	exp := time.Now().Add(42 * time.Minute).Truncate(time.Second)
	tok := signed(t, jwt.MapClaims{
		"iss":   "https://issuer.example",
		"sub":   "user-123",
		"aud":   "api://gateway",
		"email": "dev@example.com",
		"exp":   exp.Unix(),
	})
	c, err := Parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !c.IsJWT {
		t.Fatal("expected IsJWT true")
	}
	if !c.Expiry.Equal(exp) {
		t.Errorf("expiry = %v, want %v", c.Expiry, exp)
	}
	if c.Issuer != "https://issuer.example" {
		t.Errorf("issuer = %q", c.Issuer)
	}
	if c.Email != "dev@example.com" {
		t.Errorf("email = %q", c.Email)
	}
	if len(c.Audience) != 1 || c.Audience[0] != "api://gateway" {
		t.Errorf("aud = %v", c.Audience)
	}
}

func TestParseExpiredTokenStillReadable(t *testing.T) {
	// An expired token must still parse (we use it to decide to refresh).
	exp := time.Now().Add(-time.Hour).Truncate(time.Second)
	tok := signed(t, jwt.MapClaims{"exp": exp.Unix()})
	if got := ExpiryOf(tok); !got.Equal(exp) {
		t.Errorf("ExpiryOf = %v, want %v", got, exp)
	}
}

func TestParseOpaqueToken(t *testing.T) {
	c, err := Parse("ya29.opaque-google-access-token")
	if err != nil {
		t.Fatalf("parse opaque: %v", err)
	}
	if c.IsJWT {
		t.Error("opaque token should not be flagged as JWT")
	}
	if !ExpiryOf("ya29.opaque").IsZero() {
		t.Error("opaque token should have zero expiry")
	}
}
