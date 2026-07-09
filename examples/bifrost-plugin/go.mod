// Separate module so it is excluded from the parent `go build ./...`.
// Build/adapt this alongside your Bifrost deployment.
module github.com/PalenaAI/claude-code-auth-helper/examples/bifrost-plugin

go 1.25.0

require github.com/coreos/go-oidc/v3 v3.20.0

require (
	github.com/go-jose/go-jose/v4 v4.1.4 // indirect
	golang.org/x/oauth2 v0.36.0 // indirect
)
