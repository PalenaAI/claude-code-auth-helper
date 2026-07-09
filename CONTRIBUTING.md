# Contributing to ccauth

Thank you for your interest in contributing! This guide will help you get started.

## Code of Conduct

Be respectful and constructive. We are committed to providing a welcoming and inclusive experience for everyone.

## Contributor License Agreement & DCO

Two lightweight requirements apply to every contribution:

1. **CLA** — you must agree to the [Contributor License Agreement](CLA.md). A CLA
   bot will prompt you on your first pull request.
2. **DCO** — every commit must be signed off under the
   [Developer Certificate of Origin](https://developercertificate.org/). Add the
   sign-off automatically with `git commit -s`:

   ```
   Signed-off-by: Your Name <you@example.com>
   ```

## Getting Started

### Prerequisites

- Go 1.25+
- git
- (optional) a real Entra/Google/OIDC app registration and a gateway to test end-to-end

### Fork and Clone

```sh
git clone https://github.com/<your-username>/claude-code-auth-helper.git
cd claude-code-auth-helper
```

### Build, Test, Run

```sh
make build        # -> ./ccauth
make test         # unit tests
make vet          # go vet
./ccauth --help
```

## Development Workflow

### 1. Create a Branch

```sh
git checkout -b feat/my-feature
```

Use conventional branch prefixes: `feat/`, `fix/`, `docs/`, `refactor/`, `test/`.

### 2. Make Your Changes

- Follow existing code patterns and conventions (see below and [CLAUDE.md](CLAUDE.md)).
- Add or update tests for any new or changed behavior.
- Every `.go` file must carry the Apache license header (`make license-check` verifies).

### 3. Test, Vet, Format

```sh
make test
make vet
gofmt -l .            # must print nothing
make license-check    # verifies SPDX/Apache headers
```

### 4. Commit and Push

Use [Conventional Commits](https://www.conventionalcommits.org/) **and** sign off:

```
feat(oidc): add device-code polling backoff
fix(credential): refresh id token before exp when Claude TTL is large
docs: document the exchange/broker contract
test(config): cover managed-layer lockdown
refactor(cli): extract wiring helpers
```

```sh
git commit -s -m "feat(oidc): add device-code polling backoff"
```

### 5. Open a Pull Request

- Reference any related issues.
- Ensure CI passes (build, test, vet, license headers) and the CLA/DCO checks are green.
- Keep PRs focused — one feature or fix per PR.

## Coding Conventions

### Go Style

- Follow standard Go conventions and [Effective Go](https://go.dev/doc/effective_go).
- Wrap errors with context: `fmt.Errorf("refreshing token for %s: %w", name, err)`.
- **The `token` command writes only the credential to stdout**; all human-facing
  output goes to stderr. Never print anything else to stdout on that path.
- Prefer named types and explicit, actionable error messages that name the next
  command to run.

### Invariants (do not break)

See [CLAUDE.md](CLAUDE.md) for the full list. The load-bearing ones:

- `login` is interactive; `token` is silent-first and only escalates to a browser
  login on `credential.ErrLoginRequired`, gated by `canAutoLogin` and serialized by
  a login lock.
- Config is layered (`user < embedded < managed < remote`); IT layers override user
  and may lock it down. `token` never fetches remote config on the hot path.
- ccauth parses JWTs **unverified** only to read `exp`; signature/audience/issuer
  verification is the gateway's responsibility.
- Never log tokens.

### Testing

- Prefer table-driven tests. Add or extend one when changing presets, credential
  freshness, header logic, or config layering.
- Tests must not require network or real credentials (mock/env-drive instead).

## Project Structure

```text
cmd/ccauth/          entrypoint
internal/config/     layered profiles, provider presets, validation
internal/oidc/       OIDC discovery, auth-code+PKCE+loopback, device-code, refresh
internal/credential/ passthrough + exchange emit, freshness/skew logic
internal/store/      keychain + file session store, login lock
internal/gateway/    per-gateway presets + Claude Code wiring
internal/jwtutil/    unverified claim/expiry reader
internal/cli/        setup, init, login, token, logout, status, wire, config, doctor
examples/            LiteLLM config, Portkey notes, Bifrost plugin, broker contract
```

## Adding Support

**A new gateway:** add an entry to `internal/gateway.registry` (name, `NativeJWT`,
`DefaultMode`, base-URL hint, operator setup notes), then extend the setup wizard
default base URL and any routing headers.

**A new identity provider:** extend the presets in `internal/config` (issuer,
default scopes, credential-token default) and `internal/oidc.effectiveScopes`; the
generic OIDC engine handles the flows.

## Reporting Issues

- Use [GitHub Issues](https://github.com/PalenaAI/claude-code-auth-helper/issues).
- Include steps to reproduce, expected vs actual behavior, and relevant output
  (with tokens redacted).
- For security vulnerabilities, **do not** open a public issue — see
  [SECURITY.md](SECURITY.md) (email `security@bitkaio.com`).

## License

By contributing, you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE) and the terms of the [CLA](CLA.md).
