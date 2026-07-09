// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isolateLayers(t *testing.T) (userDir, managedPath, stateDir string) {
	t.Helper()
	root := t.TempDir()
	userDir = filepath.Join(root, "user")
	managedPath = filepath.Join(root, "managed.toml")
	stateDir = filepath.Join(root, "state")
	t.Setenv("CCAUTH_CONFIG_DIR", userDir)
	t.Setenv("CCAUTH_MANAGED_CONFIG", managedPath)
	t.Setenv("CCAUTH_STATE_DIR", stateDir)
	t.Setenv("CCAUTH_CONFIG_URL", "")
	return
}

func TestLayeredPrecedenceAndSources(t *testing.T) {
	userDir, managedPath, _ := isolateLayers(t)

	// Managed defines "corp" (→ litellm); user tries to redefine it (→ portkey)
	// and adds a personal "mine".
	writeFile(t, managedPath, `
[profiles.corp]
provider = "entra"
gateway  = "litellm"
  [profiles.corp.oauth]
  tenant_id = "managed-tenant"
  client_id = "managed-client"
  scopes    = ["api://x/.default"]
`)
	writeFile(t, filepath.Join(userDir, "config.toml"), `
default_profile = "corp"
[profiles.corp]
provider = "entra"
gateway  = "portkey"
  [profiles.corp.oauth]
  tenant_id = "user-tenant"
  client_id = "user-client"
  scopes    = ["api://y/.default"]
[profiles.mine]
provider = "google"
gateway  = "litellm"
  [profiles.mine.oauth]
  client_id = "g"
`)

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Sources["corp"] != "managed" {
		t.Errorf("corp source = %q, want managed (IT overrides user)", c.Sources["corp"])
	}
	if c.Profiles["corp"].Gateway != GatewayLiteLLM {
		t.Errorf("corp gateway = %q, want litellm (from managed)", c.Profiles["corp"].Gateway)
	}
	if c.Sources["mine"] != "user" {
		t.Errorf("mine source = %q, want user", c.Sources["mine"])
	}
}

func TestLockdownDropsUserProfiles(t *testing.T) {
	userDir, managedPath, _ := isolateLayers(t)
	writeFile(t, managedPath, `
allow_user_profiles = false
[profiles.corp]
provider = "entra"
gateway  = "litellm"
  [profiles.corp.oauth]
  tenant_id = "t"
  client_id = "c"
  scopes    = ["s"]
`)
	writeFile(t, filepath.Join(userDir, "config.toml"), `
[profiles.mine]
provider = "google"
gateway  = "litellm"
  [profiles.mine.oauth]
  client_id = "g"
`)

	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Profiles["mine"]; ok {
		t.Error("user profile 'mine' should be dropped under allow_user_profiles=false")
	}
	if _, ok := c.Profiles["corp"]; !ok {
		t.Error("managed profile 'corp' should remain")
	}
}

func TestInteractiveHelperDefaultAndOverride(t *testing.T) {
	p, err := base(Profile{
		Provider: ProviderEntra, Gateway: GatewayLiteLLM,
		OAuth: OAuth{TenantID: "t", ClientID: "c", Scopes: []string{"s"}},
	}).Resolve("x")
	if err != nil {
		t.Fatal(err)
	}
	if !p.InteractiveHelper() {
		t.Error("InteractiveHelper should default to true")
	}

	no := false
	p2, err := base(Profile{
		Provider: ProviderEntra, Gateway: GatewayLiteLLM, HelperInteractive: &no,
		OAuth: OAuth{TenantID: "t", ClientID: "c", Scopes: []string{"s"}},
	}).Resolve("x")
	if err != nil {
		t.Fatal(err)
	}
	if p2.InteractiveHelper() {
		t.Error("InteractiveHelper should honor explicit false")
	}
}
