# Security Policy

`ccauth` handles OAuth credentials, so we take security reports seriously and aim
to respond quickly.

## Supported Versions

Only the **latest release** of ccauth receives security fixes. Older releases are
not backported; upgrade to the latest release to receive fixes.

| Version       | Supported          |
|---------------|--------------------|
| latest (main) | :white_check_mark: |
| < latest      | :x:                |

## Reporting a Vulnerability

**Please do not file public GitHub issues for security vulnerabilities.**

Report vulnerabilities via one of the following channels:

1. **GitHub Security Advisories** — preferred. Use the
   [Security → Report a vulnerability](https://github.com/PalenaAI/claude-code-auth-helper/security/advisories/new)
   form on this repository.
2. **Email** — `security@bitkaio.com` for issues that cannot be reported via GitHub.

Please include:

- A description of the vulnerability and its potential impact
- Steps to reproduce or proof-of-concept code
- Affected versions
- Any suggested mitigations

Please **redact any real tokens, refresh tokens, client secrets, or tenant IDs**
from reports and logs.

## Response SLA

| Severity | Initial response | Fix target   |
|----------|------------------|--------------|
| Critical | Within 48 hours  | 7 days       |
| High     | Within 7 days    | 30 days      |
| Medium   | Within 14 days   | Next release |
| Low      | Within 30 days   | Best-effort  |

We follow **coordinated disclosure**. Once a fix is available, we will:

1. Publish a GitHub Security Advisory with a CVE identifier when appropriate
2. Credit the reporter (unless anonymity is requested)
3. Release a patched version

## How ccauth handles credentials

Context for assessing reports (see [DESIGN.md §8](DESIGN.md) for detail):

- ccauth uses **public-client OAuth with PKCE** (RFC 8252) — no client secret is
  stored on the user's machine for Entra/OIDC public clients.
- The long-lived **refresh token** is stored in the **OS keychain** (macOS Keychain,
  Windows Credential Manager, Linux Secret Service), falling back to a `0600` file
  only where no keychain is available.
- Only short-lived tokens are emitted; **ccauth never logs tokens**.
- ccauth parses tokens **unverified** solely to read `exp` for refresh timing;
  cryptographic verification (signature, `iss`, `aud`, `exp`, scopes) is performed
  by the gateway.
- A tenant ID and a public client ID are **not secrets** (they appear in every OAuth
  request); reports treating their disclosure as a vulnerability will be closed as
  informational.

## Supply chain

Cross-compiled release binaries are produced by `make cross` and published with a
**`SHA256SUMS`** file so downloads can be verified:

```bash
shasum -a 256 -c SHA256SUMS
```

Signing release binaries with [cosign](https://github.com/sigstore/cosign) and
publishing an SBOM are planned. Dependencies are kept current and scanned for known
vulnerabilities (`govulncheck`).
