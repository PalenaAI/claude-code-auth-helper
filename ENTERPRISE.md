# Enterprise deployment guide

How IT provisions `ccauth` so end users authenticate to the AI gateway with SSO
**without ever seeing a tenant ID, client ID, or gateway URL** — and without
running a setup wizard. The end-user experience becomes:

```
(install, pushed by MDM)  →  ccauth login   →  done
```

…and often not even `login`, because the helper can open the browser itself when
a session expires (see [Interactive re-auth](#interactive-re-authentication)).

---

## First, a clarification: tenant/client IDs are not secrets

A concern we hear is "we don't want to share the tenant ID with every employee."
Worth being precise: **a directory (tenant) ID and a public-client application ID
are not secrets.** They travel in every OAuth request and are trivially
discoverable from your domain:

```
https://login.microsoftonline.com/<your-domain>/.well-known/openid-configuration
```

reveals the tenant. A distributed CLI is, by definition, a **public client** — it
cannot safely hold a client *secret*, which is exactly why `ccauth` uses PKCE
(RFC 8252). So hiding these IDs is not a security boundary.

The legitimate goals are real, though, and `ccauth` supports all of them:

- **UX** — users shouldn't have to know or type these values.
- **Central control** — IT sets them once and can rotate/change them without every
  user editing files.
- **Consistency** — no drift between employees' configs.

All three are met by the **layered configuration** below.

---

## Configuration layers

`ccauth` merges configuration from four layers. Higher layers override lower ones
on same-named profiles:

| Layer | Location | Owner | Overrides |
|---|---|---|---|
| `user` | `~/.config/ccauth/config.toml` | end user | lowest |
| `embedded` | compiled into the binary | IT (build time) | user |
| `managed` | `/etc/ccauth/config.toml` (macOS/Linux) · `%ProgramData%\ccauth\config.toml` (Windows) | IT (MDM) | user, embedded |
| `remote` | cached fetch of `config_url` | IT (central server) | all |

The user layer holds only personal profiles (or nothing). The **session** — the
refresh token — is always per-user in the OS keychain, regardless of where config
comes from.

Inspect the layers on any machine:

```bash
ccauth config path     # which layers exist
ccauth config show     # merged effective config + per-profile source (secrets redacted)
ccauth status          # active profile + "Config source: managed"
```

### Lockdown

An IT layer can forbid personal profiles entirely:

```toml
allow_user_profiles = false
```

With this set, `ccauth setup` refuses and only IT-provisioned profiles are usable.

---

## Distribution option 1 — Managed config via MDM (recommended)

Push a `config.toml` to the managed path with your MDM (Intune, Jamf, Workspace
ONE, Ansible, etc.). Users never touch it.

`/etc/ccauth/config.toml` (macOS/Linux) or `%ProgramData%\ccauth\config.toml`
(Windows):

```toml
default_profile     = "corp"
allow_user_profiles = false          # optional lockdown

[profiles.corp]
provider = "entra"
gateway  = "litellm"
mode     = "passthrough"
  [profiles.corp.oauth]
  tenant_id = "<tenant-guid>"
  client_id = "<public-client-app-id>"
  scopes    = ["api://<gateway-app-id>/.default"]
  [profiles.corp.gateway_opts]
  base_url = "https://llm-gateway.corp.example.com"
  ttl_ms   = 3000000
```

- **Intune (Windows):** deploy the file via a Configuration Profile / Win32 app to
  `%ProgramData%\ccauth\`.
- **Jamf (macOS):** a package or a Files-and-Processes policy placing it at
  `/etc/ccauth/config.toml`.
- Override the path with `CCAUTH_MANAGED_CONFIG` if you prefer another location.

---

## Distribution option 2 — Remote config URL (central rotation)

Bake only a **pointer** into the managed/embedded layer and host the real config
centrally, so you can change tenant/gateway/scope without touching devices.

Managed (or embedded) layer:

```toml
config_url = "https://ccauth.corp.example.com/config.toml"
```

The device pulls and caches it:

```bash
ccauth config sync     # fetch config_url -> ~/.local/state/ccauth/remote-config.toml
```

`ccauth config sync` runs on `login`/setup, or wire it into MDM to run
periodically. The hot path (`ccauth token`) reads the **cached** file only — it
never blocks on the network for config. `CCAUTH_CONFIG_URL` overrides the URL.

---

## Distribution option 3 — Compile-time embedded (branded binary)

For a fully self-contained binary with zero external config: put your org config
in [`internal/config/defaults.toml`](internal/config/defaults.toml) and rebuild.

```toml
# internal/config/defaults.toml
config_url          = "https://ccauth.corp.example.com/config.toml"
allow_user_profiles = false
default_profile     = "corp"
[profiles.corp]
provider = "entra"
gateway  = "litellm"
  [profiles.corp.oauth]
  tenant_id = "..."
  client_id = "..."
  scopes    = ["api://.../.default"]
  [profiles.corp.gateway_opts]
  base_url = "https://llm-gateway.corp.example.com"
```

```bash
make cross     # dist/ccauth-<os>-<arch> — distribute via your package channel
```

---

## Packaging

Any of these compose with the options above:

- **Homebrew tap** (`brew install yourorg/tap/ccauth`) with a formula whose
  `post_install` drops the managed config.
- **`.pkg` / `.msi` / `.deb`** with a postinstall script that writes the managed
  config and puts `ccauth` on `PATH`.
- **MDM app deployment** of the static binary plus a separate managed-config
  profile.

The Claude Code wiring itself can also be distributed centrally: Claude Code reads
[managed settings](https://code.claude.com/docs/en/settings), so IT can push
`apiKeyHelper` + `ANTHROPIC_BASE_URL` there. Or have `ccauth` write it locally:

```bash
ccauth wire --write     # merges apiKeyHelper + env into ~/.claude/settings.json
```

---

## Interactive re-authentication

By default (`helper_interactive = true`), `ccauth token` — the command Claude Code
calls — will **open a browser to re-authenticate on its own** when the refresh
token has expired or been revoked. Users don't drop back to a terminal.

- **Common case:** the refresh token is valid (Entra: rolling, up to ~90 days;
  Google published apps: until revoked), so the helper refreshes **silently** —
  no browser.
- **When re-auth is needed:** the helper opens the system browser, catches the
  loopback redirect, stores the new session, and returns the token. A file lock
  ensures concurrent requests open **one** tab, not many; a 3-minute timeout means
  a stalled login fails cleanly instead of hanging Claude Code forever.
- **Headless/CI:** if no browser is available (SSH without X11, no display), or
  `flow = device_code`, or `CCAUTH_NONINTERACTIVE=1`/`CI=1`, the helper instead
  exits non-zero telling the user to run `ccauth login` (or `--device`).
- **Disable per profile:** `helper_interactive = false`.

`ccauth login` remains the explicit path for first-time setup, reconfiguration, or
testing a new auth config.

---

## Identity-provider setup (one-time, IT)

What the app registration needs so the tokens validate at the gateway:

**Microsoft Entra ID**
- App registration with **"Allow public client flows" = Yes**.
- Platform **Mobile & desktop applications**, redirect URI **`http://localhost`**
  (Entra allows any loopback port at runtime).
- A separate app registration for the **gateway API** with **Expose an API** → an
  Application ID URI (`api://<gateway-app-id>`) and a scope. Grant the CLI app that
  delegated scope. Tokens then carry `aud = api://<gateway-app-id>` (or the API's
  client-ID GUID for v2), which the gateway validates via JWKS.
- Device-code flow may be blocked by Conditional Access — prefer `auth_code`.

**Google Workspace**
- OAuth client of type **Desktop app** (client ID + non-confidential secret).
- **Publish the consent screen** (unpublished "Testing" apps get 7-day refresh
  tokens).
- Profiles emit the **ID token** (`aud` = the client ID); access tokens are opaque.

**Generic OIDC (Okta/Auth0/Keycloak)**
- Public client with PKCE + loopback redirect (`http://localhost`).
- Provide the `issuer`; `ccauth` discovers endpoints and JWKS automatically.

---

## Roadmap: true zero-touch on managed devices

On Entra-joined / MDM-managed devices, a brokered flow (WAM / Primary Refresh
Token) can obtain tokens with **no prompt at all**. That is the ultimate
"user does nothing" experience and is a natural future addition to the login
engine; today's model (silent refresh + auto browser on expiry) already keeps
prompts rare.
