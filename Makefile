.PHONY: all build test test-unit test-integration test-integration-quick test-packages test-packages-docker clean install uninstall fmt deps help lint lint-version-check tidy-check vet fmt-check docs-check verify-static verify check vuln test-race build-cross ci svu-version-check bump

GO ?= go
GOFMT ?= gofmt
# Tool pins come from mise.toml, the single source of every tool pin (core
# ADR-0043): `mise install` provisions them locally and in CI. Never edit
# these lines; bump the pin in mise.toml.
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))
SVU_VERSION := $(strip $(shell sed -n 's/^svu = "\(.*\)"/\1/p' mise.toml))
SVU ?= svu
# go.mod's toolchain line, the only Go pin. golangci-lint must be built with a
# Go at least this new.
GO_TOOLCHAIN := $(strip $(shell sed -n 's/^toolchain go\(.*\)/\1/p' go.mod))
# govulncheck release; the Go checksum database verifies the module.
GOVULNCHECK_VERSION := v1.9.0

# Default target
all: build

# Build the repogen binary
build:
	@echo "Building repogen..."
	@go build -o repogen ./cmd/repogen
	@echo "✓ Build complete: ./repogen"

# Run all tests
test: test-unit test-integration

# Run unit tests only
test-unit:
	@echo "Running unit tests..."
	@go test -v -short ./...

# Build test packages (requires Docker or native tools)
test-packages:
	@echo "Building test packages..."
	@chmod +x test/build-test-packages.sh
	@./test/build-test-packages.sh

# Build test packages using Docker (if native tools not available)
test-packages-docker:
	@echo "Building test packages in Docker..."
	@chmod +x test/build-test-packages.sh
	@echo "Building Debian packages..."
	@docker run --rm -v $(PWD):/work -w /work debian:bookworm bash -c "apt-get update -qq && apt-get install -y -qq dpkg-dev > /dev/null 2>&1 && ./test/build-test-packages.sh"
	@echo "✓ All test packages built successfully"

# Run integration tests (requires Docker)
test-integration: test-packages
	@echo "Running integration tests..."
	@go test -v -timeout 15m ./test

# Quick integration test (skip if Docker not available)
test-integration-quick:
	@echo "Running integration tests (quick)..."
	@go test -v -timeout 10m -short ./test || echo "Integration tests skipped"

# Clean build artifacts and test outputs
clean:
	@echo "Cleaning..."
	@rm -f repogen
	@rm -rf test/integration-output
	@rm -rf test/fixtures/deb-build test/fixtures/apk-build test/fixtures/bottle-build
	@echo "✓ Clean complete"

# Install to system
install: build
	@echo "Installing repogen to /usr/local/bin..."
	@sudo cp repogen /usr/local/bin/
	@echo "✓ Installation complete"

# Uninstall from system
uninstall:
	@echo "Removing repogen from /usr/local/bin..."
	@sudo rm -f /usr/local/bin/repogen
	@echo "✓ Uninstall complete"

# Run golangci-lint at the mise.toml pin (no tool is optional, core ADR-0043)
lint: lint-version-check
	@echo "Running linter..."
	golangci-lint run ./...

