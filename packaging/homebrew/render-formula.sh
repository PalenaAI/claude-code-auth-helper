#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 bitkaio LLC
#
# Render the Homebrew formula for a release tag.
#
#   render-formula.sh <tag> <out.rb> [bottle.json ...]
#
# Fills the version and source-archive sha256 into packaging/homebrew/ccauth.rb.
# Given `brew bottle --json` outputs, it also writes the `bottle do` block so
# users get a prebuilt bottle instead of a source build. A source build needs
# Command Line Tools that match the user's macOS; a bottle does not.
set -euo pipefail

tag=${1:?usage: render-formula.sh <tag> <out.rb> [bottle.json ...]}
out=${2:?usage: render-formula.sh <tag> <out.rb> [bottle.json ...]}
shift 2

# The tag can come from a manual workflow input; accept only release tags.
if ! [[ $tag =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$ ]]; then
  echo "render-formula.sh: not a release tag: $tag" >&2
  exit 1
fi

repo=${GITHUB_REPOSITORY:-PalenaAI/claude-code-auth-helper}
here=$(cd "$(dirname "$0")" && pwd)
version=${tag#v}
sha=$(curl -fsSL "https://github.com/${repo}/archive/refs/tags/${tag}.tar.gz" | shasum -a 256 | cut -d' ' -f1)

bottle=""
if [ "$#" -gt 0 ]; then
  # One `sha256 cellar: …, <tag>: "…"` line per bottle, laid out the way
  # `brew bottle --merge` writes them (brew style checks both): ARM macOS, Intel
  # macOS, ARM Linux, Intel Linux, with tags and digests aligned in columns.
  lines=$(jq -r '.[] | .bottle as $b | $b.tags | to_entries[] |
      [(if ($b.cellar | startswith(":")) then $b.cellar else ($b.cellar | tojson) end), .key, .value.sha256] | @tsv' "$@" |
    awk -F'\t' '
      { rank = ($2 ~ /linux/ ? 2 : 0) + ($2 ~ /^arm64_/ ? 0 : 1)
        n++; c[n] = "cellar: " $1 ","; t[n] = $2 ":"; d[n] = $3; r[n] = rank
        if (length(c[n]) > cw) cw = length(c[n]); if (length(t[n]) > tw) tw = length(t[n]) }
      END { for (k = 0; k <= 3; k++) for (i = 1; i <= n; i++) if (r[i] == k)
              printf "    sha256 %-*s %-*s \"%s\"\n", cw, c[i], tw, t[i], d[i] }')
  bottle=$(printf '  bottle do\n    root_url "https://github.com/%s/releases/download/%s"\n%s\n  end\n' "$repo" "$tag" "$lines")
fi

# The bottle block is multi-line, so it travels via the environment (BSD awk
# rejects newlines in -v values). Without bottles, the marker line and the blank
# line after it are dropped.
BOTTLE="$bottle" awk -v version="$version" -v sha="$sha" '
  skip { skip = 0; if ($0 == "") next }
  /__BOTTLE__/ { if (ENVIRON["BOTTLE"] != "") print ENVIRON["BOTTLE"]; else skip = 1; next }
  { gsub(/__VERSION__/, version); gsub(/__SHA256__/, sha); print }
' "$here/ccauth.rb" > "$out"
