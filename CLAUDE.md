# CLAUDE.md — ccauth

## What this is
`ccauth`: a Go CLI that authenticates Claude Code to an AI gateway (LiteLLM,
Portkey, Bifrost, generic OIDC) using corporate SSO (Entra ID, Google Workspace,
generic OIDC), feeding a short-lived token to Claude Code via its `apiKeyHelper`
hook. See [DESIGN.md](DESIGN.md) for the architecture and gateway matrix.

## Tech stack
Go (single static binary). Deps: `spf13/cobra` (CLI), `coreos/go-oidc/v3` +
`golang.org/x/oauth2` (OIDC/OAuth), `zalando/go-keyring` (keychain),
`golang-jwt/jwt/v5` (unverified claim reads), `BurntSushi/toml` (config).

## Commands
- `make build` → `./ccauth`   ·   `make install`   ·   `make cross` (dist/ binaries)
- `make test` / `go test ./...`   ·   `make vet`   ·   `make tidy`

## Layout
- `cmd/ccauth` — entrypoint
- `internal/config` — TOML profiles, provider **presets**, validation, env overrides
- `internal/oidc` — discovery + auth-code/PKCE/loopback + device-code + refresh
- `internal/credential` — passthrough & exchange emit + freshness/skew logic
- `internal/store` — keychain + `0600`-file session store
- `internal/gateway` — per-gateway presets + Claude Code wiring
- `internal/jwtutil` — unverified expiry/claims reader
- `internal/cli` — commands
- `examples/` — LiteLLM config, Portkey notes, Bifrost plugin (own go.mod), broker contract

## Key invariants (don't break these)
- **`login` is interactive; `token` is silent-first.** `token` returns a cached
  token or silently refreshes. Only when the refresh token is dead does it escalate
  to an interactive browser login — gated by `canAutoLogin` (helper_interactive +
  browser available + not CI/`CCAUTH_NONINTERACTIVE` + not device flow), serialized
  by `store.AcquireLoginLock`, bounded by a 3-min timeout. `credential.ErrLoginRequired`
  is the escalation signal. **Never write anything but the credential to stdout.**
- **Config is layered** (`user < embedded < managed < remote`); IT layers override
  user and can set `allow_user_profiles=false` to lock down. `token` reads the
  cached remote file only — never fetch on the hot path. `setup` edits the *user*
  layer via `LoadUserConfig`; `Save` only ever writes the user file.
- **`apiKeyHelper` output is sent by Claude Code as BOTH `Authorization: Bearer` and
  `x-api-key`.** Passthrough relies on the Bearer header reaching a JWKS-validating gateway.
- **`apiKeyHelper` is 4th in Claude Code's credential precedence** (after
  `CLAUDE_CODE_USE_*`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_API_KEY`), is re-run on
  401/403 and on expired cached JWTs, and is refused when managed settings set
  `forceLoginMethod`/`forceLoginOrgUUID`. `doctor` checks these (`cli/claudecode.go`).
  Use `ANTHROPIC_DEFAULT_HAIKU_MODEL`; `ANTHROPIC_SMALL_FAST_MODEL` is deprecated.
- **`token` reads `CLAUDE_CODE_API_KEY_HELPER_TTL_MS`** and guarantees the emitted
  token outlives Claude Code's cache window (`requiredLifetime`).
- **Provider token choice:** Entra→access token; Google→ID token (access tokens are
  opaque); generic OIDC→configurable. Encoded in `config.applyPresets`.
- **Mode per gateway:** Bifrost has no native inbound JWT validation → defaults to
  `exchange`; others default to `passthrough`.
- Never log tokens. `ccauth` parses JWTs **unverified** only to read `exp`;
  cryptographic verification is the gateway's job.

## Conventions
- Errors: wrap with context (`fmt.Errorf("...: %w")`), actionable messages that name
  the next command to run. Credential output goes to **stdout**; everything else to **stderr**.
- New gateway → add to `internal/gateway.registry` (+ `NativeJWT`, `DefaultMode`,
  setup notes). New provider → extend `config` presets + `oidc.effectiveScopes`.
- Add/extend a table-driven test when changing presets, freshness, or header logic.

## Git
Never commit/push/merge/rebase (owner handles it). Conventional commit messages.
