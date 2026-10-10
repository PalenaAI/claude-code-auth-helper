# Template for the Homebrew formula published to PalenaAI/homebrew-tap.
# .github/workflows/release.yml renders it with render-formula.sh on each tag: it
# fills in the version and sha256, adds bottles built on CI runners, and pushes
# the result to the tap. Bottles mean users never build locally, so installs
# work regardless of the user's Command Line Tools or macOS version. The source
# build stays as the fallback for platforms without a bottle.
class Ccauth < Formula
  desc "OAuth2/SSO credential helper for Claude Code and AI gateways"
  homepage "https://github.com/PalenaAI/claude-code-auth-helper"
  url "https://github.com/PalenaAI/claude-code-auth-helper/archive/refs/tags/v__VERSION__.tar.gz"
  sha256 "__SHA256__"
  license "Apache-2.0"
  head "https://github.com/PalenaAI/claude-code-auth-helper.git", branch: "main"

  # __BOTTLE__ (replaced by the release workflow with prebuilt bottles)

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=v#{version}"), "./cmd/ccauth"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ccauth --version")
  end
end
