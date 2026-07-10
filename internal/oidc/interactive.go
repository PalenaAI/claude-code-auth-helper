// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"html"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
)

// loginTimeout bounds how long we wait for the user to complete a browser login.
const loginTimeout = 5 * time.Minute

// authCodeLogin runs authorization code + PKCE with a loopback redirect.
func (f *flow) authCodeLogin(ctx context.Context) (*oauth2.Token, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", f.prof.OAuth.RedirectPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind loopback listener on %s: %w", addr, err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	f.conf.RedirectURL = fmt.Sprintf("http://%s:%d/callback", f.prof.OAuth.RedirectHost, port)

	verifier := oauth2.GenerateVerifier()
	state, err := randToken()
	if err != nil {
		return nil, err
	}

	opts := append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}, f.extraAuth...)
	authURL := f.conf.AuthCodeURL(state, opts...)

	type result struct {
		code string
		err  error
	}
	resCh := make(chan result, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			writePage(w, "Login failed", "Authentication failed: "+e+". You can close this window.", false)
			resCh <- result{err: fmt.Errorf("authorization error: %s: %s", e, q.Get("error_description"))}
			return
		}
		if q.Get("state") != state {
			writePage(w, "Login failed", "State mismatch. You can close this window.", false)
			resCh <- result{err: fmt.Errorf("state mismatch (possible CSRF)")}
			return
		}
		writePage(w, "Login complete", "ccauth received your credentials — this window will close automatically.", true)
		resCh <- result{code: q.Get("code")}
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	fmt.Fprintln(os.Stderr, "Opening your browser to sign in. If it doesn't open, visit:")
	fmt.Fprintln(os.Stderr, "  "+authURL)
	if err := openBrowser(authURL); err != nil {
		fmt.Fprintln(os.Stderr, "(could not launch a browser automatically)")
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(loginTimeout):
		return nil, fmt.Errorf("timed out waiting for browser login")
	case res := <-resCh:
		if res.err != nil {
			return nil, res.err
		}
		tok, err := f.conf.Exchange(ctx, res.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, fmt.Errorf("code exchange failed: %w", err)
		}
		return tok, nil
	}
}

// deviceLogin runs the OAuth device authorization grant.
func (f *flow) deviceLogin(ctx context.Context) (*oauth2.Token, error) {
	if f.deviceAuthURL == "" {
		return nil, fmt.Errorf("provider does not advertise a device authorization endpoint")
	}
	da, err := f.conf.DeviceAuth(ctx, f.extraAuth...)
	if err != nil {
		return nil, fmt.Errorf("device authorization request failed: %w", err)
	}
	fmt.Fprintln(os.Stderr, "To sign in, visit:")
	fmt.Fprintln(os.Stderr, "  "+da.VerificationURI)
	fmt.Fprintln(os.Stderr, "and enter code:")
	fmt.Fprintln(os.Stderr, "  "+da.UserCode)
	if da.VerificationURIComplete != "" {
		fmt.Fprintln(os.Stderr, "(or open this direct link: "+da.VerificationURIComplete+")")
	}
	tok, err := f.conf.DeviceAccessToken(ctx, da, f.extraAuth...)
	if err != nil {
		return nil, fmt.Errorf("device token polling failed: %w", err)
	}
	return tok, nil
}

func randToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writePage(w http.ResponseWriter, title, body string, autoClose bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	title = html.EscapeString(title)
	body = html.EscapeString(body)
	// Best-effort auto-close: browsers only permit window.close() on tabs opened by
	// script, so if the browser refuses we reveal a "you can close this" fallback.
	extra := ""
	if autoClose {
		extra = `<p id="cc-fallback" style="color:#888;font-size:.9em;display:none">` +
			`You can close this window and return to the terminal.</p>` +
			`<script>setTimeout(function(){window.close();` +
			`setTimeout(function(){var f=document.getElementById('cc-fallback');` +
			`if(f){f.style.display='block';}},500);},1200);</script>`
	}
	fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>%s</title>"+
		"<body style=\"font-family:system-ui;max-width:32rem;margin:4rem auto;text-align:center\">"+
		"<h2>%s</h2><p>%s</p>%s</body>", title, title, body, extra)
}

// BrowserAvailable reports whether a system browser can plausibly be launched in
// this environment. It is used to decide whether `ccauth token` may escalate to
// an interactive re-auth, versus failing with a "run ccauth login" message.
func BrowserAvailable() bool {
	// A remote shell generally can't reach the user's local browser unless X11
	// is forwarded.
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return os.Getenv("DISPLAY") != ""
	}
	switch runtime.GOOS {
	case "darwin", "windows":
		return true
	default: // Linux/BSD need a display server.
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
