// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package credential

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/jwtutil"
)

const (
	tokenExchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"
	accessTokenType    = "urn:ietf:params:oauth:token-type:access_token"
	idTokenType        = "urn:ietf:params:oauth:token-type:id_token"
	exchangeTimeout    = 20 * time.Second
	defaultKeyTTL      = 5 * time.Minute
)

// exchange trades the subject (IdP) token for a gateway-native credential and
// returns the credential plus its lifetime.
func exchange(ctx context.Context, prof config.Profile, subject string) (string, time.Duration, error) {
	switch prof.Exchange.Style {
	case "broker":
		return brokerExchange(ctx, prof, subject)
	default: // rfc8693
		return rfc8693Exchange(ctx, prof, subject)
	}
}

// rfc8693Exchange implements RFC 8693 OAuth 2.0 Token Exchange.
func rfc8693Exchange(ctx context.Context, prof config.Profile, subject string) (string, time.Duration, error) {
	form := url.Values{}
	form.Set("grant_type", tokenExchangeGrant)
	form.Set("subject_token", subject)
	if prof.Exchange.SubjectTokenType == config.TokenID {
		form.Set("subject_token_type", idTokenType)
	} else {
		form.Set("subject_token_type", accessTokenType)
	}
	form.Set("requested_token_type", accessTokenType)
	if prof.Exchange.Audience != "" {
		form.Set("audience", prof.Exchange.Audience)
	}
	if prof.Exchange.Resource != "" {
		form.Set("resource", prof.Exchange.Resource)
	}
	// Confidential exchange clients authenticate via HTTP Basic; public clients
	// pass client_id in the body. Build the form fully before encoding it.
	useBasic := prof.Exchange.ClientID != "" && prof.Exchange.ClientSecret != ""
	if prof.Exchange.ClientID != "" && !useBasic {
		form.Set("client_id", prof.Exchange.ClientID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prof.Exchange.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if useBasic {
		req.SetBasicAuth(prof.Exchange.ClientID, prof.Exchange.ClientSecret)
	}

	body, err := do(ctx, req)
	if err != nil {
		return "", 0, err
	}
	var resp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", 0, fmt.Errorf("exchange: bad JSON response: %w", err)
	}
	if resp.AccessToken == "" {
		return "", 0, fmt.Errorf("exchange: response contained no access_token: %s", truncate(body))
	}
	return resp.AccessToken, keyTTL(resp.AccessToken, resp.ExpiresIn), nil
}

// brokerExchange calls an org-specific broker that validates the IdP token
// (sent as a bearer) and returns a gateway key in a configurable JSON field.
func brokerExchange(ctx context.Context, prof config.Profile, subject string) (string, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prof.Exchange.TokenURL, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Authorization", "Bearer "+subject)
	req.Header.Set("Accept", "application/json")

	body, err := do(ctx, req)
	if err != nil {
		return "", 0, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return "", 0, fmt.Errorf("broker: bad JSON response: %w", err)
	}
	keyField := prof.Exchange.KeyField
	if keyField == "" {
		keyField = "key"
	}
	key, _ := m[keyField].(string)
	if key == "" {
		return "", 0, fmt.Errorf("broker: field %q missing/empty in response: %s", keyField, truncate(body))
	}
	ttl := defaultKeyTTL
	expiryField := prof.Exchange.ExpiryField
	if expiryField == "" {
		expiryField = "expires_in"
	}
	if secs, ok := m[expiryField].(float64); ok && secs > 0 {
		ttl = time.Duration(secs) * time.Second
	} else {
		ttl = keyTTL(key, 0)
	}
	return key, ttl, nil
}

func do(ctx context.Context, req *http.Request) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(cctx))
	if err != nil {
		return nil, fmt.Errorf("exchange request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exchange endpoint returned %s: %s", resp.Status, truncate(body))
	}
	return body, nil
}

// keyTTL derives a credential lifetime from an explicit expires_in, else the
// token's exp claim, else a conservative default.
func keyTTL(token string, expiresIn int) time.Duration {
	if expiresIn > 0 {
		return time.Duration(expiresIn) * time.Second
	}
	if exp := jwtutil.ExpiryOf(token); !exp.IsZero() {
		if d := time.Until(exp); d > 0 {
			return d
		}
	}
	return defaultKeyTTL
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
