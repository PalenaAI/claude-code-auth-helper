# ccauth — Design

A generic **OAuth2 / SSO credential helper** for Claude Code that authenticates a
developer against their corporate identity provider (Microsoft Entra ID, Google
Workspace, or any OIDC provider) and feeds a short-lived token to an AI gateway
(LiteLLM, Portkey, Bifrost, or a generic OIDC-aware gateway) through Claude
Code's `apiKeyHelper` hook — so nobody generates or pastes long-lived API keys.

This document is the "precise and proper overall picture": how the pieces fit,
why each decision was made, and what the gateway operator must configure.

---

## 1. The problem

Claude Code authenticates to a model endpoint with a credential. When an org puts
an **AI gateway** in front of the model, they want that credential to be the
developer's **SSO identity**, not a shared static key:

- keys don't get generated, pasted, or leaked into dotfiles;
- access follows the user's IdP lifecycle (offboarding, Conditional Access, MFA);
- the gateway can attribute usage, budgets, and rate limits to a real person.

The tension: Claude Code was built to send an Anthropic-style key, while a gateway
behind SSO wants a **short-lived OAuth Bearer JWT**. `ccauth` bridges that gap.

---

## 2. Two facts that make this work

### 2.1 `apiKeyHelper` sends its output in **both** auth headers

