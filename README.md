<p align="center">
  <img src="docs/assets/claude-logo.svg" width="76" alt="Claude" />
</p>

<h1 align="center">ccauth</h1>

<p align="center"><strong>Bring your own SSO to Claude Code.</strong></p>

<p align="center">
  An OAuth2 / SSO credential helper that logs <a href="https://code.claude.com">Claude Code</a> into any AI gateway
  using your corporate identity —<br/>
  <b>Microsoft Entra ID</b>, <b>Google Workspace</b>, or any <b>OIDC</b> provider. No hand-managed API keys.
</p>

<p align="center">
  <a href="https://github.com/PalenaAI/claude-code-auth-helper/releases"><img src="https://img.shields.io/github/v/release/PalenaAI/claude-code-auth-helper?include_prereleases&sort=semver&label=release" alt="Release" /></a>
  <a href="https://github.com/PalenaAI/claude-code-auth-helper/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/PalenaAI/claude-code-auth-helper/ci.yml?branch=main&label=ci" alt="CI" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/PalenaAI/claude-code-auth-helper?color=blue" alt="License" /></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/PalenaAI/claude-code-auth-helper" alt="Go version" /></a>
  <img src="https://img.shields.io/badge/for-Claude%20Code-D97757?logo=claude&logoColor=white" alt="For Claude Code" />
  <img src="https://img.shields.io/badge/SSO-Entra%20%7C%20Google%20%7C%20OIDC-2ea44f" alt="SSO providers" />
  <img src="https://img.shields.io/badge/gateways-LiteLLM%20%7C%20Portkey%20%7C%20Bifrost-5f87af" alt="Gateways" />
</p>

---

## Why ccauth?

When a company puts an **AI gateway** in front of Claude Code, it wants the credential to be the developer's **SSO identity** — not a shared, long-lived API key copied into a dotfile. But Claude Code speaks Anthropic-style keys, while an SSO-fronted gateway wants a short-lived **OAuth bearer JWT**.

`ccauth` bridges that gap. The developer signs in once with their company account; from then on Claude Code silently gets fresh, short-lived tokens through its `apiKeyHelper` hook. Access follows the IdP lifecycle (offboarding, Conditional Access, MFA), the gateway attributes usage to a real person, and **no one generates or pastes an API key**.

```text
  you ── ccauth login ─▶  browser SSO  ─▶  refresh token stored in your OS keychain
                                                │
  Claude Code ── apiKeyHelper ─▶  ccauth token ─┤─▶  fresh short-lived token
                                                │      · silent refresh
                                                │      · auto browser re-auth on expiry
                                                ▼
                     Authorization: Bearer <jwt>  +  x-api-key: <jwt>
                                                │
                                                ▼
                    AI gateway  (validates the SSO JWT via JWKS)  ─▶  Claude
```

## Highlights

- **Any gateway** — LiteLLM, Portkey, Bifrost, or any OIDC-aware gateway, via one tool.
- **Any SSO** — Entra ID, Google Workspace, or generic OIDC (Okta/Auth0/Keycloak).
- **Two credential modes** — *passthrough* (emit the raw SSO JWT for the gateway to validate) or *exchange* (trade it for a gateway-native key via RFC 8693 or a broker).
- **Invisible refresh** — the `apiKeyHelper` returns cached tokens instantly, refreshes silently, and **opens a browser itself** when the session finally expires — you never leave Claude Code.
- **Zero-config for users** — IT provisions everything centrally (MDM / remote config / embedded); developers run only `ccauth login`. See [ENTERPRISE.md](ENTERPRISE.md).
- **Secure by construction** — public-client + PKCE, refresh token in the OS keychain, tokens never logged.

> Deep dive: architecture, the gateway compatibility matrix, and the research behind each decision live in **[DESIGN.md](DESIGN.md)**.

## Install

```bash
# from source (Go 1.25+)
git clone https://github.com/PalenaAI/claude-code-auth-helper.git
cd claude-code-auth-helper
make install         # builds ./ccauth into $GOBIN  (or `make build` for ./ccauth)
```

Prebuilt, checksummed static binaries for macOS/Linux/Windows: `make package` → `dist/`.

## Quick start

```bash
ccauth setup --write     # pick provider + gateway; writes config + Claude Code wiring
ccauth login             # sign in (browser). Use --device for headless/SSH.
ccauth status            # confirm you're logged in and see token expiry
# start Claude Code — it now authenticates through your SSO
```

Prefer to hand-edit? `ccauth init` writes a fully commented config; `ccauth wire` prints the exact `settings.json` block.

## Commands

| Command | What it does |
|---|---|
| `ccauth setup [--write]` | Interactive wizard: choose provider + gateway, write the profile (and optionally the Claude Code wiring) |
| `ccauth init [--force]` | Write a commented example `config.toml` to edit by hand |
| `ccauth login [--device]` | Interactive SSO sign-in; stores the refresh token in your OS keychain |
| `ccauth token` | Print the current credential to stdout — **this is your `apiKeyHelper`**. Silent refresh; auto browser re-auth on expiry |
| `ccauth status [--json]` | Show the active profile, config source, login state, and token expiry |
| `ccauth wire [--write]` | Print (or merge) the `settings.json` wiring for a profile |
| `ccauth config path\|show\|sync` | Inspect the config layers (user/embedded/managed/remote) or pull remote config |
| `ccauth doctor [--probe]` | Diagnose config + session and print the gateway-side checklist |
| `ccauth logout` | Delete the stored session for a profile |
| `ccauth gateways` | List supported gateways and how each authenticates |

