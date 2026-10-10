// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package jwtutil

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// FuzzParse feeds arbitrary tokens to the unverified claim reader. Tokens come
// from the IdP or an exchange broker, so Parse must never panic and must treat
// anything that isn't a JWT as opaque rather than failing.
func FuzzParse(f *testing.F) {
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"exp": 1767225600, "iat": 1767222000, "iss": "https://idp", "aud": []string{"a", "b"}, "email": "u@example.com",
	})
	seed, err := tok.SignedString([]byte("k"))
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{"", seed, "opaque-access-token", "a.b.c", "e30.e30.", "eyJhbGciOiJub25lIn0.eyJleHAiOiJ4In0.", "...."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, token string) {
		c, err := Parse(token)
		if err != nil {
			t.Fatalf("Parse must not error, got %v", err)
		}
		if !c.IsJWT && !c.Expiry.IsZero() {
			t.Fatalf("opaque token reported an expiry: %+v", c)
		}
		if got := ExpiryOf(token); !got.Equal(c.Expiry) {
			t.Fatalf("ExpiryOf = %v, Parse expiry = %v", got, c.Expiry)
		}
	})
}
