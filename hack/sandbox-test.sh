#!/usr/bin/env bash
# Isolated end-to-end test for ccauth against a real Entra + LiteLLM setup.
#
# Touches NOTHING in ~/.claude or your OS keychain — config, the login session,
# and (optionally) an isolated Claude Code all live in a throwaway temp dir.
# Clean up any time with:  rm -rf "$SANDBOX"
#
# Provide these from your Entra app + LiteLLM gateway (export or inline):
#   TENANT_ID    Entra directory (tenant) GUID
#   CLIENT_ID    Entra app registration (client) ID  [public client + PKCE]
#   API_SCOPE    e.g. api://<gateway-app-id>/.default  (token aud the gateway checks)
#   GATEWAY_URL  e.g. http://localhost:4000            (LiteLLM proxy root)
#   MODEL        (optional) a model your LiteLLM serves, to auto-run the curl test
#
# Usage:
#   TENANT_ID=... CLIENT_ID=... API_SCOPE=... GATEWAY_URL=... MODEL=... \
#     ./hack/sandbox-test.sh
set -uo pipefail

: "${TENANT_ID:?set TENANT_ID}"; : "${CLIENT_ID:?set CLIENT_ID}"
: "${API_SCOPE:?set API_SCOPE}"; : "${GATEWAY_URL:?set GATEWAY_URL}"
MODEL="${MODEL:-}"
REDIRECT_PORT="${REDIRECT_PORT:-8765}"   # pinned loopback port — register http://localhost:<port>/callback in your IdP
CCAUTH="${CCAUTH:-$(cd "$(dirname "$0")/.." && pwd)/ccauth}"

SANDBOX="${SANDBOX:-$(mktemp -d -t ccauth-sandbox.XXXX)}"
export CCAUTH_CONFIG_DIR="$SANDBOX/config" CCAUTH_STATE_DIR="$SANDBOX/state"
mkdir -p "$CCAUTH_CONFIG_DIR" "$CCAUTH_STATE_DIR"

cat > "$CCAUTH_CONFIG_DIR/config.toml" <<EOF
default_profile = "test"
[profiles.test]
provider = "entra"
gateway  = "litellm"
mode     = "passthrough"
store    = "file"            # session stays in the sandbox, not your OS keychain
  [profiles.test.oauth]
  tenant_id = "$TENANT_ID"
  client_id = "$CLIENT_ID"
  scopes        = ["$API_SCOPE"]
  flow          = "auth_code"
  redirect_port = $REDIRECT_PORT
  [profiles.test.gateway_opts]
  base_url = "$GATEWAY_URL"
EOF

echo "▶ sandbox:  $SANDBOX"
echo "▶ ccauth:   $CCAUTH"
echo "▶ redirect: register this EXACT URI in your IdP app → http://localhost:$REDIRECT_PORT/callback"
"$CCAUTH" status || true

echo; echo "▶ ccauth login (a browser window will open — sign in with your work account)…"
"$CCAUTH" login || { echo "login failed — check the Entra app has 'Allow public client flows' = Yes and a http://localhost redirect"; exit 1; }

TOKEN="$("$CCAUTH" token)" || { echo "ccauth token failed"; exit 1; }

echo; echo "▶ token claims — confirm 'aud'/'iss' match your LiteLLM JWT_AUDIENCE / JWT_ISSUER:"
payload="$(printf '%s' "$TOKEN" | cut -d. -f2)"
case $(( ${#payload} % 4 )) in 2) payload="$payload==";; 3) payload="$payload=";; esac
printf '%s' "$payload" | tr '_-' '/+' | base64 -d 2>/dev/null \
  | { jq '{aud, iss, scp, roles, exp}' 2>/dev/null || cat; } || true

echo; echo "▶ curl the gateway with the SSO token (the decisive test — no Claude Code involved):"
if [ -n "$MODEL" ]; then
  curl -sS "$GATEWAY_URL/v1/messages" \
    -H "Authorization: Bearer $TOKEN" \
    -H "anthropic-version: 2023-06-01" \
    -H "content-type: application/json" \
    -d "{\"model\":\"$MODEL\",\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"say hi in three words\"}]}" \
    | { jq . 2>/dev/null || cat; }
  echo "  (a JSON reply starting with \"id\":\"msg_ = the whole SSO→gateway chain works)"
else
  echo "  set MODEL=<a model your LiteLLM serves> to auto-run it, or run manually:"
  echo "    curl -sS \"$GATEWAY_URL/v1/messages\" -H \"Authorization: Bearer \$(CCAUTH_CONFIG_DIR=$CCAUTH_CONFIG_DIR CCAUTH_STATE_DIR=$CCAUTH_STATE_DIR $CCAUTH token)\" \\"
  echo "      -H 'anthropic-version: 2023-06-01' -H 'content-type: application/json' \\"
  echo "      -d '{\"model\":\"<MODEL>\",\"max_tokens\":16,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}'"
fi

# Wire up an isolated Claude Code that reuses this sandbox session.
CLAUDE_DIR="$SANDBOX/claude"; mkdir -p "$CLAUDE_DIR"
{
  printf '{\n  "apiKeyHelper": "%s token --profile test",\n  "env": {\n' "$CCAUTH"
  printf '    "ANTHROPIC_BASE_URL": "%s",\n' "$GATEWAY_URL"
  # Pin the model so Claude Code asks for one the gateway actually serves.
  # Both point at MODEL — a single-model gateway has no separate small/fast model.
  [ -n "$MODEL" ] && printf '    "ANTHROPIC_MODEL": "%s",\n    "ANTHROPIC_SMALL_FAST_MODEL": "%s",\n' "$MODEL" "$MODEL"
  printf '    "CCAUTH_CONFIG_DIR": "%s",\n    "CCAUTH_STATE_DIR": "%s"\n  }\n}\n' "$CCAUTH_CONFIG_DIR" "$CCAUTH_STATE_DIR"
} > "$CLAUDE_DIR/settings.json"

cat <<EOF

▶ Full integration test in an ISOLATED Claude Code (does not touch ~/.claude):

    CLAUDE_CONFIG_DIR="$CLAUDE_DIR" claude

  It uses ccauth as the apiKeyHelper against your gateway. Ask it anything; each
  request pulls a fresh token via ccauth. Your normal Claude Code is untouched.

▶ When finished, delete the whole sandbox:

    rm -rf "$SANDBOX"
EOF