All commands accept `-p/--profile <name>` (default: the config's `default_profile`).

## Gateway support

Run `ccauth gateways` for the live table.

| Gateway | Native inbound JWT? | Mode | Notes |
|---|---|---|---|
| **LiteLLM** (Enterprise) | ✅ `enable_jwt_auth` (+ OAuth2 introspection) | passthrough | Set `JWT_AUDIENCE` + `JWT_ISSUER` — unset = checks silently off. [example](examples/litellm) |
| **Portkey** (Enterprise) | ✅ JWKS, RS256, org mapping | passthrough | Base URL `https://api.portkey.ai` (no `/v1`). [example](examples/portkey) |
| **Bifrost** | ❌ virtual-key only | exchange | Broker → `sk-bf-*`, or the edge-auth [plugin](examples/bifrost-plugin) |
| **Generic OIDC gateway** | ✅ (you configure JWKS) | passthrough | Envoy / APISIX / Kong / custom |

## Configuration

Config lives at `~/.config/ccauth/config.toml` (override with `CCAUTH_CONFIG_DIR`), one `[profiles.<name>]` per gateway+SSO combination. Minimal Entra → LiteLLM:

```toml
default_profile = "work"

[profiles.work]
provider = "entra"
gateway  = "litellm"
mode     = "passthrough"

  [profiles.work.oauth]
  tenant_id = "<tenant-guid>"
  client_id = "<app-registration-client-id>"
  scopes    = ["api://<gateway-app-id>/.default"]
  flow      = "auth_code"          # or "device_code"

  [profiles.work.gateway_opts]
  base_url  = "http://localhost:4000"
  ttl_ms    = 3000000
```

`ccauth init` documents every provider/gateway combination. Env overrides for CI/headless: `CCAUTH_TENANT_ID`, `CCAUTH_CLIENT_ID`, `CCAUTH_CLIENT_SECRET`, `CCAUTH_ISSUER`, `CCAUTH_BASE_URL`.

## Enterprise / IT deployment

Users shouldn't type tenant/client IDs or run a wizard. IT provisions everything centrally and the developer runs only `ccauth login` (or nothing — the helper opens the browser when needed). Config is layered (`user < embedded < managed < remote`), distributable via **MDM managed file**, **remote config URL**, or a **compile-time embedded** branded binary, with an optional `allow_user_profiles = false` lockdown.

> Full guide: **[ENTERPRISE.md](ENTERPRISE.md)**.

```bash
ccauth config path     # which layers are active on this machine
ccauth config show     # merged config + per-profile source (secrets redacted)
```

## Security

- Public client + **PKCE** + loopback redirect (no client secret on disk for Entra/OIDC public clients).
- Refresh token in the **OS keychain**, with a `0600`-file fallback for headless environments.
- Only short-lived tokens are emitted; ccauth **never logs tokens**.
- The gateway verifies signature + `iss` + `aud` + `exp` + scopes; ccauth reads token expiry unverified only to time refreshes.
- Report vulnerabilities per **[SECURITY.md](SECURITY.md)** — not via public issues.

## Troubleshooting

| Symptom | Likely cause / fix |
|---|---|
| `no valid session … run ccauth login` | Not logged in for this profile, and auto-reauth is disabled/unavailable (headless). Run `ccauth login -p <name>`. |
| Gateway returns `401` | Wrong mode, or the gateway isn't validating your IdP. Run `ccauth doctor` and check the gateway-side checklist (`JWT_AUDIENCE`/`JWT_ISSUER` on LiteLLM, JWKS registration on Portkey). |
| `OIDC discovery … failed` | Bad issuer/tenant. Entra needs a **tenant GUID or verified domain**, not `common`. |
| Google re-prompts weekly | Publish the OAuth consent screen — "Testing" apps get 7-day refresh tokens. |
| Device flow blocked (Entra) | Conditional Access may block device-code. Use `flow = "auth_code"`. |

Run `ccauth doctor --probe` to exercise the whole path end-to-end (credential redacted).

## Development

```bash
make build          # ./ccauth
make test           # unit tests (race)
make vet
make license-check  # verify Apache headers
make package        # dist/ cross-compiled binaries + LICENSE/NOTICE + SHA256SUMS
```

Contributions welcome — see **[CONTRIBUTING.md](CONTRIBUTING.md)** (CLA + DCO sign-off required).

## License

Licensed under the **Apache License, Version 2.0** — Copyright 2026 bitkaio LLC
(<https://bitkaio.com>). See [LICENSE](LICENSE) and [NOTICE](NOTICE).

The Claude mark is a trademark of Anthropic PBC, used nominatively to indicate
compatibility. Not affiliated with Anthropic, LiteLLM, Portkey, or Bifrost.
