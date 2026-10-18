# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- Wiring no longer silently does nothing when the settings file already sets a
  competing credential. `ccauth wire --write` / `setup` kept an existing
  `ANTHROPIC_AUTH_TOKEN` (or `ANTHROPIC_API_KEY`, `CLAUDE_CODE_USE_BEDROCK/VERTEX/
  FOUNDRY`) in the env block, and Claude Code uses those ahead of `apiKeyHelper`.
  They are now moved out of the file ccauth writes (kept in ccauth's `0600` state
  so `ccauth unwire` restores them), `ccauth claude` overrides them with empty
  values for its session, and `wire --project` / `setup` warn when one is set in
  `~/.claude/settings.json` or the shell, where project settings can't override it.

## [1.0.0] - 2026-10-13

First stable release.

### Stability

From 1.0.0 on, these follow semantic versioning: breaking changes to them only
come with a new major version.

- Command names, flags and exit behavior.
- The `config.toml` format (profiles, layers, `gateway_opts`, exchange settings).
- The `ccauth token` contract: only the credential on stdout, diagnostics on stderr.
- What ccauth writes elsewhere: the Claude Code wiring (`apiKeyHelper` + `env`),
  `.claude/settings.local.json` for `wire --project`, and the `ccauth: <profile>`
  entries in Claude Desktop's configuration library.

Human-readable output (colors, wording, layout) is not part of that contract;
use `ccauth status --json` for scripts.

### Added

- `ccauth setup` is now an interactive form (Charm `huh`): arrow-key selects,
  inline validation, steps that only show what applies to your provider and
  gateway, and a summary card with the redirect URI to register and the next
  commands. `--accessible` / `ACCESSIBLE=1` gives plain numbered prompts for
  screen readers.
- Colored, easier-to-scan output for `doctor`, `status`, `login`, `wire`,
  `unwire`, `desktop` and errors (✓ / ⚠ / ✗, section headings, highlighted
  commands and values). `--no-color` / `NO_COLOR=1` turns colors off; output to
  a pipe or file is always plain, and the credential `ccauth token` prints is
  never styled.
- `ccauth desktop apply|personal|export|status` configures Claude Desktop's
  third-party inference mode for the profile's gateway: `apply` adds/updates a
  `ccauth: <profile>` entry in Desktop's configuration library and applies it,
  `personal` switches Desktop back to the Claude.ai sign-in, and `export` writes
  JSON (Import configuration), `.mobileconfig` or `.reg` for IT. Desktop signs in
  to the IdP itself (`--auth sso`, optionally `--broker` on Entra) or runs
  `ccauth token` as its credential helper (`--auth helper`). `doctor` shows
  Desktop's applied configuration.
