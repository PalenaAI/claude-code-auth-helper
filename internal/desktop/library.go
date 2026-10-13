// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package desktop

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// The per-user configuration library Claude Desktop's "Configure Third-Party
// Inference" window writes (and "Reveal in Finder" opens):
//
//	<Claude-3p>/configLibrary/_meta.json  {"appliedId": "<uuid>", "entries": [{"id", "name", "provider"?, "note"?}], …}
//	<Claude-3p>/configLibrary/<uuid>.json {flat configuration keys}
//
// Desktop applies the entry named by appliedId at launch, unless an MDM profile
// or registry policy is present, which takes precedence over the library.
// ccauth only ever writes its own entries and the appliedId pointer, and keeps
// every other field Desktop stores in _meta.json.

var idPattern = regexp.MustCompile(`^[a-f0-9-]{36}$`)

// Dir is Claude Desktop's 3P data directory (the parent of configLibrary).
// CCAUTH_DESKTOP_DIR overrides it, for tests and sandboxes.
func Dir() string {
	if d := os.Getenv("CCAUTH_DESKTOP_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude-3p")
	case "windows":
		if la := os.Getenv("LOCALAPPDATA"); la != "" {
			return filepath.Join(la, "Claude-3p")
		}
		return filepath.Join(home, "AppData", "Local", "Claude-3p")
	default:
		if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, "Claude-3p")
		}
		return filepath.Join(home, ".config", "Claude-3p")
	}
}

func libraryDir() string              { return filepath.Join(Dir(), "configLibrary") }
func metaPath() string                { return filepath.Join(libraryDir(), "_meta.json") }
func entryPath(id string) string      { return filepath.Join(libraryDir(), id+".json") }
func entryName(profile string) string { return "ccauth: " + profile }

// PersonalEntryName names the empty configuration `desktop personal` applies.
const PersonalEntryName = "Claude.ai sign-in (ccauth)"

// meta is _meta.json, kept as a generic map so fields ccauth doesn't know about
// survive a rewrite.
type meta map[string]any

func loadMeta() (meta, error) {
	b, err := os.ReadFile(metaPath())
	if os.IsNotExist(err) {
		return meta{"appliedId": "", "entries": []any{}}, nil
	}
	if err != nil {
		return nil, err
	}
	m := meta{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON (%v); fix or remove it in Claude Desktop's configuration window first", metaPath(), err)
	}
	if _, ok := m["entries"].([]any); !ok {
		if m["entries"] != nil {
			return nil, fmt.Errorf("%s has an unexpected format (entries is not a list); leaving it alone", metaPath())
		}
		m["entries"] = []any{}
	}
	return m, nil
}

func (m meta) entries() []map[string]any {
	var out []map[string]any
	for _, e := range m["entries"].([]any) {
		if em, ok := e.(map[string]any); ok {
			out = append(out, em)
		}
	}
	return out
}

func (m meta) find(pred func(map[string]any) bool) map[string]any {
	for _, e := range m.entries() {
		if pred(e) {
			return e
		}
	}
	return nil
}