From the Anthropic docs (*Connect Claude Code to an LLM gateway → "How the
credential variable maps to a header"*):

> `ANTHROPIC_AUTH_TOKEN` in `Authorization: Bearer`, `ANTHROPIC_API_KEY` in
> `x-api-key`, and **`apiKeyHelper` in both**. … "The helper's value is sent in
> both the `Authorization` and `x-api-key` headers, so it works whichever header
> your gateway reads."

So an `apiKeyHelper` script is strictly the right mechanism: whatever the gateway
reads (`Authorization: Bearer` for JWT-validating gateways, `x-api-key` for
key-based ones), the token arrives. It also refreshes automatically (see §2.2).

### 2.2 The helper is re-invoked on a TTL **and** on HTTP 401

- Output is cached for **5 minutes** by default; override with
  `CLAUDE_CODE_API_KEY_HELPER_TTL_MS`.
- It is re-run when a request returns **HTTP 401** (the backstop for expiry).
- It must return in **< 10 s** (ideally < 2 s) or the UX degrades.

The < 10 s budget is the reason for the architecture in §3: the helper cannot run
an interactive browser flow. It must be a fast, cached, silent-refresh path.

---

## 3. Core architecture: `login` (interactive) vs `token` (silent)

Because the helper runs non-interactively and fast, `ccauth` splits into two
responsibilities — the same pattern used by `kubelogin`, `aws-vault`, and `gcloud`:

```
                 ┌──────────────────────────────────────────────┐
   human, once   │  ccauth login                                 │
   ───────────►  │   • browser (auth-code+PKCE+loopback) or       │
                 │     device-code flow                           │
                 │   • stores REFRESH TOKEN in OS keychain        │
                 └──────────────────────────────────────────────┘
                                     │  refresh token at rest
                                     ▼
                 ┌──────────────────────────────────────────────┐
 Claude Code,    │  ccauth token   (= apiKeyHelper)              │
 many times      │   • reads cache, silently refreshes near exp  │
   ───────────►  │   • passthrough: emit the IdP JWT             │
                 │   • exchange:   IdP JWT → gateway key → emit  │
                 │   • never opens a browser; exits non-zero if   │
                 │     login is required                          │
                 └──────────────────────────────────────────────┘
                                     │ stdout: credential
                                     ▼
              Authorization: Bearer <cred>   +   x-api-key: <cred>
                                     │
                                     ▼
                         AI gateway  →  Anthropic / model
```

### 3.1 Credential modes

Native inbound IdP-JWT validation is **not** universal, so the emitted credential
has two modes:

- **passthrough** — emit the raw IdP token; the gateway validates it against the
  IdP's JWKS. (LiteLLM/Portkey Enterprise, generic OIDC gateways.)
- **exchange** — trade the IdP token for a gateway-native credential (e.g. a
  Bifrost virtual key) via RFC 8693 token exchange or an org broker, then emit
  that. (Bifrost, or any non-JWT gateway tier.)

### 3.2 Interactive re-auth from the helper

`ccauth login` is not the only recovery path. When silent refresh fails (the
refresh token is expired/revoked), `ccauth token` — by default — **opens a
browser itself** and completes the loopback flow, so the user never leaves Claude
Code. This is safe within the helper contract because a browser flow needs no TTY
and writes nothing to stdout (all UI goes to the browser + stderr; only the final
token is printed).

Guardrails:
- **Silent first.** The browser only appears when the refresh token is truly gone
  (rare — Entra rolling RTs last ~90 days, Google published-app RTs until revoked).
- **One tab, not N.** A file lock (`store.AcquireLoginLock`) serializes concurrent
  helper invocations; the others wait and then read the freshly stored session.
- **Bounded.** A 3-minute timeout means a stalled login fails cleanly rather than
  hanging Claude Code.
- **Environment-aware.** Disabled when no browser is reachable (SSH w/o X11, no
  display), when `flow = device_code`, or when `helper_interactive = false` /
  `CCAUTH_NONINTERACTIVE=1` / `CI=1` — then it falls back to the "run `ccauth
  login`" message.

The signal `credential.ErrLoginRequired` is what distinguishes "needs interactive
login" from other errors; the `token` command escalates on it.

---

## 4. Gateway compatibility matrix

| Gateway | Native inbound IdP-JWT validation? | Recommended mode | How the token lands | Key gotcha |
|---|---|---|---|---|
| **LiteLLM** (Enterprise) | ✅ `enable_jwt_auth` (JWKS, aud/iss, claims→team/RBAC); also OAuth2 **introspection** for opaque tokens | passthrough | `Authorization: Bearer` (helper emits it — LiteLLM's 2nd-precedence header) | **Must set `JWT_AUDIENCE` + `JWT_ISSUER`** or those checks are silently disabled |
| **Portkey** (Enterprise) | ✅ JWT auth (JWKS, **RS256 only**, exp/scope, Mode A/B org mapping) | passthrough | `Authorization: Bearer` + `x-portkey-provider` (static, via `ANTHROPIC_CUSTOM_HEADERS`) | Base URL `https://api.portkey.ai` (no `/v1`); RS256 required |
| **Bifrost** | ❌ data-plane is virtual-key only; OIDC/SSO is **dashboard-login only** | exchange | helper emits the `sk-bf-*` virtual key | Base URL `…/anthropic`; set `enforce_auth_on_inference: true`. Alternative: a Go `HTTPTransportPreHook` plugin (see `examples/bifrost-plugin`) |
| **Generic OIDC gateway** | ✅ if you configure JWKS validation (Envoy/APISIX/Kong/custom) | passthrough | `Authorization: Bearer` | Exempt `/v1/messages` from WAF XSS body inspection |

**Header-precedence note (LiteLLM):** it checks `x-litellm-api-key` →
`Authorization` → `x-api-key`. The helper populates `Authorization` and
`x-api-key`; the JWT path reads `Authorization`, so passthrough works cleanly. If
you use a LiteLLM **virtual key** instead of a JWT, put it in `x-litellm-api-key`
via `ANTHROPIC_CUSTOM_HEADERS` to dodge the precedence entirely.

---

## 5. Per-identity-provider token choice

| Provider | Emit | Why | Flow | JWKS the gateway uses |
|---|---|---|---|---|
| **Entra ID** | **access token**, `aud = api://<gateway-app>` | Entra is designed to issue API-audience access tokens | auth-code+PKCE+loopback (device-code fallback); `offline_access` for refresh | `https://login.microsoftonline.com/{tenant}/discovery/v2.0/keys` |
| **Google Workspace** | **ID token** (not the access token) | Google **access tokens are opaque** — a gateway can't cryptographically validate them; the ID token is a real JWT (`aud` = your client ID) | installed-app auth-code+PKCE+loopback, scopes `openid email profile` | `https://www.googleapis.com/oauth2/v3/certs` |
| **Generic OIDC** | access **or** ID token (configurable) | depends on what the gateway validates | auth-code+PKCE+loopback or device-code | provider's `jwks_uri` from discovery |

**Google alternative:** rather than the ID-token dance, LiteLLM's OAuth2
**introspection** mode (`enable_oauth2_auth` + `OAUTH_TOKEN_INFO_ENDPOINT`) can
validate Google's opaque **access** token directly — set `credential_token = "access"`
and point the gateway at Google's tokeninfo endpoint.

**Gotchas:** Entra access tokens live ~60–90 min. Google refresh tokens for an
**unpublished ("Testing") consent screen expire after 7 days** — publish the
consent screen. Entra device-code flow may be blocked by Conditional Access.

---

## 6. Token lifecycle & freshness

`ccauth token` guarantees the credential it prints outlives Claude Code's own
cache window, so Claude never sends an already-expired token:

```
requiredLifetime = max(defaultSkew=2m,  CLAUDE_CODE_API_KEY_HELPER_TTL_MS + 2m)
```

The helper reads `CLAUDE_CODE_API_KEY_HELPER_TTL_MS` from its own environment
(Claude Code passes it through). On each call:

1. If the cached emit-credential has `> requiredLifetime` remaining → print it (no network).
2. Else refresh the IdP token via the stored refresh token (once), then:
   - passthrough → emit the chosen (access/ID) token;
   - exchange → call the exchange endpoint, cache the gateway key + its TTL, emit it.
3. If there is no refresh token / refresh fails → exit non-zero pointing at `ccauth login`.

HTTP 401 from the gateway is the backstop: Claude Code re-invokes the helper,
which refreshes.

---

## 7. Configuration model (how the user sets this up)

One TOML file, `~/.config/ccauth/config.toml` (dir overridable via
`CCAUTH_CONFIG_DIR`), organized into named **profiles**. A profile binds
`{provider, gateway, mode}` plus the provider-specific fields. **Presets** fill in
issuers, default scopes, default flow, and the sensible default mode per gateway,
so the user supplies only the minimum.

Three ways to configure, in order of convenience:

1. **`ccauth setup`** — interactive wizard: pick provider + gateway, answer for
   the fields that matter (Entra tenant/client IDs + API scope, Google client
   ID/secret, OIDC issuer, gateway URL), and it writes the profile **and** the
   Claude Code `settings.json` wiring (`--write` merges it automatically).
2. **`ccauth init`** — writes a fully commented example config to edit by hand.
3. **Env overrides** — `CCAUTH_TENANT_ID`, `CCAUTH_CLIENT_ID`,
   `CCAUTH_CLIENT_SECRET`, `CCAUTH_ISSUER`, `CCAUTH_BASE_URL` for CI/headless.

Minimum required fields per provider:

| Provider | Required |
|---|---|
| Entra | `tenant_id`, `client_id`, `scopes` (the API audience, e.g. `api://<app>/.default`) |
| Google | `client_id`, `client_secret` (Desktop-app OAuth client) |
| Generic OIDC | `issuer`, `client_id` (+ `client_secret` only for confidential clients) |

The Claude Code wiring a profile produces:

```json
{
  "apiKeyHelper": "ccauth token --profile <name>",
  "env": {
    "ANTHROPIC_BASE_URL": "<gateway base url>",
    "CLAUDE_CODE_API_KEY_HELPER_TTL_MS": "3000000",
    "ANTHROPIC_CUSTOM_HEADERS": "x-portkey-provider: @anthropic-prod"   // gateway-specific, static
  }
}
```

The rotating token flows **only** through the helper (→ both auth headers). Static
routing headers go in `ANTHROPIC_CUSTOM_HEADERS`.

### 7.1 Layered configuration (enterprise / zero-config for users)

For large orgs, no user should type a tenant/client ID. Config is merged from four
layers, higher overriding lower on same-named profiles:

```
user      ~/.config/ccauth/config.toml     (personal; lowest authority)
embedded  compiled into the binary          (branded self-contained build)
managed   /etc/ccauth/config.toml (MDM)      (IT-owned; overrides user)
remote    cached fetch of config_url         (central server; freshest)
```

- IT provisions a **managed** file (via Intune/Jamf/etc.), a **remote** `config_url`
  (rotate centrally; cached so the `token` hot path never hits the network), or an
  **embedded** config (rebuild for a self-contained binary). The end user runs only
  `ccauth login` — often not even that (see §3.2).
- `allow_user_profiles = false` in an IT layer **locks** the tool to provisioned
  profiles.
- The per-user **session** (refresh token) always lives in that user's keychain,
  independent of where config comes from.
- Note on secrecy: a tenant ID and a *public* client ID are **not secrets** (they
  appear in every OAuth request); the layering exists for UX and central control,
  not confidentiality. A distributed CLI is a public client and must use PKCE.

Inspect with `ccauth config path` / `ccauth config show` / `ccauth config sync`.
Full deployment guide: **[ENTERPRISE.md](ENTERPRISE.md)**.

---

## 8. Security model

- **Public client + PKCE + loopback** (RFC 8252): no client secret on the
  developer's machine for Entra/OIDC public clients. (Google Desktop-app clients
  ship a non-confidential "secret" by design.)
