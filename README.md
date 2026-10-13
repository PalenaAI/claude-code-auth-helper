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
  <a href="https://github.com/PalenaAI/claude-code-auth-helper/releases"><img src="https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-informational" alt="Platforms" /></a>
  <a href="https://github.com/PalenaAI/claude-code-auth-helper/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/PalenaAI/claude-code-auth-helper/ci.yml?branch=main&label=ci" alt="CI" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg" alt="License: Apache 2.0" /></a>
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

**Homebrew** (macOS / Linux):

```bash
brew install PalenaAI/tap/ccauth
```

**From source** (Go 1.26+):

```bash
git clone https://github.com/PalenaAI/claude-code-auth-helper.git
cd claude-code-auth-helper
make install         # builds ./ccauth into $GOBIN  (or `make build` for ./ccauth)
```

Or download a prebuilt, checksummed binary from the
[releases page](https://github.com/PalenaAI/claude-code-auth-helper/releases).

## Quick start

```bash
ccauth setup             # pick provider + gateway, and where Claude Code should use it
ccauth login             # sign in (browser). Use --device for headless/SSH.
ccauth claude            # start Claude Code on the gateway (or plain `claude`, if you wired it)
```

`ccauth setup` is an interactive form (arrow keys, inline validation) that ends with a summary of what to register in your identity provider and the next command to run. `--accessible` (or `ACCESSIBLE=1`) switches it to plain numbered prompts for screen readers; `--no-color` (or `NO_COLOR=1`) turns colors off everywhere. Output that isn't a terminal is always plain.

Prefer to hand-edit? `ccauth init` writes a fully commented config; `ccauth wire` prints the exact `settings.json` block.

### Work gateway and personal subscription side by side

Pick how far the gateway wiring reaches. Plain `claude` keeps your own claude.ai subscription wherever ccauth isn't wired:

| Want | Command | What it touches |
|---|---|---|
| One gateway session | `ccauth claude -p work` (tip: `alias claude-work='ccauth claude -p work'`) | Nothing: passes `claude --settings` for that session only |
| Every session in a work repo, incl. the VS Code extension | `ccauth wire --project -p work` | `<repo>/.claude/settings.local.json` (never committed) |
| Every session everywhere | `ccauth wire --write -p work` | `~/.claude/settings.json` |
| Undo either `wire` | `ccauth unwire [--project]` | Removes only what ccauth wrote |

Extra gateway headers (e.g. a team header): `gateway_opts.headers` in the profile for every request, or `-H "x-team: data"` on `ccauth claude` / `ccauth wire` (repeatable; overrides a profile header of the same name). `ccauth claude --isolated` also gives work its own Claude Code config dir (`~/.claude-<profile>`: history, memory, MCP servers, plugins). `ccauth doctor` shows which credential Claude Code uses in the current directory.

### Claude Desktop

Claude Desktop doesn't use `apiKeyHelper`; it reaches a gateway only in its third-party inference mode, configured with its own keys. `ccauth desktop` writes that configuration from the same profile:

```bash
ccauth desktop apply -p work      # quit Claude Desktop first; next launch uses the gateway
ccauth desktop personal           # next launch uses your Claude.ai sign-in again
ccauth desktop status             # what Desktop will use at its next launch
ccauth desktop export -p work --format mobileconfig -o work.mobileconfig   # or json / reg, for IT
```

- The switch is app-wide (Chat, Cowork and Code tabs), not per project, and takes effect at the next launch. Gateway mode has no claude.ai web/mobile sync or Anthropic cloud sessions.
- **Credential:** `--auth sso` (default for passthrough profiles) lets Desktop sign in to your IdP itself, reusing the `http://localhost/callback` registration; add `--broker` on Entra to sign in through Company Portal. `--auth helper` (default for exchange mode and Google) makes Desktop run `ccauth token`, like Claude Code does.
- ccauth only adds its own named entries (`ccauth: <profile>`) to Desktop's configuration library, so configurations you made in Desktop's own window stay. An MDM profile or registry policy for Claude Desktop overrides the library; `apply` warns when one is present.

## Commands

| Command | What it does |
|---|---|
| `ccauth setup [--write]` | Interactive wizard: choose provider + gateway, write the profile, and pick launcher / project / global wiring (`--write` = global) |
| `ccauth init [--force]` | Write a commented example `config.toml` to edit by hand |
| `ccauth login [--device]` | Interactive SSO sign-in; stores the refresh token in your OS keychain |
| `ccauth token` | Print the current credential to stdout — **this is your `apiKeyHelper`**. Silent refresh; auto browser re-auth on expiry |
| `ccauth status [--json]` | Show the active profile, config source, login state, and token expiry |
| `ccauth claude [-H hdr] [--isolated] [-- claude-flags]` | Start Claude Code on the gateway for one session, without touching settings files |
| `ccauth wire [--write\|--project] [-H hdr]` | Print the wiring, or merge it into `~/.claude/settings.json` / this repo's `.claude/settings.local.json` |
| `ccauth unwire [--project]` | Remove ccauth's wiring, keeping everything else |
| `ccauth desktop apply\|personal\|export\|status` | Point Claude Desktop at the gateway (or back to Claude.ai), or export its configuration for import/MDM |
| `ccauth config path\|show\|sync` | Inspect the config layers (user/embedded/managed/remote) or pull remote config |
| `ccauth doctor [--probe]` | Diagnose config + session, show which credential Claude Code uses here, flag anything that bypasses or blocks the helper, and print the gateway-side checklist |
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
  # haiku_model     = "<model id>"  # if the gateway doesn't serve Claude Code's default Haiku ID
  # model_discovery = true          # let Claude Code list models via GET /v1/models
```

`ccauth init` documents every provider/gateway combination. Env overrides for CI/headless: `CCAUTH_TENANT_ID`, `CCAUTH_CLIENT_ID`, `CCAUTH_CLIENT_SECRET`, `CCAUTH_ISSUER`, `CCAUTH_BASE_URL`.

**Setting up your identity provider?** Step-by-step guides (provider app + the matching gateway validation values):
[Microsoft Entra ID](docs/identity-providers/entra-id.md) ·
[Google Workspace](docs/identity-providers/google-workspace.md) ·
[Keycloak / generic OIDC](docs/identity-providers/keycloak.md).

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
| ccauth is never called | `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY` or a `CLAUDE_CODE_USE_*` variable is set — they outrank `apiKeyHelper`. Unset them; `ccauth doctor` shows where they come from. |
| Claude Code refuses `apiKeyHelper` at startup | Managed settings set `forceLoginMethod` / `forceLoginOrgUUID`. IT must remove them for gateway users. |
| Helper in a repo's `.claude/settings.json` doesn't run | Claude Code runs project-level helpers only after you accept the workspace trust prompt. Prefer `ccauth wire --write` (user settings). |
| Background tasks / haiku fail with "model not found" | The gateway doesn't serve Claude Code's default Haiku ID. Set `gateway_opts.haiku_model` and re-run `ccauth wire --write` (also migrates the deprecated `ANTHROPIC_SMALL_FAST_MODEL`). |
| Gateway models missing from `/model` | Set `model_discovery = true`; the gateway must answer `GET /v1/models` with the same JWT, and IDs must contain `claude` or `anthropic`. Failures are silent — check `claude --debug`. |

Run `ccauth doctor --probe` to exercise the whole path end-to-end (credential redacted).

## Development

```bash
make build          # ./ccauth
make test           # unit tests (race)
make fuzz           # native Go fuzzers (FUZZTIME=30s each)
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
