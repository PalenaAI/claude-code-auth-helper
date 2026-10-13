// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

//go:build !windows

package cli

import (
	"fmt"
	"syscall"
)

// execClaude replaces this process with Claude Code, so it owns the terminal
// and receives signals directly.
func execClaude(bin string, argv, env []string) error {
	if err := syscall.Exec(bin, argv, env); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	return nil
}