func (m meta) applied() string {
	s, _ := m["appliedId"].(string)
	return s
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".ccauth.tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// Apply writes the configuration as the profile's library entry (creating it,
// or updating the one ccauth made before) and makes it the applied one.
func Apply(profile string, settings map[string]any) (id string, err error) {
	m, err := loadMeta()
	if err != nil {
		return "", err
	}
	name := entryName(profile)
	e := m.find(func(e map[string]any) bool { return e["name"] == name })
	if e == nil {
		if id, err = newID(); err != nil {
			return "", err
		}
		e = map[string]any{"id": id}
		m["entries"] = append(m["entries"].([]any), e)
	} else if id, _ = e["id"].(string); !idPattern.MatchString(id) {
		return "", fmt.Errorf("library entry %q has an invalid id %q; delete it in Claude Desktop's configuration window", name, id)
	}
	e["name"] = name
	e["provider"] = "gateway"
	e["note"] = fmt.Sprintf("Written by `ccauth desktop apply -p %s`; edits here are overwritten on the next apply.", profile)
	if err := writeJSON(entryPath(id), settings); err != nil {
		return "", err
	}
	m["appliedId"] = id
	return id, writeJSON(metaPath(), m)
}

// Personal applies an empty configuration, which leaves third-party mode so
// Claude Desktop starts with the normal Claude.ai sign-in. It reuses an empty
// entry if the library has one, and leaves every other entry in place.
func Personal() (id string, err error) {
	m, err := loadMeta()
	if err != nil {
		return "", err
	}
	e := m.find(func(e map[string]any) bool {
		eid, _ := e["id"].(string)
		return idPattern.MatchString(eid) && isEmptyEntry(eid)
	})
	if e == nil {
		if id, err = newID(); err != nil {
			return "", err
		}
		if err := writeJSON(entryPath(id), map[string]any{}); err != nil {
			return "", err
		}
		m["entries"] = append(m["entries"].([]any), map[string]any{
			"id": id, "name": PersonalEntryName,
			"note": "Empty configuration applied by `ccauth desktop personal`: Claude Desktop signs in to Claude.ai.",
		})
	} else {
		id, _ = e["id"].(string)
	}
	m["appliedId"] = id
	return id, writeJSON(metaPath(), m)
}

func isEmptyEntry(id string) bool {
	b, err := os.ReadFile(entryPath(id))
	if err != nil {
		return false
	}
	var v map[string]any
	return json.Unmarshal(b, &v) == nil && len(v) == 0
}

// Status describes the library for `desktop status` and doctor.
type Status struct {
	Dir          string
	AppliedID    string
	AppliedName  string
	AppliedByUs  bool           // the applied entry is one ccauth wrote
	AppliedEmpty bool           // the applied entry is empty (Claude.ai sign-in)
	Applied      map[string]any // the applied configuration, if readable
	Entries      []string       // entry names
	Managed      string         // non-empty: the managed policy that overrides the library
}

// ReadStatus inspects the library and managed policy without changing anything.
func ReadStatus() (Status, error) {
	st := Status{Dir: libraryDir(), Managed: ManagedSource()}
	m, err := loadMeta()
	if err != nil {
		return st, err
	}
	for _, e := range m.entries() {
		n, _ := e["name"].(string)
		st.Entries = append(st.Entries, n)
	}
	st.AppliedID = m.applied()
	if e := m.find(func(e map[string]any) bool { return e["id"] == st.AppliedID }); e != nil {
		st.AppliedName, _ = e["name"].(string)
		st.AppliedByUs = strings.HasPrefix(st.AppliedName, "ccauth: ")
	}
	if idPattern.MatchString(st.AppliedID) {
		if b, err := os.ReadFile(entryPath(st.AppliedID)); err == nil {
			_ = json.Unmarshal(b, &st.Applied)
			st.AppliedEmpty = len(st.Applied) == 0
		}
	}
	return st, nil
}

// ManagedSource reports an MDM profile or policy that overrides the local
// library ("" when none is found). Windows registry is checked with reg.exe.
func ManagedSource() string {
	switch runtime.GOOS {
	case "darwin":
		paths := []string{"/Library/Managed Preferences/com.anthropic.claudefordesktop.plist"}
		if u, err := user.Current(); err == nil {
			paths = append([]string{filepath.Join("/Library/Managed Preferences", u.Username, "com.anthropic.claudefordesktop.plist")}, paths...)
		}
		for _, p := range paths {
			if fileExists(p) {
				return p
			}
		}
	case "linux":
		if p := "/etc/claude-desktop/managed-settings.json"; fileExists(p) {
			return p
		}
	case "windows":
		for _, hive := range []string{`HKLM\SOFTWARE\Policies\Claude`, `HKCU\SOFTWARE\Policies\Claude`} {
			if out, err := exec.Command("reg", "query", hive).Output(); err == nil && strings.Contains(string(out), "REG_") {
				return hive
			}
		}
	}
	return ""
}

// Running reports whether Claude Desktop appears to be running. Desktop writes
// _meta.json itself, so ccauth only changes the library while it is closed.
func Running() bool {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("pgrep", "-x", "Claude").Run() == nil
	case "windows":
		out, err := exec.Command("tasklist", "/FI", "IMAGENAME eq claude.exe", "/NH").Output()
		return err == nil && strings.Contains(strings.ToLower(string(out)), "claude.exe")
	default:
		return exec.Command("pgrep", "-x", "claude-desktop").Run() == nil
	}
}
