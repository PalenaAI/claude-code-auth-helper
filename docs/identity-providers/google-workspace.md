# Google Workspace setup

Configure a Google **OAuth Desktop client** so `ccauth` can obtain a token your
gateway validates.

> **Emit the ID token, not the access token.** Google **access tokens are opaque**
> — a gateway cannot cryptographically validate them. Google **ID tokens** are
> signed OIDC JWTs with a verifiable `aud`. `ccauth`'s `google` provider emits the
> **ID token** by default (`credential_token = "id"`), and its `aud` is your OAuth
> **client ID**. (If you'd rather validate the opaque access token, see
> [Alternative](#alternative-validate-the-opaque-access-token).)

---

## Part A — Create the OAuth client

### 1. Configure the OAuth consent screen

Google Cloud Console → **APIs & Services → OAuth consent screen**.

- **User type: Internal** (recommended for Workspace — restricts to your org, needs
  no Google verification, and avoids the 7-day-refresh-token limit that "External /
  Testing" apps have). Only choose *External* if you must include non-org users, and
  then **publish** the app.
- Add scopes: **`openid`, `.../auth/userinfo.email`, `.../auth/userinfo.profile`**.
- Save.

### 2. Create the Desktop OAuth client

**APIs & Services → Credentials → Create credentials → OAuth client ID.**

- **Application type: Desktop app.**
- Name: `ccauth`.
- **Create.** Copy the **Client ID** and **Client secret**.

> The Desktop-app "client secret" is **not confidential** — it ships in installed
> apps by design, and security comes from PKCE + the loopback redirect. `ccauth`
> handles the loopback automatically; no redirect URI configuration is needed for
> Desktop clients.

---

## Part B — ccauth profile

`~/.config/ccauth/config.toml` (or run `ccauth setup`):

```toml
default_profile = "google"

[profiles.google]
provider = "google"
gateway  = "litellm"        # or portkey / bifrost / generic
mode     = "passthrough"

  [profiles.google.oauth]
  client_id     = "<xxxxx.apps.googleusercontent.com>"
  client_secret = "<GOCSPX-...>"
  # scopes default to openid/email/profile; credential_token defaults to "id"

  [profiles.google.gateway_opts]
  base_url = "http://localhost:4000"
  ttl_ms   = 3000000
```

Log in and inspect the ID token:

```bash
ccauth login --profile google
ccauth token --profile google | cut -d. -f2 | base64 -D 2>/dev/null   # peek at claims
```

The ID token carries: `iss = https://accounts.google.com`, **`aud` = your client
ID**, `email`, `email_verified`, and `hd` (your Workspace domain).

---

## Part C — Gateway validation values

| Value | For Google |
|---|---|
| **JWKS URL** | `https://www.googleapis.com/oauth2/v3/certs` |
| **Issuer** | `https://accounts.google.com` |
| **Audience** | your OAuth **client ID** (`…apps.googleusercontent.com`) |
| **OIDC discovery** | `https://accounts.google.com/.well-known/openid-configuration` |

### LiteLLM (`enable_jwt_auth`)

```yaml
general_settings:
  enable_jwt_auth: true
  litellm_jwtauth:
    user_id_jwt_field: "email"
    user_id_upsert: true
```
```bash
export JWT_PUBLIC_KEY_URL="https://www.googleapis.com/oauth2/v3/certs"
export JWT_ISSUER="https://accounts.google.com"
export JWT_AUDIENCE="<xxxxx.apps.googleusercontent.com>"
```

Restrict to your Workspace domain by checking the `hd` claim (a gateway policy) or
by mapping `email`/`hd` to teams. For Portkey, register the JWKS URL under
*Admin → Authentication*.

---

## Alternative: validate the opaque access token

If you prefer `ccauth` to emit the **access token** (`credential_token = "access"`),
the gateway must validate it by **introspection**, not JWKS, since it's opaque.
LiteLLM supports this:

```yaml
general_settings:
  enable_oauth2_auth: true
```
```bash
export OAUTH_TOKEN_INFO_ENDPOINT="https://oauth2.googleapis.com/tokeninfo"
export OAUTH_USER_ID_FIELD_NAME="email"
```

This trades a JWKS check for a network call to Google's tokeninfo endpoint per
request. The ID-token path (default) is usually simpler and faster.

---

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Re-prompted for consent every ~7 days | The app is *External / Testing*. Set **User type: Internal**, or publish the consent screen (Part A step 1). |
| Gateway `401`, "no `aud`" / can't parse token | You're emitting the **access token** (opaque). Use `credential_token = "id"` (default) or the introspection alternative above. |
| `aud` doesn't match | `JWT_AUDIENCE` must equal your **client ID** exactly, including the `.apps.googleusercontent.com` suffix. |
| Login opens but never returns | A firewall is blocking the loopback callback; try again on a network that allows `127.0.0.1` connections, or use a machine with a local browser. |
