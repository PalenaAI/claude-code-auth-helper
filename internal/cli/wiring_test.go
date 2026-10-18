// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
)

// isolateState points ccauth's state dir (wiring records, launch files) at a
// temp dir.
func isolateState(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	t.Setenv("CCAUTH_STATE_DIR", d)
	return d
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

var workProfile = config.Profile{GatewayOpts: config.GatewayOpts{
	BaseURL: "https://gw.example.com",
	TTLms:   3000000,
	Headers: map[string]string{"x-team": "platform"},
}}

func TestWireThenUnwireRestoresUserSettings(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"theme":"dark","env":{"MY_VAR":"keep","ANTHROPIC_MODEL":"mine"}}`)

	if _, err := wireFile(path, "work", workProfile, gateway.Header{Name: "x-cost-center", Value: "42"}); err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, path)
	env := got["env"].(map[string]any)
	if got["apiKeyHelper"] != "ccauth token --profile work" || env["ANTHROPIC_BASE_URL"] != "https://gw.example.com" {
		t.Fatalf("not wired: %v", got)
	}
	if env["ANTHROPIC_CUSTOM_HEADERS"] != "x-cost-center: 42\nx-team: platform" {
		t.Errorf("headers = %q", env["ANTHROPIC_CUSTOM_HEADERS"])
	}

	removed, notes, err := unwireFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Errorf("unexpected notes: %v", notes)
	}
	if len(removed) != 4 { // apiKeyHelper + base url + ttl + headers
		t.Errorf("removed = %v", removed)
	}
	want := map[string]any{"theme": "dark", "env": map[string]any{"MY_VAR": "keep", "ANTHROPIC_MODEL": "mine"}}
	if got := readJSON(t, path); !reflect.DeepEqual(got, want) {
		t.Errorf("after unwire = %v, want %v", got, want)
	}
}

func TestUnwireKeepsValuesChangedAfterWiring(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	if _, err := wireFile(path, "work", workProfile); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, path)
	s["env"].(map[string]any)["ANTHROPIC_BASE_URL"] = "https://edited.example.com"
	b, _ := json.Marshal(s)
	writeFile(t, path, string(b))

	_, notes, err := unwireFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	got := readJSON(t, path)
	if got["env"].(map[string]any)["ANTHROPIC_BASE_URL"] != "https://edited.example.com" {
		t.Errorf("edited value removed: %v", got)
	}
	if _, set := got["apiKeyHelper"]; set {
		t.Error("apiKeyHelper should be removed")
	}
	if !strings.Contains(strings.Join(notes, "\n"), "env.ANTHROPIC_BASE_URL changed") {
		t.Errorf("notes = %v", notes)
	}
}

func TestUnwireProjectDeletesEmptiedFile(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	if _, err := wireFile(path, "work", workProfile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := unwireFile(path, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("emptied project file should be deleted, stat err = %v", err)
	}
}

func TestUnwireWithoutRecordLeavesForeignHelper(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"apiKeyHelper":"/usr/local/bin/get-key","env":{"ANTHROPIC_BASE_URL":"https://x"}}`)
	removed, _, err := unwireFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Errorf("removed someone else's wiring: %v", removed)
	}
}

func TestUnwireLegacyWiringUsesHelperProfile(t *testing.T) {
	isolateState(t)
	cfgDir := t.TempDir()
	t.Setenv("CCAUTH_CONFIG_DIR", cfgDir)
	t.Setenv("CCAUTH_MANAGED_CONFIG", filepath.Join(cfgDir, "none.toml"))
	writeFile(t, filepath.Join(cfgDir, "config.toml"), `
[profiles.work]
provider = "entra"
gateway  = "litellm"
  [profiles.work.oauth]
  tenant_id = "t"
  client_id = "c"
  scopes    = ["c/.default"]
  [profiles.work.gateway_opts]
  base_url = "https://gw.example.com"
`)
	path := filepath.Join(t.TempDir(), "settings.json")
	// Written by an older ccauth: no wiring record.
	writeFile(t, path, `{"apiKeyHelper":"ccauth token --profile work","env":{"ANTHROPIC_BASE_URL":"https://gw.example.com","OTHER":"x"}}`)

	removed, _, err := unwireFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(removed, ",") != "apiKeyHelper,env.ANTHROPIC_BASE_URL" {
		t.Errorf("removed = %v", removed)
	}
	if got := readJSON(t, path); !reflect.DeepEqual(got, map[string]any{"env": map[string]any{"OTHER": "x"}}) {
		t.Errorf("after unwire = %v", got)
	}
}

func TestProjectSettingsPathUsesRepoRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	sub := filepath.Join(repo, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got := projectSettingsPath(sub)
	want := filepath.Join(repo, ".claude", "settings.local.json")
	if !sameDir(filepath.Dir(filepath.Dir(got)), repo) || filepath.Base(got) != "settings.local.json" {
		t.Errorf("projectSettingsPath(subdir) = %s, want %s", got, want)
	}

	outside := t.TempDir()
	if got := projectSettingsPath(outside); got != filepath.Join(outside, ".claude", "settings.local.json") {
		t.Errorf("outside a repo = %s", got)
	}
}

func TestLaunchEnv(t *testing.T) {
	base := []string{"PATH=/bin", "ANTHROPIC_API_KEY=sk-x", "ANTHROPIC_BASE_URL=https://old", "CLAUDE_CODE_USE_BEDROCK=1", "HOME=/h"}
	env, dropped := launchEnv(base, map[string]string{"ANTHROPIC_BASE_URL": "https://gw"}, "/h/.claude-work")
	want := []string{"PATH=/bin", "HOME=/h", "ANTHROPIC_BASE_URL=https://gw", "CLAUDE_CONFIG_DIR=/h/.claude-work"}
	if !reflect.DeepEqual(env, want) {
		t.Errorf("env = %v, want %v", env, want)
	}
	if !reflect.DeepEqual(dropped, []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK"}) {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestWriteLaunchSettings(t *testing.T) {
	isolateState(t)
	path, err := writeLaunchSettings("work", workProfile, []gateway.Header{{Name: "x-team", Value: "override"}})
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("launch settings must be 0600: %v %v", fi, err)
	}
	got := readJSON(t, path)
	if got["apiKeyHelper"] != "ccauth token --profile work" {
		t.Errorf("apiKeyHelper = %v", got["apiKeyHelper"])
	}
	if h := got["env"].(map[string]any)["ANTHROPIC_CUSTOM_HEADERS"]; h != "x-team: override" {
		t.Errorf("-H should override the profile header, got %q", h)
	}
}

func TestClaudeCmdRejectsOwnSettingsFlag(t *testing.T) {
	c := newClaudeCmd()
	c.SetArgs([]string{"--", "--settings", "x.json"})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "--settings") {
		t.Errorf("want --settings conflict error, got %v", err)
	}
}

func TestAuthStatusFinding(t *testing.T) {
	tests := []struct {
		method      string
		helperWired bool
		status      string
		msg         string
	}{
		{"api_key_helper", true, "✓", "apiKeyHelper"},
		{"claude.ai", false, "•", "ccauth claude"},
		{"claude.ai", true, "⚠", "although an apiKeyHelper"},
		{"api_key", true, "⚠", "api_key"},
		{"api_key", false, "•", "api_key"},
		{"none", false, "•", "no credential"},
	}
	for _, tc := range tests {
		f := authStatusFinding(claudeAuthStatus{AuthMethod: tc.method}, tc.helperWired)
		if f.Status != tc.status || !strings.Contains(f.Msg, tc.msg) {
			t.Errorf("%s/%v: got %+v, want %s ~%q", tc.method, tc.helperWired, f, tc.status, tc.msg)
		}
	}
}

