// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

//go:build windows

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// execClaude runs Claude Code as a child (Windows has no exec) and exits with
// its exit code.
func execClaude(bin string, argv, env []string) error {
	cmd := exec.Command(bin, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		os.Exit(exitErr.ExitCode())
	}
	if err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}
	os.Exit(0)
	return nil
}
