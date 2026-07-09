# Microsoft Entra ID setup

Configure an Entra **app registration** so `ccauth` can obtain an access token
your gateway validates. This takes ~10 minutes in the Azure portal.

> **App registration, not Enterprise application.** These are different blades.
> *Enterprise applications* configure SAML/OIDC SSO for a SaaS app's web UI — that
> is **not** what `ccauth` needs. You need an **App registration** (a public OAuth
> client). Creating an app registration auto-creates its Enterprise-app (service
> principal) counterpart; the reverse is not true.

One app registration can play **both** roles here — the client `ccauth` signs in
as, **and** the API the token is audienced to. The steps below use a single app.

---

## Part A — Create and configure the app registration

### 1. Create the app registration

Azure portal → **Microsoft Entra ID → App registrations → New registration**.

- **Name:** `ccauth` (or anything).
- **Supported account types:** *Accounts in this organizational directory only*
  (single tenant) is recommended.
- **Redirect URI:** leave blank for now.
- **Register.**

From the **Overview** page, copy these — you'll need them later:
- **Directory (tenant) ID** → `tenant_id`
- **Application (client) ID** → `client_id`

### 2. Make it a public client (so ccauth can log in with PKCE)

**Authentication → Add a platform → Mobile and desktop applications.**

- Add the redirect URI **`http://localhost`** (Entra treats loopback specially and
  allows any port at runtime — you do **not** need to pin a port).
- **Save.**

Then, still under **Authentication → Advanced settings**, set
**"Allow public client flows" = Yes** and **Save**.

> Skipping either of these is the #1 cause of login failing immediately.

### 3. Expose an API (this creates the audience)

**Expose an API → Application ID URI → Add** (or "Set").

- Accept the default `api://<application-client-id>` or enter a custom URI like
  `api://ccauth-gateway`. **Save.** *This value is your `api://<gateway-app-id>`.*
- **Add a scope:**
  - Scope name: `access_as_user`
  - Who can consent: *Admins and users*
  - Fill the display name/description, **State: Enabled**, **Add scope.**

### 4. Let the app call its own API

Because the same app is both client and API, grant it access to the scope you just
created, so `.default` returns a token audienced to your API.

**API permissions → Add a permission → My APIs → select your app → Delegated
permissions → check `access_as_user` → Add permissions.**

Then **Grant admin consent for &lt;tenant&gt;** and confirm the status shows a green
check.

### 5. (Recommended) Force v2 access tokens

The `aud` claim's format depends on the token version. Pin it so it's predictable.

**Manifest** → find `requestedAccessTokenVersion` (under `api` in the newer
manifest; older manifests call it `accessTokenAcceptedVersion`) → set it to **`2`**
→ **Save.**

With v2 tokens, `aud` = your app's **Application (client) ID (GUID)**. (With v1 it
would be the `api://…` URI instead.)

### 6. (Optional) App roles for gateway RBAC

If you want the gateway to map users to teams/roles, define **App roles**
(*App roles → Create app role*, e.g. `gateway.user`) and assign users/groups via
the Enterprise application → *Users and groups*. Assigned roles appear in the token's
`roles` claim, which LiteLLM/Portkey can map to teams.

---

## Part B — ccauth profile

`~/.config/ccauth/config.toml` (or run `ccauth setup`):

```toml
default_profile = "entra"

[profiles.entra]
provider = "entra"
gateway  = "litellm"        # or portkey / bifrost / generic
mode     = "passthrough"

  [profiles.entra.oauth]
  tenant_id = "<Directory (tenant) ID>"
  client_id = "<Application (client) ID>"
  scopes    = ["api://<your Application ID URI>/.default"]   # e.g. api://ccauth-gateway/.default
  flow      = "auth_code"        # use "device_code" for headless/SSH

  [profiles.entra.gateway_opts]
  base_url = "http://localhost:4000"
  ttl_ms   = 3000000
```

Log in and inspect the token:

```bash
ccauth login --profile entra
ccauth token --profile entra | cut -d. -f2 | base64 -D 2>/dev/null   # peek at claims
```

The token carries: `aud` (your API), `iss` (tenant issuer), `azp`/`appid` (the
client), `scp` (`access_as_user`), and `roles` (if you assigned app roles).

---

## Part C — Gateway validation values

Give your gateway these three values. Endpoints use your **tenant ID (GUID)**.

| Value | For Entra |
|---|---|
| **JWKS URL** | `https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys` |
| **Issuer** | `https://login.microsoftonline.com/<tenant>/v2.0` |
| **Audience** | v2 token → your app's **client-ID GUID**; v1 token → the `api://…` URI |
| **OIDC discovery** | `https://login.microsoftonline.com/<tenant>/v2.0/.well-known/openid-configuration` |

### LiteLLM (`enable_jwt_auth`)

```yaml
general_settings:
  enable_jwt_auth: true
  litellm_jwtauth:
    user_id_jwt_field: "sub"
    roles_jwt_field: "roles"        # Entra app roles
    user_id_upsert: true
```
```bash
export JWT_PUBLIC_KEY_URL="https://login.microsoftonline.com/<tenant>/discovery/v2.0/keys"
export JWT_ISSUER="https://login.microsoftonline.com/<tenant>/v2.0"
export JWT_AUDIENCE="<your app's client-ID GUID>"     # for v2 tokens (see Part A step 5)
```

> **Always set `JWT_AUDIENCE` and `JWT_ISSUER`.** LiteLLM silently disables those
> checks when they're unset. See [examples/litellm](../../examples/litellm).

For Portkey, register the same **JWKS URL** under *Admin → Authentication* (RS256);
for any OIDC gateway, point it at the JWKS URL and set the issuer + audience.

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Browser login fails instantly / "redirect URI mismatch" | Missing `http://localhost` redirect under *Mobile and desktop applications*, or "Allow public client flows" not set to Yes (Part A steps 2). |
| `ccauth login` errors with `invalid_scope` / `AADSTS65001` | The app isn't consented to its own API — do Part A step 4 (add the scope under *My APIs* and grant admin consent). |
| Gateway returns `401`, token looks valid | `aud`/`iss` mismatch. Decode the token (Part B) and set the gateway's `JWT_AUDIENCE`/`JWT_ISSUER` to the **exact** values in the token. Remember v1 vs v2 `aud` format (Part A step 5). |
| Device-code login blocked | Conditional Access can block device code; use `flow = "auth_code"`. |
| Works via curl but Claude Code 401s | Corporate WAF may strip/inspect the `/v1/messages` body — exempt that path (Claude prompts trip XSS rules). |