- `ccauth token --min-lifetime <duration>` makes the token outlive a caller's
  cache window (used by Claude Desktop's helper).

### Changed

- When Claude Desktop runs `ccauth token` (`CLAUDE_HELPER_CONTEXT` set), browser
  re-login only happens in its `interactive` context and a mid-session refresh
  stays within Desktop's 20 s limit. Calls from Claude Code are unchanged.

## [0.2.0] - 2026-10-13

### Added

- `ccauth claude [-H "Name: value"] [--isolated] [-- claude-flags]` starts Claude
  Code on the gateway for one session through `claude --settings`, without
  touching settings files, so plain `claude` keeps your personal subscription.
  It unsets credentials that would outrank the helper for that session;
  `--isolated` uses a separate Claude Code config dir per profile.
- `ccauth wire --project` wires only the current repository
  (`.claude/settings.local.json` at the repo root, never committed);
  `ccauth unwire [--project]` removes exactly what ccauth wrote and keeps the rest.
- Extra gateway headers: `-H` on `claude`/`wire` for per-session or per-repo
  headers (overrides a profile header of the same name), and `setup` now asks
  for static headers for every gateway.
- `ccauth doctor` reports which credential Claude Code uses in the current
  directory (`claude auth status`).

### Changed

- `ccauth setup` asks where to use the profile (launcher, this repository, or
  every session) instead of only offering global wiring; `--write` still means
  every session.
- Custom headers are validated: HTTP-token names, no line breaks in values, and
  no `Authorization`/`x-api-key` or Claude Code protocol headers.
- `doctor` no longer warns when no settings file has an `apiKeyHelper`, since
  launcher users don't need one.

## [0.1.4] - 2026-10-12

### Fixed

- The Entra guide and `ccauth setup` now point to `http://localhost/callback` as the
  redirect URI. Entra ignores the port of localhost redirect URIs, so ccauth's random
  callback port matches without pinning. `setup` prints the URI to register and asks
  for an optional fixed port (needed only for port forwarding such as devcontainers);
  `doctor` shows the URI too.
- `ccauth setup` no longer uses the literal placeholder `api://<gateway-app-id>/.default`
  as the Entra scope when you press Enter. It now defaults to `<client-id>/.default`
  (CLI app = gateway API) and re-asks until the scope is valid.
- Entra profiles are checked for scopes Entra would reject only after the browser
  round trip: placeholders, scopes without a resource such as a bare client ID
  (AADSTS650053), and `api://<client-id>/…` for an app requesting a token for
  itself (AADSTS90009). The error names the value to use.
- Homebrew bottles are now actually used: the formula wrote the bottle cellar as
  a string (`cellar: "any_skip_relocation"`) instead of a symbol, so Homebrew
  treated every bottle as incompatible and still built from source.
- The release no longer tries to build an Intel macOS bottle. Homebrew dropped
  macOS x86_64 to Tier 3 in September 2026 and refuses to build the Go
  dependency there; Intel Macs can use the `ccauth-darwin-amd64` release binary.

## [0.1.3] - 2026-10-10

### Fixed

- `brew install PalenaAI/tap/ccauth` no longer builds from source. Releases now
  attach Homebrew bottles (macOS arm64/Intel, built on macOS 15 and used on 15,
  26 and 27; Linux x86_64/arm64), so installs no longer fail when the Command Line
  Tools don't match the installed macOS. A manual `release` workflow run with a
  tag rebuilds bottles for an existing release.
- Removed the custom CodeQL workflow: GitHub's CodeQL default setup is enabled
  for the repository (Go, GitHub Actions, Ruby) and rejects uploads from a second,
  advanced configuration, so the workflow failed on every run.

## [0.1.2] - 2026-10-10

### Added

- `gateway_opts.model`, `haiku_model` and `model_discovery` emit
  `ANTHROPIC_MODEL`, `ANTHROPIC_DEFAULT_HAIKU_MODEL` and
  `CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY`; `setup` asks for the latter two,
  and the gateway checklist covers what `GET /v1/models` needs per gateway.
- `ccauth doctor` inspects Claude Code's managed (incl. `managed-settings.d/`),
  project and user settings plus the environment, and flags credentials that
  outrank `apiKeyHelper`, managed `forceLoginMethod`/`forceLoginOrgUUID` (which
  block it), a missing/foreign helper, a missing `ANTHROPIC_BASE_URL`, and
  project-level helpers that need workspace trust.
- Native Go fuzz tests for the unverified JWT reader and config parsing/resolution
  (`make fuzz`, smoke-run in CI).
- CodeQL static analysis and Dependabot (Go modules + GitHub Actions).

### Changed

- `wire --write` / `setup --write` rename the deprecated
  `ANTHROPIC_SMALL_FAST_MODEL` to `ANTHROPIC_DEFAULT_HAIKU_MODEL` and warn when
  the settings `env` holds a credential that would shadow the helper.
- Docs updated for current Claude Code helper behavior (re-run on 401/403 and on
  expired JWTs, slow-helper warning, credential precedence) and managed-settings
  paths/drop-ins.
- Minimum Go is now 1.26 (Go 1.25 is out of support); CI and releases build
  with the latest Go 1.27.x.
- The release workflow's `GITHUB_TOKEN` is read-only by default; only the
  release job gets `contents: write`.

### Security

- Bump `golang.org/x/sys` to v0.49.0 (GO-2026-5024 / CVE-2026-39824, Windows
  `NewNTUnicodeString` overflow), plus `golang.org/x/oauth2` v0.37.0,
  `go-oidc` v3.21.0 and `go-jose` v4.1.5.

## [0.1.1] - 2026-07-10

### Added

- Identity-provider setup guides for Entra ID, Google Workspace, and Keycloak,
  each with the matching gateway JWT-validation values.

### Changed

- The browser "Login complete" page now auto-closes after sign-in (best-effort;
  falls back to a "you can close this window" message where the browser blocks it).

### Fixed

- CI builds with the latest patched Go 1.25.x so govulncheck stays green.

## [0.1.0] - 2026-07-09

### Added

- Initial release. **ccauth** — an OAuth2/SSO credential helper that authenticates
  Claude Code to AI gateways through its `apiKeyHelper` hook, so developers never
  hand-manage API keys.
- **Identity providers:** Microsoft Entra ID, Google Workspace, and generic OIDC
  (Okta / Auth0 / Keycloak), via a single OIDC engine with provider presets.
- **Gateways:** LiteLLM, Portkey, Bifrost, and any OIDC-aware gateway, with two
  credential modes — *passthrough* (emit the raw SSO JWT) and *exchange* (trade it
  for a gateway-native key via RFC 8693 or a broker).
- **Interactive `login`** (authorization code + PKCE + loopback, or device-code)
  split from a fast, silent **`token`** helper that returns cached tokens, refreshes
  silently, and opens a browser to re-authenticate when the refresh token expires.
- **Layered configuration** (`user < embedded < managed < remote`) for zero-config
  enterprise rollout, with `allow_user_profiles` lockdown and `ccauth config` /
  `config sync` commands.
- **Secure storage:** refresh token in the OS keychain (macOS Keychain, Windows
  Credential Manager, Linux Secret Service) with a `0600`-file fallback.
- **Commands:** `setup`, `init`, `login`, `token`, `logout`, `status`, `wire`,
  `config`, `doctor`, `gateways`.
- **Reference material:** `DESIGN.md`, `ENTERPRISE.md`, LiteLLM and Portkey
  examples, a Bifrost edge-auth plugin, and a token-exchange broker contract.

[Unreleased]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.2.0...v1.0.0
[0.2.0]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.4...v0.2.0
[0.1.4]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/PalenaAI/claude-code-auth-helper/releases/tag/v0.1.0
