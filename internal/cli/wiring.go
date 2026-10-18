// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
)

// helperCommand is the apiKeyHelper value pointing back at this tool.
func helperCommand(name string) string {
	return fmt.Sprintf("ccauth token --profile %s", name)
}

// buildEnv renders the env block Claude Code needs for a profile. Extra headers
// (e.g. from `ccauth claude -H`) are merged into ANTHROPIC_CUSTOM_HEADERS.
func buildEnv(prof config.Profile, extra ...gateway.Header) map[string]string {
	env := map[string]string{}
	if prof.GatewayOpts.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = prof.GatewayOpts.BaseURL
	}
	if prof.GatewayOpts.TTLms > 0 {
		env["CLAUDE_CODE_API_KEY_HELPER_TTL_MS"] = strconv.Itoa(prof.GatewayOpts.TTLms)
	}
	if h := gateway.CustomHeaders(prof, extra...); h != "" {
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

// wiringSettings is the Claude Code settings fragment for a profile.
func wiringSettings(name string, prof config.Profile, extra ...gateway.Header) map[string]any {
	return map[string]any{
		"apiKeyHelper": helperCommand(name),
		"env":          buildEnv(prof, extra...),
	}
}

// settingsJSON renders the settings.json snippet for a profile.
func settingsJSON(name string, prof config.Profile, extra ...gateway.Header) (string, error) {
	b, err := json.MarshalIndent(wiringSettings(name, prof, extra...), "", "  ")
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

// projectSettingsPath is the .claude/settings.local.json Claude Code reads for a
// session started in dir. Like Claude Code, it uses the git repository root (the
// main checkout's root for a worktree), and dir itself outside a repository, on
// Windows, or when the repository root is the home directory.
func projectSettingsPath(dir string) string {
	return filepath.Join(projectRoot(dir), ".claude", "settings.local.json")
}

func projectRoot(dir string) string {
	if runtime.GOOS == "windows" {
		return dir
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return dir
	}
	common := filepath.Clean(strings.TrimSpace(string(out)))
	if filepath.Base(common) != ".git" {
		return dir // bare repository or unusual layout: no checkout root to use
	}
	root := filepath.Dir(common)
	if home, err := os.UserHomeDir(); err == nil && sameDir(root, home) {
		return dir
	}
	return root
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// readSettings loads a settings file; a missing or empty file is an empty map.
func readSettings(path string) (map[string]any, error) {
	settings := map[string]any{}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return settings, nil
	}
	if err := json.Unmarshal(b, &settings); err != nil {
		return nil, fmt.Errorf("existing %s is not valid JSON: %w", path, err)
	}
	return settings, nil
}

// writeSettings writes settings atomically, keeping an existing file's mode.
func writeSettings(path string, settings map[string]any) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".ccauth.tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// mergeClaudeSettings wires a profile into the user settings file.
func mergeClaudeSettings(name string, prof config.Profile, extra ...gateway.Header) ([]string, error) {
	return wireFile(claudeSettingsPath(), name, prof, extra...)
}

// wireFile merges the profile's wiring into a Claude Code settings file,
// preserving every other key and env value. A deprecated
// ANTHROPIC_SMALL_FAST_MODEL is renamed to its replacement. What ccauth wrote is
// recorded so `ccauth unwire` can later remove exactly that. The returned notes
// describe the migration and any settings that would stop the helper from being
// used; callers print them to stderr.
func wireFile(path, name string, prof config.Profile, extra ...gateway.Header) ([]string, error) {
	settings, err := readSettings(path)
	if err != nil {
		return nil, err
	}
	helper := helperCommand(name)
	settings["apiKeyHelper"] = helper
	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	written := buildEnv(prof, extra...)
	for k, v := range written {
		env[k] = v
	}
	notes := migrateEnv(env)

	// A credential or cloud-provider switch in the same env block outranks
	// apiKeyHelper, so the wiring would silently do nothing. Move them out;
	// the wiring record keeps them so `ccauth unwire` can put them back.
	rec := wiringRecord{APIKeyHelper: helper, Env: written, Displaced: map[string]string{}}
	if old, err := loadWiringRecords(); err == nil {
		for k, v := range old[recordKey(path)].Displaced {
			rec.Displaced[k] = v // keep what an earlier wire moved aside
		}
	}
	for _, k := range competingVars() {
		v, set := env[k]
		if !set {
			continue
		}
		rec.Displaced[k] = fmt.Sprint(v)
		delete(env, k)
		notes = append(notes, fmt.Sprintf("moved %s out of %s: it takes precedence over apiKeyHelper, so ccauth would never be called (`ccauth unwire` puts it back)", k, path))
	}
	if len(rec.Displaced) == 0 {
		rec.Displaced = nil
	}
	settings["env"] = env

	// Record first: once the file no longer holds a moved credential, the
	// record is the only copy.
	if err := recordWiring(path, rec); err != nil {
		if rec.Displaced != nil {
			return nil, fmt.Errorf("could not save the settings ccauth has to move aside, so %s was left unchanged: %w", path, err)
		}
		notes = append(notes, fmt.Sprintf("could not record the wiring for `ccauth unwire`: %v", err))
	}
	if err := writeSettings(path, settings); err != nil {
		return nil, err
	}
	return notes, nil
}

// competingElsewhere lists credentials that would still outrank apiKeyHelper
// from outside the wired file: the user settings file (for a project wiring)
// and the shell environment. Claude Code doesn't let project settings override
// them, so all ccauth can do is tell the user.
func competingElsewhere(wiredPath string, getenv func(string) string) []string {
	var out []string
	if user := claudeSettingsPath(); recordKey(user) != recordKey(wiredPath) {
		if s, err := readSettings(user); err == nil {
			if env, ok := s["env"].(map[string]any); ok {
				for _, k := range competingVars() {
					if v, set := env[k]; set && fmt.Sprint(v) != "" {
						out = append(out, fmt.Sprintf("%s sets %s, which outranks this wiring; remove it there (or use `ccauth wire --write`, which moves it aside)", user, k))
					}
				}
			}
		}
	}
	for _, k := range competingVars() {
		if getenv(k) != "" {
			out = append(out, fmt.Sprintf("your shell exports %s, which outranks apiKeyHelper; unset it before starting Claude Code (`ccauth claude` does this for you)", k))
		}
	}
	return out
}

// wiringRecord is what ccauth last wrote into one settings file.
type wiringRecord struct {
	APIKeyHelper string            `json:"apiKeyHelper"`
	Env          map[string]string `json:"env"`
	// Displaced holds env values wire moved out of the file because they
	// outrank apiKeyHelper (e.g. ANTHROPIC_AUTH_TOKEN); unwire restores them.
	// The record file is 0600 in ccauth's state dir.
	Displaced map[string]string `json:"displaced,omitempty"`
}

func wiringRecordPath() string { return filepath.Join(config.StateDir(), "wiring.json") }

func loadWiringRecords() (map[string]wiringRecord, error) {
	recs := map[string]wiringRecord{}
	b, err := os.ReadFile(wiringRecordPath())
	if os.IsNotExist(err) {
		return recs, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("corrupt %s: %w", wiringRecordPath(), err)
	}
	return recs, nil
}

func saveWiringRecords(recs map[string]wiringRecord) error {
	if err := os.MkdirAll(config.StateDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(wiringRecordPath(), append(b, '\n'), 0o600)
}

func recordKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func recordWiring(path string, rec wiringRecord) error {
	recs, err := loadWiringRecords()
	if err != nil {
		return err
	}
	recs[recordKey(path)] = rec
	return saveWiringRecords(recs)
}

// unwireFile removes ccauth's wiring from a settings file and keeps everything
// else. With a record from `wire`, it removes exactly the keys ccauth wrote, and
// only while they still hold the value ccauth wrote. Without one (wiring from
// an older ccauth), it removes a ccauth apiKeyHelper plus the env values that
// the helper's profile would produce. An emptied project file is deleted.
// It returns the removed keys and notes about anything left in place.
func unwireFile(path string, isProject bool) (removed, notes []string, err error) {
	settings, err := readSettings(path)
	if err != nil {
		return nil, nil, err
	}
	recs, err := loadWiringRecords()
	if err != nil {
		return nil, nil, err
	}
	rec, recorded := recs[recordKey(path)]
	helper, _ := settings["apiKeyHelper"].(string)
	if !recorded {
		if !strings.Contains(helper, "ccauth") {
			return nil, nil, nil // nothing of ours here
		}
		rec = legacyRecord(helper)
	}

	switch {
	case helper == "":
	case helper == rec.APIKeyHelper || (!recorded && strings.Contains(helper, "ccauth")):
		delete(settings, "apiKeyHelper")
		removed = append(removed, "apiKeyHelper")
	default:
		notes = append(notes, fmt.Sprintf("apiKeyHelper changed since ccauth wrote it (%q); left in place", helper))
	}
	if env, ok := settings["env"].(map[string]any); ok {
		keys := make([]string, 0, len(rec.Env))
		for k := range rec.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cur, set := env[k]
			switch {
			case !set:
			case cur == rec.Env[k]:
				delete(env, k)
				removed = append(removed, "env."+k)
			default:
				notes = append(notes, fmt.Sprintf("env.%s changed since ccauth wrote it; left in place", k))
			}
		}
		if len(env) == 0 {
			delete(settings, "env")
		}
	}
	// Put back what wire moved aside, unless the user has set it again since.
	if len(rec.Displaced) > 0 {
		env, _ := settings["env"].(map[string]any)
		if env == nil {
			env = map[string]any{}
		}
		keys := make([]string, 0, len(rec.Displaced))
		for k := range rec.Displaced {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if _, set := env[k]; set {
				notes = append(notes, fmt.Sprintf("env.%s was set again since ccauth moved it aside; kept the current value", k))
				continue
			}
			env[k] = rec.Displaced[k]
			removed = append(removed, "restored env."+k)
		}
		settings["env"] = env
	}
	if !recorded && len(rec.Env) == 0 {
		notes = append(notes, "no record of what ccauth wrote here and the helper's profile could not be resolved; check env for ANTHROPIC_BASE_URL and similar keys yourself")
	}

	if len(removed) > 0 {
		if isProject && len(settings) == 0 {
			if err := os.Remove(path); err != nil {
				return nil, nil, err
			}
		} else if err := writeSettings(path, settings); err != nil {
			return nil, nil, err
		}
	}
	if recorded {
		delete(recs, recordKey(path))
		if err := saveWiringRecords(recs); err != nil {
			notes = append(notes, fmt.Sprintf("could not update %s: %v", wiringRecordPath(), err))
		}
	}
	return removed, notes, nil
}

// legacyRecord reconstructs what an older ccauth wrote, from the profile named
// in the helper command; env is empty when the profile can't be resolved.
func legacyRecord(helper string) wiringRecord {
	rec := wiringRecord{APIKeyHelper: helper}
	c, err := config.Load()
	if err != nil {
		return rec
	}
	name := c.ProfileName(helperProfile(helper))
	prof, err := c.Resolve(name)
	if err != nil {
		return rec
	}
	rec.Env = buildEnv(prof)
	return rec
}

// helperProfile returns the --profile/-p value of an apiKeyHelper command, or "".
func helperProfile(cmd string) string {
	f := strings.Fields(cmd)
	for i, a := range f {
		switch {
		case (a == "--profile" || a == "-p") && i+1 < len(f):
			return strings.Trim(f[i+1], `"'`)
		case strings.HasPrefix(a, "--profile="):
			return strings.Trim(strings.TrimPrefix(a, "--profile="), `"'`)
		}
	}
	return ""
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
