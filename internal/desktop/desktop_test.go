// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package desktop

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PalenaAI/claude-code-auth-helper/internal/config"
	"github.com/PalenaAI/claude-code-auth-helper/internal/gateway"
)

const cid = "46ad0f8f-f5e7-457f-841a-af0cbe689a04"

func entraProfile() config.Profile {
	return config.Profile{
		Provider: config.ProviderEntra,
		Mode:     config.ModePassthrough,
		OAuth: config.OAuth{TenantID: "tid", ClientID: cid, Scopes: []string{cid + "/.default"},
			Credential: config.TokenAccess, RedirectHost: "localhost"},
		GatewayOpts: config.GatewayOpts{BaseURL: "https://gw.example.com", Headers: map[string]string{"x-team": "platform"}},
	}
}

func TestResolveAuth(t *testing.T) {
	google := config.Profile{Provider: config.ProviderGoogle, Mode: config.ModePassthrough}
	exchange := entraProfile()
	exchange.Mode = config.ModeExchange
	tests := []struct {
		name, req string
		prof      config.Profile
		want      Auth
		wantErr   bool
	}{
		{"entra auto → sso", "auto", entraProfile(), AuthSSO, false},
		{"google auto → helper", "", google, AuthHelper, false},
		{"exchange auto → helper", "auto", exchange, AuthHelper, false},
		{"explicit helper", "helper", entraProfile(), AuthHelper, false},
		{"sso refused for google", "sso", google, "", true},
		{"sso refused for exchange", "sso", exchange, "", true},
		{"unknown", "magic", entraProfile(), "", true},
	}
	for _, tc := range tests {
		got, err := ResolveAuth(tc.req, tc.prof)
		if (err != nil) != tc.wantErr || got != tc.want {
			t.Errorf("%s: got %q, %v", tc.name, got, err)
		}
	}
}

