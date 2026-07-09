// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package credential turns a stored login session into the exact credential
// string Claude Code should send to the gateway. It owns the freshness logic:
// it refreshes the IdP token ahead of expiry and, in exchange mode, trades it
// for a gateway-native key.
package credential

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/jwtutil"
	"github.com/PalenaAI/claude-code-auth-helper/internal/oidc"
	"github.com/PalenaAI/claude-code-auth-helper/internal/store"
)

// defaultSkew is the minimum remaining lifetime we insist a token has before
// handing it out, even when Claude Code's TTL is tiny.
const defaultSkew = 2 * time.Minute

// ErrLoginRequired means silent refresh can't produce a credential — the user
// must authenticate interactively (there is no session, or the refresh token is
// expired/revoked). Callers may escalate to a browser login.
var ErrLoginRequired = errors.New("interactive login required")

// Emit returns the credential for the profile, refreshing/exchanging as needed,
// and persists any updated session.
func Emit(ctx context.Context, prof config.Profile, st store.Store, profileName string) (string, error) {
	sess, err := st.Load(profileName)
	if err != nil {
		if errors.Is(err, store.ErrNoSession) {
			return "", fmt.Errorf("%w: no stored session", ErrLoginRequired)
		}
		return "", err
	}
	cred, changed, err := ensure(ctx, prof, sess, requiredLifetime())
	if err != nil {
		return "", err
	}
	if changed {
		if serr := st.Save(profileName, sess); serr != nil {
			// Non-fatal: we still have a valid credential to emit this run.
			fmt.Fprintf(os.Stderr, "ccauth: warning: could not persist refreshed session: %v\n", serr)
		}
	}
	if cred == "" {
		return "", fmt.Errorf("no credential available (run `ccauth login --profile %s`)", profileName)
	}
	return cred, nil
}

// requiredLifetime returns how much remaining life a token must have. We read
// Claude Code's own cache TTL so the token we return outlives its cache window;
// otherwise Claude Code would keep using an expired token until it 401s.
func requiredLifetime() time.Duration {
	need := defaultSkew
	if v := os.Getenv("CLAUDE_CODE_API_KEY_HELPER_TTL_MS"); v != "" {
		if ms, err := strconv.Atoi(v); err == nil && ms > 0 {
			if d := time.Duration(ms)*time.Millisecond + defaultSkew; d > need {
				need = d
			}
		}
	}
	return need
}

// ensure makes the emitted credential fresh, refreshing the IdP token at most
// once per call. It mutates sess in place and reports whether it changed.
func ensure(ctx context.Context, prof config.Profile, sess *store.Session, minLife time.Duration) (cred string, changed bool, err error) {
	switch prof.Mode {
	case config.ModePassthrough:
		return ensurePassthrough(ctx, prof, sess, minLife)
	case config.ModeExchange:
		return ensureExchange(ctx, prof, sess, minLife)
	default:
		return "", false, fmt.Errorf("unknown mode %q", prof.Mode)
	}
}

func ensurePassthrough(ctx context.Context, prof config.Profile, sess *store.Session, minLife time.Duration) (string, bool, error) {
	tok := pickToken(sess, prof.OAuth.Credential)
	if tok != "" && remaining(tok, sess.IDPExpiry) > minLife {
		return tok, false, nil
	}
	// Stale or missing — refresh once.
	ns, err := oidc.Refresh(ctx, prof, sess)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrLoginRequired, err)
	}
	*sess = *ns
	tok = pickToken(sess, prof.OAuth.Credential)
	if tok == "" {
		return "", false, fmt.Errorf("provider did not return a %s token; check your scopes", prof.OAuth.Credential)
	}
	sess.EmitCredential = tok
	sess.EmitExpiry = tokenExpiry(tok, sess.IDPExpiry)
	return tok, true, nil
}

func ensureExchange(ctx context.Context, prof config.Profile, sess *store.Session, minLife time.Duration) (string, bool, error) {
	// Reuse a cached gateway key while it is fresh.
	if sess.EmitCredential != "" && time.Until(sess.EmitExpiry) > minLife {
		return sess.EmitCredential, false, nil
	}
	// Make sure the subject (IdP) token we exchange is itself valid.
	subjectKind := prof.Exchange.SubjectTokenType
	subject := pickToken(sess, subjectKind)
	refreshed := false
	if subject == "" || remaining(subject, sess.IDPExpiry) < defaultSkew {
		ns, err := oidc.Refresh(ctx, prof, sess)
		if err != nil {
			return "", false, fmt.Errorf("%w: %v", ErrLoginRequired, err)
		}
		*sess = *ns
		refreshed = true
		subject = pickToken(sess, subjectKind)
	}
	if subject == "" {
		return "", false, fmt.Errorf("no %s token to exchange; check your scopes", subjectKind)
	}
	key, ttl, err := exchange(ctx, prof, subject)
	if err != nil {
		return "", refreshed, err
	}
	sess.EmitCredential = key
	sess.EmitExpiry = time.Now().Add(ttl)
	return key, true, nil
}

// pickToken returns the access or ID token from the session.
func pickToken(sess *store.Session, kind config.TokenKind) string {
	if kind == config.TokenID {
		return sess.IDToken
	}
	return sess.AccessToken
}

// remaining returns how long a token is still valid. It prefers the token's own
// exp claim and falls back to the session's recorded expiry for opaque tokens.
func remaining(token string, fallback time.Time) time.Duration {
	if exp := jwtutil.ExpiryOf(token); !exp.IsZero() {
		return time.Until(exp)
	}
	if !fallback.IsZero() {
		return time.Until(fallback)
	}
	return 0
}

func tokenExpiry(token string, fallback time.Time) time.Time {
	if exp := jwtutil.ExpiryOf(token); !exp.IsZero() {
		return exp
	}
	return fallback
}
