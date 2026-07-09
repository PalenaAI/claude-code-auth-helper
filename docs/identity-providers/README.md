# Identity provider setup

Step-by-step guides for configuring each identity provider so `ccauth` can obtain
a token your AI gateway will validate.

- [Microsoft Entra ID](entra-id.md)
- [Google Workspace](google-workspace.md)
- [Keycloak (generic OIDC)](keycloak.md)

## The shared model

Every provider setup is the same three pieces. Understanding them makes each
guide obvious:

1. **A client** — a *public* OAuth application `ccauth` signs in as, using
   authorization code + PKCE with a `http://localhost` loopback redirect. No client
   secret lives on the user's machine (except Google, whose "Desktop app" secret is
   non-confidential by design).

2. **An audience** — the token must be *stamped for your gateway* so the gateway
   can verify "this token was minted for me." This is the `aud` claim. How you set
   it differs per provider (Entra: *Expose an API*; Google: the client ID itself;
   Keycloak: an *audience mapper*), but the goal is identical.

3. **Validation at the gateway** — the gateway trusts the provider's **JWKS**
   (public keys) and checks the token's signature, `iss` (issuer), `aud`
   (audience), and `exp`. You give the gateway three values: the **JWKS URL**, the
   **issuer**, and the **expected audience**.

```
  ccauth  ──PKCE login──▶  IdP  ──issues JWT (aud = gateway)──▶  ccauth token
                                                                     │
   Claude Code ──apiKeyHelper──▶ ccauth token ──Bearer JWT──▶  gateway
                                                                     │
                          gateway validates: signature (JWKS) + iss + aud + exp
```

## What each guide gives you

- The provider-side steps (with the exact settings that bite people).
- The resulting **ccauth profile** (`~/.config/ccauth/config.toml`).
- The three **gateway validation values** (JWKS URL, issuer, audience) — shown for
  LiteLLM's `enable_jwt_auth`, but the same values apply to Portkey's JWKS config or
  any OIDC-aware gateway.

## Two things that are true for every provider

- **Data-plane JWT auth, not UI SSO.** `ccauth` sends a bearer token to the
  gateway's `/v1/messages` endpoint. The gateway must be configured to *validate an
  inbound JWT* (LiteLLM `enable_jwt_auth`, Portkey Enterprise JWT auth, etc.) — this
  is **not** the same as configuring SSO for the gateway's admin dashboard. See
  [DESIGN.md §4–5](../../DESIGN.md).
- **Verify the real `aud` before configuring the gateway.** Don't assume the
  audience format — run the sandbox test and read the decoded `aud`/`iss`:

  ```bash
  TENANT_ID=… CLIENT_ID=… API_SCOPE=… GATEWAY_URL=… ./hack/sandbox-test.sh
  ```

  It prints the token's actual claims so you set the gateway's expected audience to
  exactly what the token carries.
