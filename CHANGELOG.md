# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- The browser "Login complete" page now attempts to close itself automatically
  after sign-in (best-effort — browsers block `window.close()` on tabs they opened,
  so it falls back to a "you can close this window" message).

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

[Unreleased]: https://github.com/PalenaAI/claude-code-auth-helper/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/PalenaAI/claude-code-auth-helper/releases/tag/v0.1.0