func TestSettingsSSOForEntra(t *testing.T) {
	s, err := Settings("work", entraProfile(), Options{Auth: AuthSSO, Extra: []gateway.Header{{Name: "x-cost-center", Value: "42"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"inferenceProvider":       "gateway",
		"inferenceGatewayBaseUrl": "https://gw.example.com",
		"inferenceCredentialKind": "external-idp",
		"inferenceCustomHeaders":  map[string]string{"x-team": "platform", "x-cost-center": "42"},
		"inferenceIdpOidc": map[string]any{
			"clientId":        cid,
			"issuer":          "https://login.microsoftonline.com/tid/v2.0",
			"bearerTokenType": "access_token",
			"scopes":          cid + "/.default",
			"redirectHost":    "localhost",
		},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("Settings =\n%#v\nwant\n%#v", s, want)
	}
}

func TestSettingsSSOBrokerAndPort(t *testing.T) {
	p := entraProfile()
	p.OAuth.RedirectPort = 8765
	s, err := Settings("work", p, Options{Auth: AuthSSO, Broker: true})
	if err != nil {
		t.Fatal(err)
	}
	if s["inferenceIdpAuthFlow"] != "broker" || s["inferenceIdpOidc"].(map[string]any)["redirectPort"] != 8765 {
		t.Errorf("broker/port not set: %v", s)
	}
	oidc := config.Profile{Provider: config.ProviderOIDC, OAuth: config.OAuth{Issuer: "https://idp", ClientID: "c"}, GatewayOpts: p.GatewayOpts}
	if _, err := Settings("x", oidc, Options{Auth: AuthSSO, Broker: true}); err == nil {
		t.Error("--broker must be refused for a non-Entra IdP")
	}
}

func TestSettingsSSOIDTokenAddsOfflineAccess(t *testing.T) {
	p := config.Profile{Provider: config.ProviderOIDC,
		OAuth:       config.OAuth{Issuer: "https://idp.example.com/", ClientID: "c", Scopes: []string{"openid", "email"}, Credential: config.TokenID},
		GatewayOpts: config.GatewayOpts{BaseURL: "https://gw"}}
	s, err := Settings("x", p, Options{Auth: AuthSSO})
	if err != nil {
		t.Fatal(err)
	}
	o := s["inferenceIdpOidc"].(map[string]any)
	if o["bearerTokenType"] != "id_token" || o["scopes"] != "openid email offline_access" || o["issuer"] != "https://idp.example.com" {
		t.Errorf("idp = %v", o)
	}
}

func TestSettingsHelper(t *testing.T) {
	s, err := Settings("work", entraProfile(), Options{Auth: AuthHelper, HelperPath: "/opt/homebrew/bin/ccauth", HelperTTL: 20 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if s["inferenceCredentialKind"] != "helper-script" || s["inferenceCredentialHelper"] != "/opt/homebrew/bin/ccauth" ||
		s["inferenceGatewayAuthScheme"] != "bearer" {
		t.Errorf("helper keys: %v", s)
	}
	if got := s["inferenceCredentialHelperArgs"]; !reflect.DeepEqual(got, []string{"token", "--profile", "work", "--min-lifetime", "20m0s"}) {
		t.Errorf("args = %v", got)
	}
	if s["inferenceCredentialHelperTtlSec"] != 1200 || s["inferenceCredentialHelperTimeoutSec"] != 240 {
		t.Errorf("ttl/timeout = %v / %v", s["inferenceCredentialHelperTtlSec"], s["inferenceCredentialHelperTimeoutSec"])
	}
	if _, set := s["inferenceIdpOidc"]; set {
		t.Error("helper mode must not set inferenceIdpOidc")
	}
	if _, err := Settings("work", entraProfile(), Options{Auth: AuthHelper, HelperPath: "ccauth"}); err == nil {
		t.Error("relative helper path must be refused")
	}
	if _, err := Settings(`we"ird`, entraProfile(), Options{Auth: AuthHelper, HelperPath: "/bin/ccauth"}); err == nil {
		t.Error("a profile name Desktop can't pass as an argument must be refused")
	}
}

func TestSettingsModels(t *testing.T) {
	p := entraProfile()
	p.GatewayOpts.Model, p.GatewayOpts.HaikuModel = "claude-sonnet", "claude-haiku"
	s, _ := Settings("w", p, Options{Auth: AuthSSO})
	if !reflect.DeepEqual(s["inferenceModels"], []string{"claude-sonnet", "claude-haiku"}) {
		t.Errorf("inferenceModels = %v", s["inferenceModels"])
	}
	p.GatewayOpts.ModelDiscovery = true
	s, _ = Settings("w", p, Options{Auth: AuthSSO})
	if _, set := s["inferenceModels"]; set {
		t.Error("with model discovery Desktop should list models from GET /v1/models")
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestApplyPersonalRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CCAUTH_DESKTOP_DIR", dir)
	lib := filepath.Join(dir, "configLibrary")
	// A library the app already wrote: a user's own entry plus a field ccauth
	// doesn't know about.
	foreign := "11111111-2222-4333-8444-555555555555"
	if err := os.MkdirAll(lib, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(lib, foreign+".json"), []byte(`{"inferenceProvider":"vertex"}`), 0o600)
	_ = os.WriteFile(filepath.Join(lib, "_meta.json"), []byte(`{"appliedId":"`+foreign+`","entries":[{"id":"`+foreign+`","name":"Mine"}],"hybridPointer":{"x":1}}`), 0o600)

	settings := map[string]any{"inferenceProvider": "gateway", "inferenceGatewayBaseUrl": "https://gw"}
	id, err := Apply("work", settings)
	if err != nil {
		t.Fatal(err)
	}
	if !idPattern.MatchString(id) {
		t.Fatalf("id %q is not a UUID", id)
	}
	m := readJSON(t, filepath.Join(lib, "_meta.json"))
	if m["appliedId"] != id || m["hybridPointer"] == nil || len(m["entries"].([]any)) != 2 {
		t.Fatalf("meta after apply = %v", m)
	}
	if got := readJSON(t, filepath.Join(lib, id+".json")); !reflect.DeepEqual(got, settings) {
		t.Errorf("entry = %v", got)
	}
	if fi, _ := os.Stat(filepath.Join(lib, id+".json")); fi.Mode().Perm() != 0o600 {
		t.Errorf("entry mode = %v", fi.Mode().Perm())
	}

	// Re-applying updates the same entry instead of adding one.
	id2, err := Apply("work", map[string]any{"inferenceProvider": "gateway", "inferenceGatewayBaseUrl": "https://gw2"})
	if err != nil || id2 != id {
		t.Fatalf("re-apply id = %q, %v; want %q", id2, err, id)
	}
	if n := len(readJSON(t, filepath.Join(lib, "_meta.json"))["entries"].([]any)); n != 2 {
		t.Errorf("re-apply added an entry: %d", n)
	}

	pid, err := Personal()
	if err != nil {
		t.Fatal(err)
	}
	st, err := ReadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if st.AppliedID != pid || !st.AppliedEmpty || st.AppliedName != PersonalEntryName {
		t.Errorf("status after personal = %+v", st)
	}
	// A second personal reuses the empty entry; the user's entry is untouched.
	if pid2, _ := Personal(); pid2 != pid {
		t.Errorf("personal created a second empty entry")
	}
	if got := readJSON(t, filepath.Join(lib, foreign+".json")); got["inferenceProvider"] != "vertex" {
		t.Errorf("foreign entry changed: %v", got)
	}

	if _, err := Apply("work", settings); err != nil {
		t.Fatal(err)
	}
	if st, _ := ReadStatus(); st.AppliedID != id || !st.AppliedByUs || st.Applied["inferenceGatewayBaseUrl"] != "https://gw" {
		t.Errorf("status after switching back = %+v", st)
	}
}

func TestApplyRefusesCorruptMeta(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CCAUTH_DESKTOP_DIR", dir)
	_ = os.MkdirAll(filepath.Join(dir, "configLibrary"), 0o700)
	_ = os.WriteFile(filepath.Join(dir, "configLibrary", "_meta.json"), []byte("{not json"), 0o600)
	if _, err := Apply("work", map[string]any{}); err == nil {
		t.Error("corrupt _meta.json must not be overwritten")
	}
}

var exportSettings = map[string]any{
	"inferenceProvider":                             "gateway",
	"inferenceGatewayBaseUrl":                       `https://gw.example.com/a"b`,
	"inferenceCustomHeaders":                        map[string]string{"x-team": "a&b"},
	"inferenceCredentialHelperArgs":                 []string{"token", "--profile", "work"},
	"inferenceCredentialHelperTtlSec":               1800,
	"inferenceCredentialHelperSilentRefreshEnabled": true,
}

func TestExportReg(t *testing.T) {
	b, err := ExportReg(exportSettings, false)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		"Windows Registry Editor Version 5.00\r\n",
		`[HKEY_CURRENT_USER\SOFTWARE\Policies\Claude]`,
		`"inferenceCredentialHelperTtlSec"="1800"`,
		`"inferenceCredentialHelperSilentRefreshEnabled"="true"`,
		`"inferenceCustomHeaders"="{\"x-team\":\"a&b\"}"`,
		`"inferenceGatewayBaseUrl"="https://gw.example.com/a\"b"`,
		`"inferenceCredentialHelperArgs"="[\"token\",\"--profile\",\"work\"]"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("reg missing %q:\n%s", want, s)
		}
	}
	if m, _ := ExportReg(exportSettings, true); !bytes.Contains(m, []byte(`HKEY_LOCAL_MACHINE\SOFTWARE\Policies\Claude`)) {
		t.Error("--machine should target HKLM")
	}
}

func TestExportMobileconfigIsWellFormedPlist(t *testing.T) {
	b, err := ExportMobileconfig("work", exportSettings, "AAAA", "BBBB")
	if err != nil {
		t.Fatal(err)
	}
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = true
	for {
		if _, err := d.Token(); err != nil {
			if err.Error() == "EOF" {
				break
			}
			t.Fatalf("not well-formed XML: %v\n%s", err, b)
		}
	}
	s := string(b)
	for _, want := range []string{
		"<string>com.anthropic.claudefordesktop</string>",
		"<key>inferenceCredentialHelperTtlSec</key>\n      <string>1800</string>",
		"<string>{&#34;x-team&#34;:&#34;a&amp;b&#34;}</string>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("mobileconfig missing %q:\n%s", want, s)
		}
	}
}

func TestExportJSONKeepsNativeTypes(t *testing.T) {
	b, err := ExportJSON(exportSettings)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["inferenceCredentialHelperTtlSec"] != float64(1800) || m["inferenceCredentialHelperSilentRefreshEnabled"] != true {
		t.Errorf("types not native: %v", m)
	}
}
