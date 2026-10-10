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
	if prof.GatewayOpts.Model != "" {
		env["ANTHROPIC_MODEL"] = prof.GatewayOpts.Model
	}
	if prof.GatewayOpts.HaikuModel != "" {
		env[envHaikuModel] = prof.GatewayOpts.HaikuModel
	}
	if prof.GatewayOpts.ModelDiscovery {
		env["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"] = "1"
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
// preserving any other keys and env values already present. A deprecated
// ANTHROPIC_SMALL_FAST_MODEL is renamed to its replacement. The returned notes
// describe that migration and any settings that would stop the helper from
// being used; callers print them to stderr.
func mergeClaudeSettings(name string, prof config.Profile) ([]string, error) {
	path := claudeSettingsPath()
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if len(b) > 0 {
			if err := json.Unmarshal(b, &settings); err != nil {
				return nil, fmt.Errorf("existing %s is not valid JSON: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	settings["apiKeyHelper"] = helperCommand(name)
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	for k, v := range buildEnv(prof) {
		env[k] = v
	}
	notes := migrateEnv(env)
	settings["env"] = env
	for _, k := range shadowingCredentialVars {
		if _, set := env[k]; set {
			notes = append(notes, fmt.Sprintf("%s's env sets %s, which takes precedence over apiKeyHelper; remove it or ccauth will never be called", path, k))
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return nil, err
	}
	return notes, nil
}

// migrateEnv renames the deprecated small/fast model variable in a settings env
// block to ANTHROPIC_DEFAULT_HAIKU_MODEL, unless that is already set (then the
// deprecated key is just dropped). It reports what it changed.
func migrateEnv(env map[string]any) []string {
	old, ok := env[envSmallFastModel]
	if !ok {
		return nil
	}
	delete(env, envSmallFastModel)
	if _, set := env[envHaikuModel]; set {
		return []string{fmt.Sprintf("removed deprecated %s (%s is already set)", envSmallFastModel, envHaikuModel)}
	}
	env[envHaikuModel] = old
	return []string{fmt.Sprintf("renamed deprecated %s to %s", envSmallFastModel, envHaikuModel)}
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
