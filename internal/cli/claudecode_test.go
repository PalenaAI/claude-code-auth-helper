// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
)

func layer(name string, data map[string]any) claudeLayer {
	return claudeLayer{Name: name, Path: name + ".json", Data: data}
}

func wired(helper string) map[string]any {
	return map[string]any{
		"apiKeyHelper": helper,
		"env":          map[string]any{"ANTHROPIC_BASE_URL": "https://gw.example.com"},
	}
}

func noEnv(string) string { return "" }

// hasFinding reports whether any finding has status and contains substr.
func hasFinding(fs []finding, status, substr string) bool {
	for _, f := range fs {
		if f.Status == status && strings.Contains(f.Msg, substr) {
			return true
		}
	}
	return false
}

func TestCheckClaudeCode(t *testing.T) {
	tests := []struct {
		name       string
		layers     []claudeLayer
		env        map[string]string
		wantStatus string
		wantMsg    string
	}{
		{"healthy user wiring", []claudeLayer{layer("user", wired("ccauth token --profile corp"))}, nil, "✓", "ccauth token --profile corp"},
		{"no helper anywhere is fine for the launcher", []claudeLayer{layer("user", map[string]any{})}, nil, "•", "ccauth claude -p corp"},
		{"foreign helper", []claudeLayer{layer("user", wired("/usr/local/bin/get-key"))}, nil, "⚠", "not ccauth"},
		{"wrong profile", []claudeLayer{layer("user", wired("ccauth token -p other"))}, nil, "⚠", `does not use profile "corp"`},
		{"managed helper wins over user", []claudeLayer{
			layer("managed", wired("ccauth token --profile corp")),
			layer("user", wired("/usr/local/bin/get-key")),
		}, nil, "✓", "managed layer"},
		{"project helper needs trust", []claudeLayer{layer("project", wired("ccauth token --profile corp"))}, nil, "•", "workspace trust"},
		{"auth token in shell shadows helper", []claudeLayer{layer("user", wired("ccauth token --profile corp"))},
			map[string]string{"ANTHROPIC_AUTH_TOKEN": "x"}, "✗", "ANTHROPIC_AUTH_TOKEN is set in your environment"},
		{"api key in settings env shadows helper", []claudeLayer{layer("user", map[string]any{
			"apiKeyHelper": "ccauth token --profile corp",
			"env":          map[string]any{"ANTHROPIC_BASE_URL": "https://gw", "ANTHROPIC_API_KEY": "sk-x"},
		})}, nil, "✗", "sets ANTHROPIC_API_KEY"},
		{"cloud provider outranks helper", []claudeLayer{layer("user", wired("ccauth token --profile corp"))},
			map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}, "✗", "CLAUDE_CODE_USE_BEDROCK"},
		{"managed forceLoginMethod blocks helper", []claudeLayer{
			layer("managed", map[string]any{"forceLoginMethod": "claudeai"}),
			layer("user", wired("ccauth token --profile corp")),
		}, nil, "✗", "forceLoginMethod"},
		{"managed forceLoginOrgUUID blocks helper", []claudeLayer{
			layer("managed", map[string]any{"forceLoginOrgUUID": "abc"}),
			layer("user", wired("ccauth token --profile corp")),
		}, nil, "✗", "forceLoginOrgUUID"},
		{"user forceLoginMethod is not a managed block", []claudeLayer{layer("user", map[string]any{
			"forceLoginMethod": "claudeai", "apiKeyHelper": "ccauth token --profile corp",
			"env": map[string]any{"ANTHROPIC_BASE_URL": "https://gw"},
		})}, nil, "✓", "apiKeyHelper"},
		{"missing base url", []claudeLayer{layer("user", map[string]any{"apiKeyHelper": "ccauth token --profile corp"})}, nil, "⚠", "ANTHROPIC_BASE_URL is not set"},
		{"deprecated small fast model", []claudeLayer{layer("user", map[string]any{
			"apiKeyHelper": "ccauth token --profile corp",
			"env":          map[string]any{"ANTHROPIC_BASE_URL": "https://gw", envSmallFastModel: "m"},
		})}, nil, "⚠", "deprecated"},
		{"invalid json", []claudeLayer{{Name: "user", Path: "user.json", Data: map[string]any{}, LoadErr: os.ErrInvalid}}, nil, "⚠", "not valid JSON"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string { return tc.env[k] }
			got := checkClaudeCode(tc.layers, getenv, "corp")
			if !hasFinding(got, tc.wantStatus, tc.wantMsg) {
				t.Errorf("want %s finding containing %q, got %+v", tc.wantStatus, tc.wantMsg, got)
			}
		})
	}
}

