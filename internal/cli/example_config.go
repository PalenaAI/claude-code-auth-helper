// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 bitkaio LLC

package cli

// exampleConfig is written by `ccauth init`. It documents every field and shows
// one worked profile per provider/gateway combination. Uncomment and edit the
// profile you need, then set default_profile.
const exampleConfig = `# ccauth configuration
# Location: ~/.config/ccauth/config.toml   (override dir with CCAUTH_CONFIG_DIR)
#
# A "profile" binds an identity provider + a gateway + a credential mode.
# Point Claude Code at a profile with:  "apiKeyHelper": "ccauth token --profile <name>"

default_profile = "entra-litellm"

# ---------------------------------------------------------------------------
# Microsoft Entra ID  ->  LiteLLM (native JWT auth, passthrough)
# ---------------------------------------------------------------------------
[profiles.entra-litellm]
provider = "entra"
gateway  = "litellm"
mode     = "passthrough"   # emit the raw IdP token; LiteLLM validates it via JWKS
# store  = "auto"          # auto | keyring | file

  [profiles.entra-litellm.oauth]
  tenant_id       = "00000000-0000-0000-0000-000000000000"  # your tenant GUID
  client_id       = "11111111-1111-1111-1111-111111111111"  # your CLI app registration
  scopes          = ["api://<gateway-app-id>/.default"]      # audience the gateway checks
  flow            = "auth_code"        # auth_code (browser) | device_code (headless)
  # redirect_host = "localhost"        # Entra loopback must be registered as http://localhost
  # credential_token = "access"        # Entra API tokens are access tokens

  [profiles.entra-litellm.gateway_opts]
  base_url = "http://localhost:4000"   # proxy root; Claude Code appends /v1/messages
  ttl_ms   = 3000000                   # CLAUDE_CODE_API_KEY_HELPER_TTL_MS (~50 min)

# ---------------------------------------------------------------------------
# Google Workspace  ->  Portkey (Enterprise JWT auth, passthrough)
# ---------------------------------------------------------------------------
# [profiles.google-portkey]
# provider = "google"
# gateway  = "portkey"
# mode     = "passthrough"
#
#   [profiles.google-portkey.oauth]
#   client_id        = "xxxx.apps.googleusercontent.com"   # Desktop-app OAuth client
#   client_secret    = "GOCSPX-..."                        # Desktop-app client secret
#   scopes           = ["openid", "email", "profile"]
#   flow             = "auth_code"
#   credential_token = "id"     # Google access tokens are opaque; emit the ID token
#
#   [profiles.google-portkey.gateway_opts]
#   base_url = "https://api.portkey.ai"    # no trailing /v1
#   ttl_ms   = 3000000
#     [profiles.google-portkey.gateway_opts.headers]
#     "x-portkey-provider" = "@anthropic-prod"   # Model Catalog slug holding the real key

# ---------------------------------------------------------------------------
# Generic OIDC (Okta/Auth0/Keycloak)  ->  Bifrost (no native JWT; exchange mode)
# ---------------------------------------------------------------------------
# [profiles.okta-bifrost]
# provider = "oidc"
# gateway  = "bifrost"
# mode     = "exchange"     # trade the IdP token for a Bifrost virtual key
#
#   [profiles.okta-bifrost.oauth]
#   issuer           = "https://your-org.okta.com"
#   client_id        = "0oaXXXXXXXX"
#   scopes           = ["openid", "profile", "email", "offline_access"]
#   flow             = "auth_code"
#   credential_token = "access"
#
#   [profiles.okta-bifrost.gateway_opts]
#   base_url = "http://localhost:8080/anthropic"
#   ttl_ms   = 240000
#
#   [profiles.okta-bifrost.exchange]
#   style              = "rfc8693"   # rfc8693 | broker
#   token_url          = "https://broker.internal/token"
#   audience           = "bifrost"
#   subject_token_type = "access"
#   # broker-style only:
#   # key_field    = "key"
#   # expiry_field = "expires_in"
`
