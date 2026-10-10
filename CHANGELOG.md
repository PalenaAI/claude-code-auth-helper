# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

### Changed

- `wire --write` / `setup --write` rename the deprecated
  `ANTHROPIC_SMALL_FAST_MODEL` to `ANTHROPIC_DEFAULT_HAIKU_MODEL` and warn when
  the settings `env` holds a credential that would shadow the helper.
- Docs updated for current Claude Code helper behavior (re-run on 401/403 and on
  expired JWTs, slow-helper warning, credential precedence) and managed-settings
  paths/drop-ins.

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

[Unreleased]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/PalenaAI/claude-code-auth-helper/releases/tag/v0.1.0
