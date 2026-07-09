// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
)

// helperCommand is the apiKeyHelper value pointing back at this tool.
func helperCommand(name string) string {
	return fmt.Sprintf("ccauth token --profile %s", name)
}

// buildEnv renders the env block Claude Code needs for a profile.
func buildEnv(prof config.Profile) map[string]string {
	env := map[string]string{}
	if prof.GatewayOpts.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = prof.GatewayOpts.BaseURL
	}
	if prof.GatewayOpts.TTLms > 0 {
		env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"] = strconv.Itoa(prof.GatewayOpts.TTLms)
	}
	if h := gateway.CustomHeaders(prof); h != "" {
		env["ANTHROPIC_CUSTOM_HEADERS"] = h
	}
	return env
}

// settingsJSON renders the full settings.json snippet for a profile.
func settingsJSON(name string, prof config.Profile) (string, error) {
	m := map[string]any{
		"apiKeyHelper": helperCommand(name),
		"env":          buildEnv(prof),
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// claudeSettingsPath is where Claude Code reads user settings.
func claudeSettingsPath() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// mergeClaudeSettings merges the profile's wiring into ~/.claude/settings.json,
// preserving any other keys and env values already present.
func mergeClaudeSettings(name string, prof config.Profile) error {
	path := claudeSettingsPath()
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if len(b) > 0 {
			if err := json.Unmarshal(b, &settings); err != nil {
				return fmt.Errorf("existing %s is not valid JSON: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	settings["apiKeyHelper"] = helperCommand(name)
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	for k, v := range buildEnv(prof) {
		env[k] = v
	}
	settings["env"] = env

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// defaultBaseURL is the clean base URL suggestion for a gateway.
func defaultBaseURL(g config.Gateway) string {
	switch g {
	case config.GatewayLiteLLM:
		return "http://localhost:4000"
	case config.GatewayPortkey:
		return "https://api.portkey.ai"
	case config.GatewayBifrost:
		return "http://localhost:8080/anthropic"
	default:
		return ""
	}
}
