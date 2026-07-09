# Keycloak setup (generic OIDC)

Configure a Keycloak **public client** so `ccauth` can obtain a token your gateway
validates. Keycloak stands in here as the example for any generic OIDC provider —
Okta, Auth0, Zitadel, and others follow the same shape (public client + PKCE,
an audience, and a JWKS the gateway trusts).

Tested against Keycloak 24+ (the "new" admin console). Older versions have the same
concepts under slightly different menus.

---

## Part A — Create the client

### 1. Pick a realm

Use an existing realm or create one (**Realms → Create realm**, e.g. `corp`). All
URLs below include this realm name.

### 2. Create a public OIDC client

**Clients → Create client:**

- **Client type:** OpenID Connect
- **Client ID:** `ccauth`
- Next → **Capability config:**
  - **Client authentication: Off** ← this makes it a *public* client (PKCE, no
    secret). If this is On, `ccauth` (which sends no secret) cannot authenticate.
  - **Standard flow: On** (authorization code). Direct access grants can be Off.
- Next → **Login settings:**
  - **Valid redirect URIs:** `http://localhost:8765/callback`
    (a fixed port you'll pin in `ccauth`; simplest and most reliable). Or use a
    wildcard like `http://localhost/*` if you prefer a random port.
- **Save.**
- *(Optional)* **Advanced → Proof Key for Code Exchange Code Challenge Method →
  `S256`** to enforce PKCE. `ccauth` sends PKCE regardless.

### 3. Add an audience (so the token is stamped for your gateway)

By default a Keycloak access token's `aud` won't name your gateway. Add an
**audience mapper**:

**Clients → `ccauth` → Client scopes → `ccauth-dedicated` → Add mapper → By
configuration → Audience.**

- **Name:** `gateway-audience`
- **Included Custom Audience:** `litellm-gateway` (any identifier your gateway will
  expect)
- **Add to access token: On** → **Save.**

The access token's `aud` now includes `litellm-gateway`.

### 4. (Optional) Roles for gateway RBAC

Assign realm or client roles to users (**Users → *user* → Role mapping**). Keycloak
puts realm roles in `realm_access.roles` and client roles in
`resource_access.<client>.roles` in the **access token** — a gateway can map those
to teams.

> **Simpler alternative (no audience mapper):** emit the **ID token** instead
> (`credential_token = "id"` in Part B). A Keycloak ID token's `aud` is the client
> ID (`ccauth`), so set the gateway's audience to `ccauth`. Downside: ID tokens
> don't carry roles by default, so use the access-token path above if you need RBAC.

---

## Part B — ccauth profile

`~/.config/ccauth/config.toml` (or run `ccauth setup` and pick `oidc`):

```toml
default_profile = "keycloak"

[profiles.keycloak]
provider = "oidc"
gateway  = "litellm"        # or portkey / bifrost / generic
mode     = "passthrough"

  [profiles.keycloak.oauth]
  issuer           = "https://<keycloak-host>/realms/<realm>"
  client_id        = "ccauth"
  scopes           = ["openid", "profile", "email"]
  credential_token = "access"     # carries the audience mapper + roles
  redirect_port    = 8765         # MUST match the registered redirect URI
  flow             = "auth_code"

  [profiles.keycloak.gateway_opts]
  base_url = "http://localhost:4000"
  ttl_ms   = 3000000
```

Log in and inspect the token:

```bash
ccauth login --profile keycloak
ccauth token --profile keycloak | cut -d. -f2 | base64 -D 2>/dev/null   # peek at claims
```

The access token carries: `iss` (your realm URL), `aud` (`litellm-gateway`),
`preferred_username`/`email`, and `realm_access.roles` / `resource_access.*.roles`.

---

## Part C — Gateway validation values

Replace `<keycloak-host>` and `<realm>` throughout.

| Value | For Keycloak |
|---|---|
| **JWKS URL** | `https://<keycloak-host>/realms/<realm>/protocol/openid-connect/certs` |
| **Issuer** | `https://<keycloak-host>/realms/<realm>` |
| **Audience** | `litellm-gateway` (the mapped audience — or `ccauth` if using the ID token) |
| **OIDC discovery** | `https://<keycloak-host>/realms/<realm>/.well-known/openid-configuration` |

### LiteLLM (`enable_jwt_auth`)

```yaml
general_settings:
  enable_jwt_auth: true
  litellm_jwtauth:
    user_id_jwt_field: "preferred_username"
    roles_jwt_field: "realm_access.roles"     # dot-notation into nested claims
    user_id_upsert: true
```
```bash
export JWT_PUBLIC_KEY_URL="https://<keycloak-host>/realms/<realm>/protocol/openid-connect/certs"
export JWT_ISSUER="https://<keycloak-host>/realms/<realm>"
export JWT_AUDIENCE="litellm-gateway"
```

For Portkey, register the JWKS URL under *Admin → Authentication* (RS256 — Keycloak
signs RS256 by default).

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Login fails / "Invalid redirect URI" | The registered redirect must match `ccauth`'s exactly. Pin `redirect_port` (Part B) and register `http://localhost:<port>/callback` (Part A step 2). |
| Login fails with `invalid_client` / client-secret error | The client is confidential. Set **Client authentication: Off** (Part A step 2). |
| Gateway `401`, `aud` is `account` or missing your value | Add the audience mapper (Part A step 3), or switch to the ID token and set audience = `ccauth`. |
| Gateway `401` on `iss` mismatch | Behind a reverse proxy, Keycloak's `iss` must be the externally-reachable URL. Set Keycloak's **hostname / frontend URL** so `iss` matches `JWT_ISSUER` exactly. |
| No `realm_access.roles` in the token | Assign roles to the user, and ensure you're emitting the **access token** (roles aren't in the ID token by default). |
| No refresh token / re-login required often | `ccauth` requests `offline_access`; ensure the realm's *Offline Session* settings allow it. |