- **Refresh token at rest in the OS keychain** (macOS Keychain, Windows Credential
  Manager, Linux Secret Service) via go-keyring; automatic fallback to a **`0600`
  file** (atomic write) when no keychain is available (headless/CI).
- **Short-lived tokens** only in the emit path; the long-lived secret is the
  refresh token.
- **CSRF-protected** loopback (state parameter), 5-minute login timeout.
- **No token logging**; `doctor --probe` redacts.
- Gateway must validate **signature (JWKS) + `iss` + `aud` + `exp` + scopes/roles**.
  `ccauth` parses tokens **unverified** only to read `exp` for refresh timing —
  cryptographic verification is the gateway's job.
- **WAF caveat (from Anthropic docs):** corporate WAFs may block `/v1/messages`
  because Claude prompt bodies look like XSS; exempt that path from body inspection.

---

## 9. Package map

```
cmd/ccauth            entrypoint
internal/config       profiles, provider presets, validation, env overrides
internal/store        keychain + 0600-file session store
internal/oidc         OIDC discovery, auth-code+PKCE+loopback, device-code, refresh
internal/credential   passthrough + exchange emit, freshness/skew logic
internal/gateway      per-gateway presets, routing headers, operator checklist
internal/jwtutil      unverified claim/expiry reader
internal/cli          setup, init, login, token, logout, status, wire, doctor, gateways
examples/             LiteLLM config, Portkey notes, Bifrost plugin, broker contract
```

A single OIDC engine covers all three providers; provider quirks are small presets
(issuer templating, Google `access_type=offline` + `prompt=consent`, credential
token choice). This keeps the flows uniform and the provider-specific surface tiny.

---

## 10. Non-goals / limitations

- Not a gateway. `ccauth` is a client-side credential helper; the gateway still
  enforces auth.
- Native JWT validation is an **Enterprise / self-host-config** feature on
  LiteLLM and Portkey, and **absent** on Bifrost's data plane — hence exchange
  mode and the reference plugin.
- Claude Code's `apiKeyHelper` applies to the **CLI, VS Code extension, Agent SDK,
  and GitHub Actions** — **not** the Desktop app or cloud surfaces (Slack/web),
  which use different mechanisms.
