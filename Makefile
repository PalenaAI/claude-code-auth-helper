BINARY  := ccauth
PKG     := ./cmd/ccauth
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Release metadata bundled alongside every distributed artifact (Apache-2.0 §4).
DIST_DOCS := LICENSE NOTICE README.md

# GOOS/GOARCH targets built by `make cross`.
PLATFORMS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/amd64

.PHONY: build install test vet tidy fmt license-check clean cross package checksums

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)

install:
	CGO_ENABLED=0 go install -ldflags "$(LDFLAGS)" $(PKG)

test:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

# Verify every Go source file carries the Apache SPDX header. Portable and
# dependency-free; CI additionally runs apache/skywalking-eyes against .licenserc.yaml.
license-check:
	@missing=0; \
	for f in $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go'); do \
		head -3 "$$f" | grep -q 'SPDX-License-Identifier: Apache-2.0' || { echo "missing header: $$f"; missing=1; }; \
	done; \
	if [ $$missing -eq 0 ]; then echo "OK: all Go files carry the Apache SPDX header"; else exit 1; fi

clean:
	rm -rf $(BINARY) dist

# Cross-compile the static binaries most orgs need to distribute.
cross:
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		out="dist/$(BINARY)-$$os-$$arch$$ext"; \
		echo "  building $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o "$$out" $(PKG) || exit 1; \
	done

# Full release bundle: cross-compiled binaries + license/notice + checksums.
package: cross
	@cp $(DIST_DOCS) dist/
	@$(MAKE) --no-print-directory checksums
	@echo "Release artifacts in dist/ (binaries, $(DIST_DOCS), SHA256SUMS)"

# Generate SHA-256 checksums for every distributable file so downloads can be
# verified with `shasum -a 256 -c SHA256SUMS`.
checksums:
	@cd dist && rm -f SHA256SUMS && \
		( command -v sha256sum >/dev/null 2>&1 && sha256sum * > SHA256SUMS \
		  || shasum -a 256 * > SHA256SUMS ) && \
		echo "  wrote dist/SHA256SUMS"
