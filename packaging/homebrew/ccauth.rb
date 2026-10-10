# Template for the Homebrew formula published to PalenaAI/homebrew-tap.
# .github/workflows/release.yml fills in the version and sha256 on each tag and
# pushes the rendered file to the tap. Build-from-source so it stays eligible for
# a future homebrew-core submission.
class Ccauth < Formula
  desc "OAuth2/SSO credential helper for Claude Code and AI gateways"
  homepage "https://github.com/PalenaAI/claude-code-auth-helper"
  url "https://github.com/PalenaAI/claude-code-auth-helper/archive/refs/tags/v__VERSION__.tar.gz"
  sha256 "__SHA256__"
  license "Apache-2.0"
  head "https://github.com/PalenaAI/claude-code-auth-helper.git", branch: "main"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=v#{version}"), "./cmd/ccauth"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/ccauth --version")
  end
end
