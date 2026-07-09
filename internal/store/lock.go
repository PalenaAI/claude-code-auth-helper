// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

// AcquireLoginLock serializes interactive re-authentication across concurrent
// `ccauth token` invocations, so multiple Claude Code requests hitting an
// expired session open exactly one browser tab, not one each.
//
// It blocks up to wait for the lock. A lock older than wait is considered stale
// (a previous holder died) and is stolen. The returned release func removes it.
func AcquireLoginLock(profile string, wait time.Duration) (func(), error) {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(config.StateDir(), profile+".login.lock")
	deadline := time.Now().Add(wait)

	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		// Lock held — steal it if stale, else wait.
		if fi, statErr := os.Stat(path); statErr == nil && time.Since(fi.ModTime()) > wait {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for login lock %s", path)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
