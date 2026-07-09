# Portkey + ccauth (passthrough)

Portkey Enterprise validates an external IdP JWT at the gateway edge. `ccauth`
emits the SSO JWT; Portkey checks it against your IdP's JWKS and routes to
Anthropic using a key stored in the Model Catalog.

## Gateway-side setup (one-time, admin)

1. **Register your IdP JWKS.** Admin Settings → Organisation → **Authentication**.
   Add your IdP's JWKS URL (or paste static JWKS JSON):
   - Entra ID: `https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys`
   - Google:   `https://www.googleapis.com/oauth2/v3/certs`
   - Generic:  the provider's `jwks_uri` from its OIDC discovery document.
2. **Tokens must be RS256** with a matching `kid` in the header. Portkey validates
   signature, `exp`, required claims, and `scope`.
3. **Choose the mapping mode:**
   - *Mode B* — a standard IdP token; Portkey maps it to an org/workspace/budget
     server-side (configure the mapping in Authentication).
   - *Mode A* — the token itself carries Portkey claims (`portkey_oid`,
     `portkey_workspace`, `scope`). Requires you to mint/augment tokens.
4. **Create a managed provider** in the Model Catalog holding your real Anthropic
   key, e.g. slug `@anthropic-prod`.

## ccauth profile

```toml
[profiles.portkey]
provider = "entra"          # or google / oidc
gateway  = "portkey"
mode     = "passthrough"

  [profiles.portkey.oauth]
  tenant_id = "<tenant-guid>"
  client_id = "<app-registration-client-id>"
  scopes    = ["api://<gateway-app-id>/.default"]
  flow      = "auth_code"

  [profiles.portkey.gateway_opts]
  base_url = "https://api.portkey.ai"      # NO trailing /v1 — Claude Code appends it
  ttl_ms   = 3000000
    [profiles.portkey.gateway_opts.headers]
    "x-portkey-provider" = "@anthropic-prod"
```

## Claude Code wiring (`ccauth wire`)

```json
{
  "apiKeyHelper": "ccauth token --profile portkey",
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.portkey.ai",
    "CLAUDE_CODE_API_KEY_HELPER_TTL_MS": "3000000",
    "ANTHROPIC_CUSTOM_HEADERS": "x-portkey-provider: @anthropic-prod"
  }
}
```

The SSO JWT rides in `Authorization: Bearer` (emitted by the helper). Portkey's
JWT auth reads it there. `x-portkey-provider` is a static routing header, so it
lives in `ANTHROPIC_CUSTOM_HEADERS`, not in the helper output.

> Note: JWT/SSO gateway auth is **Enterprise-only**. On free/OSS Portkey the only
> inbound credential is a Portkey API key — use `mode = exchange` with a broker
> that returns a Portkey key, or a static key, instead.
