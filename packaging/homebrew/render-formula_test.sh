#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 bitkaio LLC
#
# Offline regression test for render-formula.sh: renders the formula from
# fixture `brew bottle --json` outputs and checks the bottle block.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "${tmp:?}"' EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

# One fixture per tag, shaped like `brew bottle --json` output.
fixture() { # cellar tag digest-char
  printf '{"palenaai/tap/ccauth":{"bottle":{"cellar":"%s","tags":{"%s":{"sha256":"%s"}}}}}' \
    "$1" "$2" "$(printf '%064d' 0 | tr 0 "$3")" > "$tmp/$2.json"
}
fixture any_skip_relocation x86_64_linux c
fixture any_skip_relocation arm64_sequoia a
fixture any arm64_linux d
fixture /opt/homebrew/Cellar arm64_tahoe b

export SOURCE_SHA256=0000000000000000000000000000000000000000000000000000000000000000
"$here/render-formula.sh" v1.2.3 "$tmp/with.rb" "$tmp"/*.json
"$here/render-formula.sh" v1.2.3 "$tmp/without.rb"

f="$tmp/with.rb"
grep -q 'url "https://github.com/PalenaAI/claude-code-auth-helper/archive/refs/tags/v1.2.3.tar.gz"' "$f" || fail "version not rendered"
grep -q "sha256 \"$SOURCE_SHA256\"" "$f" || fail "source sha256 not rendered"
grep -q 'root_url "https://github.com/PalenaAI/claude-code-auth-helper/releases/download/v1.2.3"' "$f" || fail "root_url"
# Relocatable cellars must be Ruby symbols; a quoted string makes Homebrew
# treat the bottle as incompatible and build from source.
grep -Eq 'cellar: :any_skip_relocation, +arm64_sequoia: +"a{64}"' "$f" || fail "arm64_sequoia must use a :any_skip_relocation symbol"
grep -Eq 'cellar: :any, +arm64_linux: +"d{64}"' "$f" || fail "arm64_linux must use an :any symbol"
grep -Eq 'cellar: "/opt/homebrew/Cellar", +arm64_tahoe:' "$f" || fail "a real cellar path must stay quoted"
grep -q 'cellar: "any' "$f" && fail "relocatable cellar rendered as a string"
# Homebrew's order: ARM macOS, then ARM Linux, then Intel Linux.
order=$(grep -oE '(arm64_sequoia|arm64_tahoe|arm64_linux|x86_64_linux):' "$f" | tr -d ':' | tr '\n' ' ')
[ "$order" = "arm64_sequoia arm64_tahoe arm64_linux x86_64_linux " ] || fail "bottle order: $order"
grep -q '__' "$f" && fail "unrendered placeholder left"

w="$tmp/without.rb"
grep -q 'bottle do' "$w" && fail "bottle block without bottles"
grep -q '__BOTTLE__' "$w" && fail "marker left in source-only formula"
if "$here/render-formula.sh" 'v1;rm -rf /' "$tmp/x.rb" 2>/dev/null; then fail "accepted a non-release tag"; fi

echo "OK: render-formula.sh"
