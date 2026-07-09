// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

// Package store persists a login session (the long-lived refresh token plus the
// most recent short-lived tokens) securely between invocations of ccauth.
//
// The primary backend is the OS keychain (macOS Keychain, Windows Credential
// Manager, Linux Secret Service) via go-keyring. When no keychain is available
// (headless Linux, CI) it falls back to a 0600 file under the state directory.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

const keyringService = "ccauth"

// ErrNoSession is returned when a profile has no stored session yet.
var ErrNoSession = errors.New("no stored session (run `ccauth login`)")

// Session is everything we persist for a profile.
type Session struct {
	Provider     string    `json:"provider"`
	RefreshToken string    `json:"refresh_token"`
	AccessToken  string    `json:"access_token,omitempty"`
	IDToken      string    `json:"id_token,omitempty"`
	IDPExpiry    time.Time `json:"idp_expiry"`
	Scopes       []string  `json:"scopes,omitempty"`

	// Emitted credential cache. In passthrough mode this mirrors the chosen
	// IdP token; in exchange mode it is the gateway-native key.
	EmitCredential string    `json:"emit_credential,omitempty"`
	EmitExpiry     time.Time `json:"emit_expiry"`

	ObtainedAt time.Time `json:"obtained_at"`
	Backend    string    `json:"-"` // where this session was loaded from
}

// Store is a per-profile secret store.
type Store interface {
	Load(profile string) (*Session, error)
	Save(profile string, s *Session) error
	Delete(profile string) error
	Kind() string
}

// New returns a store for the given mode: "keyring", "file", or "auto".
func New(mode string) Store {
	switch mode {
	case "keyring":
		return &keyringStore{}
	case "file":
		return &fileStore{}
	default:
		return &autoStore{kr: &keyringStore{}, fs: &fileStore{}}
	}
}

// ----- keyring backend ---------------------------------------------------

type keyringStore struct{}

func (keyringStore) Kind() string { return "keyring" }

func (keyringStore) Load(profile string) (*Session, error) {
	raw, err := keyring.Get(keyringService, profile)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	s := &Session{}
	if err := json.Unmarshal([]byte(raw), s); err != nil {
		return nil, fmt.Errorf("corrupt session in keyring: %w", err)
	}
	s.Backend = "keyring"
	return s, nil
}

func (keyringStore) Save(profile string, s *Session) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return keyring.Set(keyringService, profile, string(b))
}

func (keyringStore) Delete(profile string) error {
	err := keyring.Delete(keyringService, profile)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// ----- file backend ------------------------------------------------------

type fileStore struct{}

func (fileStore) Kind() string { return "file" }

func (fileStore) path(profile string) string {
	return filepath.Join(config.StateDir(), profile+".json")
}

func (f fileStore) Load(profile string) (*Session, error) {
	b, err := os.ReadFile(f.path(profile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoSession
		}
		return nil, err
	}
	s := &Session{}
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("corrupt session file: %w", err)
	}
	s.Backend = "file"
	return s, nil
}

func (f fileStore) Save(profile string, s *Session) error {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Write via a 0600 temp file then rename for atomicity.
	tmp := f.path(profile) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.path(profile))
}

func (f fileStore) Delete(profile string) error {
	err := os.Remove(f.path(profile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ----- auto backend ------------------------------------------------------

// autoStore prefers the keyring and falls back to the file store when the
// keyring is unavailable. Load checks both so a session written to either is
// found.
type autoStore struct {
	kr *keyringStore
	fs *fileStore
}

func (autoStore) Kind() string { return "auto" }

func (a autoStore) Load(profile string) (*Session, error) {
	s, err := a.kr.Load(profile)
	if err == nil {
		return s, nil
	}
	if errors.Is(err, ErrNoSession) {
		// Not in keyring — maybe a previous run used the file store.
		if fs, ferr := a.fs.Load(profile); ferr == nil {
			return fs, nil
		}
		return nil, ErrNoSession
	}
	// Keyring backend unusable (e.g. no Secret Service). Try file.
	return a.fs.Load(profile)
}

func (a autoStore) Save(profile string, s *Session) error {
	if err := a.kr.Save(profile, s); err != nil {
		return a.fs.Save(profile, s)
	}
	return nil
}

func (a autoStore) Delete(profile string) error {
	_ = a.kr.Delete(profile)
	return a.fs.Delete(profile)
}