# Fail unless the installed golangci-lint is the mise.toml pin, built with a Go no older than go.mod's toolchain
lint-version-check:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (not installed; run: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (run: mise install)"; exit 1; \
	fi; \
	built="$$(golangci-lint version 2>/dev/null | sed -n 's/.*built with go\([0-9.]*\).*/\1/p')"; \
	if [ -n "$$built" ] && [ "$$(printf '%s\n%s\n' "$(GO_TOOLCHAIN)" "$$built" | sort -V | head -1)" != "$(GO_TOOLCHAIN)" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) was built with go$$built, older than go.mod's toolchain go$(GO_TOOLCHAIN)"; exit 1; \
	fi

# --- Gate triad (core ADR-0043, ADR-0044) ---

# go.mod and go.sum are tidy (no write)
tidy-check:
	$(GO) mod tidy -diff

# go vet
vet:
	$(GO) vet ./...

# Every tracked Go file is gofmt-clean (no write)
fmt-check:
	@test -z "$$($(GOFMT) -l $$(git ls-files '*.go'))" || { $(GOFMT) -l $$(git ls-files '*.go'); echo "run: make fmt"; exit 1; }

# Docs integrity: index, links, symlinks (scripts/check-docs.mjs)
docs-check:
	node scripts/check-docs.mjs

# verify-static: the non-mutating static checks
verify-static: tidy-check vet fmt-check lint docs-check

# verify: credential-free, non-mutating gate (what a read-only reviewer runs)
verify: verify-static test-unit

# check: the developer gate; it may format
check: fmt verify

# Reachable known vulnerabilities fail the gate (ADR-0016)
vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# Unit tests with the race detector and a coverage profile (coverage.txt)
test-race:
	$(GO) test -v -short -race -coverprofile=coverage.txt ./...

# The release builds: linux amd64 and arm64
build-cross:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -o /dev/null ./cmd/repogen
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -o /dev/null ./cmd/repogen

# ci: what CI runs (.github/workflows/ci.yml). Needs Docker for the test packages and the integration tests.
ci: verify vuln test-race test-packages-docker test-integration build-cross
	@echo "==> CI gate passed"

# Fail unless the installed svu is the mise.toml pin
svu-version-check:
	@test -n "$(SVU_VERSION)" || { echo "mise.toml pins no svu"; exit 1; }
	@command -v "$(SVU)" >/dev/null 2>&1 || { echo "svu $(SVU_VERSION) is required (not installed; run: mise install)"; exit 1; }
	@installed="$$($(SVU) --version 2>&1 | sed -n 's/^GitVersion:[[:space:]]*//p' | head -1)"; \
	if [ "$$installed" != "$(SVU_VERSION)" ]; then echo "expected svu $(SVU_VERSION), found $${installed:-unknown} (run: mise install)"; exit 1; fi

# bump: create the next annotated release tag with svu (.svu.yml); a maintainer pushes it
bump: svu-version-check
	@set -e; \
	test -z "$$(git status --porcelain)" || { echo "the working tree is not clean"; exit 1; }; \
	version="$$($(SVU) next)"; \
	case "$$version" in v0.*) ;; *) echo "svu next gave '$$version', not v0.x: check tag.pattern in .svu.yml"; exit 1;; esac; \
	git tag -a "$$version" -m "Release version $${version#v}"; \
	echo "tagged $$version; push it with: git push origin $$version"

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@echo "✓ Format complete"

# Update dependencies
deps:
	@echo "Updating dependencies..."
	@go mod tidy
	@go mod download
	@echo "✓ Dependencies updated"

# Display help
help:
	@echo "Repogen Makefile targets:"
	@echo ""
	@echo "  make build                 - Build the repogen binary"
	@echo "  make test                  - Run all tests"
	@echo "  make test-unit             - Run unit tests only"
	@echo "  make test-integration      - Run Docker-based integration tests"
	@echo "  make test-integration-quick - Run integration tests in short mode"
	@echo "  make test-packages         - Build test packages for integration tests"
	@echo "  make test-packages-docker  - Build test packages using Docker"
	@echo "  make test-race             - Unit tests with the race detector and coverage.txt"
	@echo "  make clean                 - Clean build artifacts"
	@echo "  make install               - Install to /usr/local/bin"
	@echo "  make uninstall             - Remove from /usr/local/bin"
	@echo "  make lint                  - Run golangci-lint at the mise.toml pin"
	@echo "  make lint-version-check    - Check golangci-lint matches the mise.toml pin"
	@echo "  make tidy-check            - Check go.mod and go.sum are tidy (no write)"
	@echo "  make vet                   - Run go vet"
	@echo "  make fmt-check             - Check every tracked Go file is gofmt-clean"
	@echo "  make docs-check            - Check the docs index, links and symlinks"
	@echo "  make verify-static         - Non-mutating static checks (tidy, vet, fmt, lint, docs)"
	@echo "  make verify                - verify-static plus the unit tests (no write)"
	@echo "  make check                 - Format, then verify (the developer gate)"
	@echo "  make vuln                  - Run govulncheck"
	@echo "  make build-cross           - Build the linux amd64 and arm64 release binaries"
	@echo "  make ci                    - What CI runs (needs Docker)"
	@echo "  make svu-version-check     - Check svu matches the mise.toml pin"
	@echo "  make bump                  - Create the next annotated release tag (maintainers only)"
	@echo "  make fmt                   - Format code"
	@echo "  make deps                  - Update dependencies"
	@echo "  make help                  - Show this help message"
	@echo ""
	@echo "Requirements for integration tests:"
	@echo "  - Docker (for running tests in containers)"
	@echo "  - dpkg-deb (for building Debian packages)"
	@echo ""
	@echo "Quick start:"
	@echo "  make build          # Build the binary"
	@echo "  make test-packages  # Build test packages"
	@echo "  make test           # Run all tests"
