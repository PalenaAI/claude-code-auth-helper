# Exchange / broker contract (mode = exchange)

Use exchange mode when the gateway can't validate your IdP JWT directly (Bifrost,
or a non-Enterprise tier). `ccauth` sends the user's SSO token to an exchange
endpoint and emits the **gateway-native credential** it returns.

`ccauth` supports two styles.

## 1. RFC 8693 token exchange (`style = "rfc8693"`)

`ccauth` POSTs `application/x-www-form-urlencoded` to `exchange.token_url`:

```
grant_type=urn:ietf:params:oauth:grant-type:token-exchange
subject_token=<the IdP access or ID token>
subject_token_type=urn:ietf:params:oauth:token-type:access_token   # or :id_token
requested_token_type=urn:ietf:params:oauth:token-type:access_token
audience=<exchange.audience>          # optional, e.g. "bifrost"
resource=<exchange.resource>          # optional
client_id=<exchange.client_id>        # if set and no client_secret
# or HTTP Basic auth when client_id + client_secret are both set
```

Expected JSON response:

```json
{ "access_token": "sk-bf-...", "expires_in": 3600 }
```

`ccauth` caches `access_token` and refreshes it before `expires_in` elapses.

## 2. Broker (`style = "broker"`)

For a simple custom broker. `ccauth` POSTs to `exchange.token_url` with the IdP
token as a bearer:

```
POST <token_url>
Authorization: Bearer <IdP token>
Accept: application/json
```

Expected JSON response (field names configurable):

```json
{ "key": "sk-bf-...", "expires_in": 3600 }
```

- `exchange.key_field`    — JSON field holding the gateway key (default `key`)
- `exchange.expiry_field` — JSON field holding lifetime seconds (default `expires_in`)

If no expiry is returned, `ccauth` falls back to the key's own JWT `exp`, else a
conservative 5-minute default.

## What the broker must do

1. **Validate** the incoming IdP token (signature via JWKS, `iss`, `aud`, `exp`,
   required scopes/roles) — this is where SSO trust is enforced.
2. **Map** the verified identity to a gateway credential:
   - Bifrost: look up or mint a virtual key (`sk-bf-*`) for that user/team.
   - Portkey (non-Enterprise): return a workspace API key.
3. **Return** the credential and its lifetime. Prefer short-lived, per-user keys.

## Example ccauth profile

```toml
[profiles.bifrost]
provider = "entra"
gateway  = "bifrost"
mode     = "exchange"

  [profiles.bifrost.oauth]
  tenant_id = "<tenant-guid>"
  client_id = "<app-registration-client-id>"
  scopes    = ["api://<gateway-app-id>/.default"]

  [profiles.bifrost.gateway_opts]
  base_url = "http://localhost:8080/anthropic"
  ttl_ms   = 240000

  [profiles.bifrost.exchange]
  style              = "rfc8693"
  token_url          = "https://broker.internal/token"
  audience           = "bifrost"
  subject_token_type = "access"
```