// Regression: a user's settings.json already had ANTHROPIC_AUTH_TOKEN, which
// outranks apiKeyHelper, so wiring kept it and ccauth was never called.
func TestWireMovesCompetingCredentialsAndUnwireRestores(t *testing.T) {
	state := isolateState(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"theme":"dark","env":{"ANTHROPIC_AUTH_TOKEN":"personal-token","CLAUDE_CODE_USE_BEDROCK":"1","MY_VAR":"keep"}}`
	writeFile(t, path, original)

	notes, err := wireFile(path, "work", workProfile)
	if err != nil {
		t.Fatal(err)
	}
	env := readJSON(t, path)["env"].(map[string]any)
	for _, k := range []string{"ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK"} {
		if _, set := env[k]; set {
			t.Errorf("%s still in settings: helper would be ignored", k)
		}
		if !strings.Contains(strings.Join(notes, "\n"), "moved "+k) {
			t.Errorf("no note about moving %s: %v", k, notes)
		}
	}
	if env["MY_VAR"] != "keep" || env["ANTHROPIC_BASE_URL"] != "https://gw.example.com" {
		t.Errorf("env after wire = %v", env)
	}
	if fi, err := os.Stat(filepath.Join(state, "wiring.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("wiring record must be 0600 (it holds the moved token): %v %v", fi, err)
	}

	if _, _, err := unwireFile(path, false); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	_ = json.Unmarshal([]byte(original), &want)
	if got := readJSON(t, path); !reflect.DeepEqual(got, want) {
		t.Errorf("after unwire = %v, want the original %v", got, want)
	}
}

func TestRewireKeepsEarlierDisplacedValues(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"env":{"ANTHROPIC_AUTH_TOKEN":"personal-token"}}`)
	if _, err := wireFile(path, "work", workProfile); err != nil {
		t.Fatal(err)
	}
	// Second wire: the token is no longer in the file, but must not be forgotten.
	if _, err := wireFile(path, "work", workProfile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := unwireFile(path, false); err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"]; got != "personal-token" {
		t.Errorf("token after re-wire + unwire = %v", got)
	}
}

func TestUnwireKeepsCredentialSetAgain(t *testing.T) {
	isolateState(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	writeFile(t, path, `{"env":{"ANTHROPIC_AUTH_TOKEN":"old"}}`)
	if _, err := wireFile(path, "work", workProfile); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, path)
	s["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"] = "new"
	b, _ := json.Marshal(s)
	writeFile(t, path, string(b))

	_, notes, err := unwireFile(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readJSON(t, path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"]; got != "new" {
		t.Errorf("unwire overwrote the token set after wiring: %v", got)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "set again") {
		t.Errorf("notes = %v", notes)
	}
}

func TestLaunchSettingsNeutralizeCompetingCredentials(t *testing.T) {
	isolateState(t)
	path, err := writeLaunchSettings("work", workProfile, nil)
	if err != nil {
		t.Fatal(err)
	}
	env := readJSON(t, path)["env"].(map[string]any)
	for _, k := range competingVars() {
		if v, set := env[k]; !set || v != "" {
			t.Errorf("--settings env must set %s to \"\" (got %v, set=%v)", k, v, set)
		}
	}
	if env["ANTHROPIC_BASE_URL"] != "https://gw.example.com" {
		t.Errorf("gateway env lost: %v", env)
	}
}

func TestCompetingElsewhere(t *testing.T) {
	isolateState(t)
	cc := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", cc)
	user := filepath.Join(cc, "settings.json")
	writeFile(t, user, `{"env":{"ANTHROPIC_AUTH_TOKEN":"x","ANTHROPIC_API_KEY":""}}`)
	project := filepath.Join(t.TempDir(), ".claude", "settings.local.json")
	noEnv := func(string) string { return "" }

	got := strings.Join(competingElsewhere(project, noEnv), "\n")
	if !strings.Contains(got, "ANTHROPIC_AUTH_TOKEN") || strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("project wiring warnings = %q (an empty value is unset and must not warn)", got)
	}
	if got := competingElsewhere(user, noEnv); len(got) != 0 {
		t.Errorf("the wired file itself is handled by wireFile, got %v", got)
	}
	shell := func(k string) string {
		if k == "ANTHROPIC_API_KEY" {
			return "sk-x"
		}
		return ""
	}
	if got := strings.Join(competingElsewhere(user, shell), "\n"); !strings.Contains(got, "shell exports ANTHROPIC_API_KEY") {
		t.Errorf("shell warning missing: %q", got)
	}
}