func TestCheckClaudeCodeHealthyHasNoProblems(t *testing.T) {
	got := checkClaudeCode([]claudeLayer{layer("user", wired("ccauth token --profile corp"))}, noEnv, "corp")
	for _, f := range got {
		if f.Status == "✗" || f.Status == "⚠" {
			t.Errorf("unexpected problem finding: %+v", f)
		}
	}
}

func TestHelperUsesProfile(t *testing.T) {
	tests := []struct {
		cmd  string
		want bool
	}{
		{"ccauth token --profile corp", true},
		{"ccauth token -p corp", true},
		{"ccauth token --profile=corp", true},
		{`ccauth token --profile "corp"`, true},
		{"ccauth token", true}, // default profile
		{"ccauth token --profile other", false},
	}
	for _, tc := range tests {
		if got := helperUsesProfile(tc.cmd, "corp"); got != tc.want {
			t.Errorf("helperUsesProfile(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestLoadClaudeLayersOrderAndDropIns(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "managed")
	project := filepath.Join(root, "proj")
	user := filepath.Join(root, "home", "settings.json")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(managed, "managed-settings.json"), `{}`)
	write(filepath.Join(managed, "managed-settings.d", "50-ccauth.json"), `{"apiKeyHelper":"ccauth token"}`)
	write(filepath.Join(managed, "managed-settings.d", "10-base.json"), `{}`)
	write(filepath.Join(project, ".claude", "settings.json"), `{not json`)
	write(user, ``)

	got := loadClaudeLayers(managed, project, user)
	var names []string
	for _, l := range got {
		names = append(names, l.Name+":"+filepath.Base(l.Path))
	}
	want := "managed:managed-settings.json managed:10-base.json managed:50-ccauth.json project:settings.json user:settings.json"
	if strings.Join(names, " ") != want {
		t.Errorf("layers = %v, want %s", names, want)
	}
	if got[3].LoadErr == nil {
		t.Error("invalid project JSON should set LoadErr")
	}
	if got[2].str("apiKeyHelper") != "ccauth token" {
		t.Error("drop-in apiKeyHelper not parsed")
	}
}

func TestBuildEnvModelOptions(t *testing.T) {
	prof := config.Profile{GatewayOpts: config.GatewayOpts{
		BaseURL:        "https://gw",
		Model:          "claude-sonnet",
		HaikuModel:     "claude-haiku",
		ModelDiscovery: true,
	}}
	env := buildEnv(prof)
	for k, want := range map[string]string{
		"ANTHROPIC_BASE_URL": "https://gw",
		"ANTHROPIC_MODEL":    "claude-sonnet",
		envHaikuModel:        "claude-haiku",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
	} {
		if env[k] != want {
			t.Errorf("env[%s] = %q, want %q", k, env[k], want)
		}
	}
	if _, set := buildEnv(config.Profile{})["CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"]; set {
		t.Error("model discovery must be off unless enabled")
	}
	if _, set := env[envSmallFastModel]; set {
		t.Error("must never emit the deprecated small/fast model variable")
	}
}

func TestMergeClaudeSettingsMigratesAndWarns(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	path := filepath.Join(dir, "settings.json")
	existing := `{"theme":"dark","env":{"ANTHROPIC_SMALL_FAST_MODEL":"old-haiku","ANTHROPIC_API_KEY":"sk-x"}}`
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	notes, err := mergeClaudeSettings("corp", config.Profile{GatewayOpts: config.GatewayOpts{BaseURL: "https://gw"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	env := got["env"].(map[string]any)
	if got["theme"] != "dark" || got["apiKeyHelper"] != "ccauth token --profile corp" {
		t.Errorf("unexpected top-level settings: %v", got)
	}
	if _, set := env[envSmallFastModel]; set || env[envHaikuModel] != "old-haiku" {
		t.Errorf("small/fast model not migrated: %v", env)
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "renamed deprecated") || !strings.Contains(joined, "ANTHROPIC_API_KEY") {
		t.Errorf("notes = %q, want migration + shadowing warning", joined)
	}
}

func TestMigrateEnvKeepsExplicitHaiku(t *testing.T) {
	env := map[string]any{envSmallFastModel: "old", envHaikuModel: "new"}
	migrateEnv(env)
	if _, set := env[envSmallFastModel]; set || env[envHaikuModel] != "new" {
		t.Errorf("explicit haiku model must win: %v", env)
	}
	if notes := migrateEnv(map[string]any{}); notes != nil {
		t.Errorf("no-op migration should report nothing, got %v", notes)
	}
}
