// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Claude Code environment variables ccauth reasons about.
const (
	envHaikuModel = "ANTHROPIC_DEFAULT_HAIKU_MODEL"
	// envSmallFastModel is deprecated in Claude Code in favor of envHaikuModel.
	envSmallFastModel = "ANTHROPIC_SMALL_FAST_MODEL"
)

// shadowingCredentialVars outrank apiKeyHelper in Claude Code's authentication
// precedence: when one is set, the helper is never called.
var shadowingCredentialVars = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}

// cloudProviderVars switch Claude Code to Bedrock/Vertex/Foundry credentials,
// which outrank every Anthropic credential including apiKeyHelper.
var cloudProviderVars = []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"}

// claudeLayer is one Claude Code settings file that exists on disk.
type claudeLayer struct {
	Name    string // managed | project-local | project | user
	Path    string
	Data    map[string]any
	LoadErr error // set when the file exists but is not valid JSON
}

// isProject reports whether the layer only applies after workspace trust.
func (l claudeLayer) isProject() bool { return strings.HasPrefix(l.Name, "project") }

func (l claudeLayer) str(key string) string {
	v, _ := l.Data[key].(string)
	return v
}

func (l claudeLayer) envVar(key string) (string, bool) {
	env, _ := l.Data["env"].(map[string]any)
	v, ok := env[key]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// claudeManagedDir is the system directory Claude Code reads
// managed-settings.json and managed-settings.d/ from.
func claudeManagedDir() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "windows":
		pf := os.Getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		return filepath.Join(pf, "ClaudeCode")
	default:
		return "/etc/claude-code"
	}
}

// loadClaudeLayers reads the file-based Claude Code settings layers, highest
// precedence first: managed (managed-settings.json, then managed-settings.d/*.json
// in lexical order), project local, project shared, user. Missing files are
// skipped.
func loadClaudeLayers(managedDir, projectDir, userSettings string) []claudeLayer {
	var paths []struct{ name, path string }
	add := func(name, path string) { paths = append(paths, struct{ name, path string }{name, path}) }

	add("managed", filepath.Join(managedDir, "managed-settings.json"))
	dropIns, _ := filepath.Glob(filepath.Join(managedDir, "managed-settings.d", "*.json"))
	sort.Strings(dropIns)
	for _, p := range dropIns {
		add("managed", p)
	}
	add("project-local", filepath.Join(projectDir, ".claude", "settings.local.json"))
	add("project", filepath.Join(projectDir, ".claude", "settings.json"))
	add("user", userSettings)

	var layers []claudeLayer
	for _, p := range paths {
		b, err := os.ReadFile(p.path)
		if err != nil {
			continue
		}
		l := claudeLayer{Name: p.name, Path: p.path, Data: map[string]any{}}
		if len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal(b, &l.Data); err != nil {
				l.LoadErr = err
			}
		}
		layers = append(layers, l)
	}
	return layers
}

// finding is one doctor line: a status glyph and a message.
type finding struct {
	Status string // ✓ ✗ ⚠ •
	Msg    string
}

// checkClaudeCode inspects the Claude Code settings layers and the process
// environment for anything that would stop `ccauth token --profile <profile>`
// from being used as the credential: higher-precedence credentials, managed
// login restrictions that block apiKeyHelper, a missing or foreign helper, and
// deprecated variables.
func checkClaudeCode(layers []claudeLayer, getenv func(string) string, profile string) []finding {
	var out []finding
	add := func(status, format string, a ...any) { out = append(out, finding{status, fmt.Sprintf(format, a...)}) }

	for _, l := range layers {
		if l.LoadErr != nil {
			add("⚠", "%s settings %s is not valid JSON (%v); Claude Code may ignore it", l.Name, l.Path, l.LoadErr)
		}
	}

	// Credentials that outrank apiKeyHelper, from the shell or any layer's env.
	for _, k := range append(append([]string{}, cloudProviderVars...), shadowingCredentialVars...) {
		if v := getenv(k); v != "" {
			add("✗", "%s is set in your environment and takes precedence over apiKeyHelper; unset it", k)
		}
		for _, l := range layers {
			if v, ok := l.envVar(k); ok && v != "" {
				add("✗", "%s sets %s in env, which takes precedence over apiKeyHelper; remove it", l.Path, k)
			}
		}
	}

	// Managed login restrictions refuse apiKeyHelper at startup.
	for _, l := range layers {
		if l.Name != "managed" {
			continue
		}
		for _, k := range []string{"forceLoginMethod", "forceLoginOrgUUID"} {
			if _, ok := l.Data[k]; ok {
				add("✗", "managed settings %s set %s, which blocks apiKeyHelper — ask IT to remove it for gateway users", l.Path, k)
			}
		}
	}

	// The effective helper is the one from the highest-precedence layer.
	var helper *claudeLayer
	for i := range layers {
		if layers[i].str("apiKeyHelper") != "" {
			helper = &layers[i]
			break
		}
	}
	switch {
	case helper == nil:
		add("⚠", "no apiKeyHelper in any Claude Code settings file — run `ccauth wire --write`")
	default:
		cmd := helper.str("apiKeyHelper")
		switch {
		case !strings.Contains(cmd, "ccauth"):
			add("⚠", "apiKeyHelper from %s is %q, not ccauth", helper.Path, cmd)
		case !helperUsesProfile(cmd, profile):
			add("⚠", "apiKeyHelper from %s (%q) does not use profile %q", helper.Path, cmd, profile)
		default:
			add("✓", "apiKeyHelper (%s layer): %s", helper.Name, cmd)
		}
		if helper.isProject() {
			add("•", "the helper comes from project settings, so Claude Code runs it only after you accept the workspace trust prompt")
		}
	}

	// Base URL: without one, the gateway token would be sent to Anthropic.
	if getenv("ANTHROPIC_BASE_URL") == "" && !anyLayerEnv(layers, "ANTHROPIC_BASE_URL") {
		add("⚠", "ANTHROPIC_BASE_URL is not set anywhere — Claude Code would call api.anthropic.com instead of your gateway")
	}

	// Deprecated variables.
	if getenv(envSmallFastModel) != "" || anyLayerEnv(layers, envSmallFastModel) {
		add("⚠", "%s is deprecated; use %s (gateway_opts.haiku_model, or `ccauth wire --write` migrates it)", envSmallFastModel, envHaikuModel)
	}
	return out
}

// helperUsesProfile reports whether an apiKeyHelper command selects profile.
// A helper without --profile/-p uses the default profile, which is accepted.
func helperUsesProfile(cmd, profile string) bool {
	f := strings.Fields(cmd)
	for i, a := range f {
		switch {
		case (a == "--profile" || a == "-p") && i+1 < len(f):
			return strings.Trim(f[i+1], `"'`) == profile
		case strings.HasPrefix(a, "--profile="):
			return strings.Trim(strings.TrimPrefix(a, "--profile="), `"'`) == profile
		}
	}
	return true
}

func anyLayerEnv(layers []claudeLayer, key string) bool {
	for _, l := range layers {
		if v, ok := l.envVar(key); ok && v != "" {
			return true
		}
	}
	return false
}
