// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package desktop

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ExportJSON renders the flat JSON configuration file that Claude Desktop loads
// via Developer → Configure Third-Party Inference… → Import configuration (also
// a bootstrap-server response body). Values keep their native JSON types.
func ExportJSON(settings map[string]any) ([]byte, error) {
	return marshal(settings, "  ")
}

// marshal encodes JSON without HTML escaping, so values such as "a&b" stay
// readable in exported files and policy strings.
func marshal(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// policyString encodes a value the way the OS preference stores need it: every
// value is a string, booleans "true"/"false", objects and arrays as JSON text.
func policyString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	default:
		b, err := marshal(t, "")
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(string(b), "\n"), nil
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ExportMobileconfig renders an Apple configuration profile that sets the
// com.anthropic.claudefordesktop preference domain, for Jamf or any Apple MDM.
// ids supplies the payload UUIDs (stable across re-exports when reused).
func ExportMobileconfig(profile string, settings map[string]any, profileUUID, payloadUUID string) ([]byte, error) {
	var b strings.Builder
	esc := func(s string) string {
		var w strings.Builder
		_ = xml.EscapeText(&w, []byte(s))
		return w.String()
	}
	kv := func(indent, k, v string) {
		fmt.Fprintf(&b, "%s<key>%s</key>\n%s<string>%s</string>\n", indent, esc(k), indent, esc(v))
	}
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>PayloadContent</key>
  <array>
    <dict>
`)
	in := "      "
	kv(in, "PayloadType", "com.anthropic.claudefordesktop")
	kv(in, "PayloadIdentifier", "com.anthropic.claudefordesktop.ccauth."+profile)
	kv(in, "PayloadUUID", payloadUUID)
	fmt.Fprintf(&b, "%s<key>PayloadVersion</key>\n%s<integer>1</integer>\n", in, in)
	kv(in, "PayloadDisplayName", "Claude Desktop gateway ("+profile+")")
	for _, k := range sortedKeys(settings) {
		v, err := policyString(settings[k])
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", k, err)
		}
		kv(in, k, v)
	}
	b.WriteString("    </dict>\n  </array>\n")
	in = "  "
	kv(in, "PayloadType", "Configuration")
	kv(in, "PayloadIdentifier", "com.anthropic.claudefordesktop.ccauth."+profile+".profile")
	kv(in, "PayloadUUID", profileUUID)
	fmt.Fprintf(&b, "%s<key>PayloadVersion</key>\n%s<integer>1</integer>\n", in, in)
	kv(in, "PayloadDisplayName", "Claude Desktop gateway ("+profile+")")
	kv(in, "PayloadScope", "User")
	b.WriteString("</dict>\n</plist>\n")
	return []byte(b.String()), nil
}

// ExportReg renders a .reg file with REG_SZ values directly under
// HKCU (or HKLM with machine) \SOFTWARE\Policies\Claude; Desktop never reads
// subkeys.
func ExportReg(settings map[string]any, machine bool) ([]byte, error) {
	hive := "HKEY_CURRENT_USER"
	if machine {
		hive = "HKEY_LOCAL_MACHINE"
	}
	var b strings.Builder
	b.WriteString("Windows Registry Editor Version 5.00\r\n\r\n")
	fmt.Fprintf(&b, "[%s\\SOFTWARE\\Policies\\Claude]\r\n", hive)
	q := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	for _, k := range sortedKeys(settings) {
		v, err := policyString(settings[k])
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", k, err)
		}
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("%s: value contains a line break", k)
		}
		fmt.Fprintf(&b, "\"%s\"=\"%s\"\r\n", q.Replace(k), q.Replace(v))
	}
	return []byte(b.String()), nil
}

// NewUUID returns a random upper-case UUID for configuration profile payloads.
func NewUUID() (string, error) {
	id, err := newID()
	return strings.ToUpper(id), err
}
