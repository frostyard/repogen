.PHONY: all build test test-unit test-integration test-integration-quick test-packages test-packages-docker clean install uninstall fmt deps help lint lint-version-check tidy-check vet fmt-check docs-check corrections-check corrections-base verify-static verify check vuln test-race build-cross ci svu-version-check bump-main-check bump site site-serve

GO ?= go
GOFMT ?= gofmt
# Tool pins come from mise.toml, the single source of every tool pin (core
# ADR-0043): `mise install` provisions them locally and in CI. Never edit
# these lines; bump the pin in mise.toml.
GOLANGCI_LINT_VERSION := $(strip $(shell sed -n 's/^golangci-lint = "\(.*\)"/\1/p' mise.toml))
SVU_VERSION := $(strip $(shell sed -n 's/^svu = "\(.*\)"/\1/p' mise.toml))
SVU ?= svu
# go.mod's go line (a full 1.MINOR.PATCH version), the only Go pin.
# golangci-lint must be built with a Go of at least this minor.
GO_MOD_VERSION := $(strip $(shell sed -n 's/^go \(.*\)/\1/p' go.mod))
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

# Integration tests without the package rebuild: uses the packages already in test/fixtures (requires Docker)
test-integration-quick:
	@echo "Running integration tests (quick: no package rebuild)..."
	@$(GO) test -v -timeout 15m ./test

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

# Fail unless the installed golangci-lint is the mise.toml pin, built with a Go
# whose major.minor is no older than go.mod's (a patch difference does not change gofmt output)
lint-version-check:
	@test -n "$(GOLANGCI_LINT_VERSION)" || { echo "mise.toml pins no golangci-lint"; exit 1; }
	@installed="$$(golangci-lint version --short 2>/dev/null)" || { \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) is required (not installed; run: mise install)"; exit 1; }; \
	if [ "$$installed" != "$(GOLANGCI_LINT_VERSION)" ]; then \
		echo "expected golangci-lint $(GOLANGCI_LINT_VERSION), found $$installed (run: mise install)"; exit 1; \
	fi; \
	built="$$(golangci-lint version 2>/dev/null | sed -n 's/.*built with go\([0-9.]*\).*/\1/p')"; \
	built_minor="$$(printf '%s' "$$built" | cut -d. -f1,2)"; \
	module_minor="$$(printf '%s' "$(GO_MOD_VERSION)" | cut -d. -f1,2)"; \
	if [ -n "$$built" ] && [ "$$(printf '%s\n%s\n' "$$module_minor" "$$built_minor" | sort -V | head -1)" != "$$module_minor" ]; then \
		echo "golangci-lint $(GOLANGCI_LINT_VERSION) was built with go$$built, older than go.mod's go$(GO_MOD_VERSION): bump golangci-lint first"; exit 1; \
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

# The documentation site (site/), built with pinned ProperDocs and MaterialX
# (core ADR-0070, the frostyard-docs-site skill). `make ci` does not build the
# site, so a change that does not touch site/ needs no Python toolchain;
# .github/workflows/site.yml builds it.
SITE_VENV := build/site-venv

$(SITE_VENV)/bin/properdocs: site/requirements.txt
	python3 -m venv $(SITE_VENV)
	$(SITE_VENV)/bin/pip install -q --require-hashes -r site/requirements.txt
	touch $@

site: $(SITE_VENV)/bin/properdocs
	$(SITE_VENV)/bin/properdocs build --strict -f site/properdocs.yml

site-serve: $(SITE_VENV)/bin/properdocs
	$(SITE_VENV)/bin/properdocs serve -f site/properdocs.yml

# Docs integrity: index, links, symlinks (scripts/check-docs.mjs)
docs-check:
	node scripts/check-docs.mjs

# The corrections inbox (core ADR-0018, ADR-0081): the format of every record
# in .memory/corrections/ (scripts/check-corrections.mjs, a byte-identical copy
# of core's script), with no comparison against a base
corrections-check:
	node scripts/check-corrections.mjs

# The corrections check compares the record files in .memory/corrections/ with
# BASE: no record is removed, renamed or changed, except that an empty
# promoted_to may be filled in once. The old single file must not come back.
# The contract starts with the gate: a BASE that does not hold
# scripts/check-corrections.mjs had no such rule, so the comparison is skipped
# with a notice. Every BASE after the adoption holds the script.
#
# A BASE that does not resolve to a commit is an error, never an old base: a
# typo or an unfetched ref must not turn the comparison into a silent pass.
corrections-base:
	@if [ -z "$(BASE)" ]; then exit 0; fi; \
	if ! git rev-parse --verify --quiet "$(BASE)^{commit}" >/dev/null; then \
		echo "FAIL: BASE $(BASE) does not resolve to a commit (fetch it first)"; \
		exit 1; \
	fi; \
	if git cat-file -e "$(BASE):scripts/check-corrections.mjs" 2>/dev/null; then \
		node scripts/check-corrections.mjs --base "$(BASE)"; \
	else \
		echo "==> corrections: BASE $(BASE) predates the corrections gate; append-only comparison skipped"; \
	fi

# verify-static: the non-mutating static checks
verify-static: tidy-check vet fmt-check lint docs-check corrections-check

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
# corrections-base runs here, with BASE set by the CI workflow to the pull
# request's base commit (empty on a push or a merge-group event).
ci: verify corrections-base vuln test-race test-packages-docker test-integration build-cross
	@echo "==> CI gate passed"

# Fail unless the installed svu is the mise.toml pin
svu-version-check:
	@test -n "$(SVU_VERSION)" || { echo "mise.toml pins no svu"; exit 1; }
	@command -v "$(SVU)" >/dev/null 2>&1 || { echo "svu $(SVU_VERSION) is required (not installed; run: mise install)"; exit 1; }
	@installed="$$($(SVU) --version 2>&1 | sed -n 's/^GitVersion:[[:space:]]*//p' | head -1)"; \
	if [ "$$installed" != "$(SVU_VERSION)" ]; then echo "expected svu $(SVU_VERSION), found $${installed:-unknown} (run: mise install)"; exit 1; fi

# bump-main-check: fail unless HEAD is the main branch and equals origin/main (core ADR-0012)
bump-main-check:
	@git rev-parse --git-dir >/dev/null 2>&1 || { echo "make bump must run in a git repository (core ADR-0012); $(CURDIR) is not in one: clone frostyard/repogen and run it there"; exit 1; }; \
	branch="$$(git symbolic-ref --quiet --short HEAD)" || branch="detached HEAD"; \
	if [ "$$branch" != "main" ]; then \
		echo "make bump must run on main (core ADR-0012); found $$branch, not main: check out main and update it first"; \
		exit 1; \
	fi; \
	url="$$(git remote get-url origin 2>/dev/null)" || { echo "make bump cannot fetch origin main: this repository has no remote named origin"; exit 1; }; \
	echo "fetching main from origin ($$url)"; \
	if ! git fetch --quiet origin +refs/heads/main:refs/remotes/origin/main; then \
		echo "make bump cannot fetch origin main from $$url; check the remote and your network access, then retry"; \
		exit 1; \
	fi; \
	head="$$(git rev-parse HEAD)" && remote="$$(git rev-parse refs/remotes/origin/main)" || exit 1; \
	if [ "$$head" != "$$remote" ]; then \
		echo "HEAD ($$(git rev-parse --short HEAD)) is not origin/main ($$(git rev-parse --short refs/remotes/origin/main)) at $$url; pull or push first so the tag lands on the released commit"; \
		exit 1; \
	fi

# bump: the only tagging path (core ADR-0012). It creates the next annotated
# release tag with svu (.svu.yml); a maintainer pushes it. The gate and the tag
# are one shell line under set -e, with no separate tag target, so neither
# make -i nor a direct call can tag past a failed step. The line refuses the
# flags that ignore or skip a failure (-i, -n, -t, -q), requires a clean tree
# before the gate (so the gate checks the tagged commit), runs make verify,
# checks main again, and tags.
bump: svu-version-check bump-main-check
	@set -e; \
	case "$(firstword -$(MAKEFLAGS))" in *[intq]*) echo "make bump refuses make -i, -n, -t and -q, which ignore or skip the gate: run plain make bump"; exit 1;; esac; \
	test -z "$$(git status --porcelain)" || { echo "the working tree is not clean: commit or stash first, so the gate checks the tagged commit"; exit 1; }; \
	$(MAKE) --no-print-directory verify; \
	$(MAKE) --no-print-directory bump-main-check; \
	test -z "$$(git status --porcelain)" || { echo "the working tree changed during the gate: clean it, then run make bump again"; exit 1; }; \
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
	@echo "  make test-integration-quick - Run the integration tests without the package rebuild (needs Docker)"
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
	@echo "  make site                  - Build the documentation site (strict) into site/dist"
	@echo "  make site-serve            - Preview the documentation site at http://127.0.0.1:8000/repogen/"
	@echo "  make corrections-check     - Check the format of every corrections record"
	@echo "  make corrections-base      - Check the corrections records are append-only against BASE"
	@echo "  make verify-static         - Non-mutating static checks (tidy, vet, fmt, lint, docs, corrections)"
	@echo "  make verify                - verify-static plus the unit tests (no write)"
	@echo "  make check                 - Format, then verify (the developer gate)"
	@echo "  make vuln                  - Run govulncheck"
	@echo "  make build-cross           - Build the linux amd64 and arm64 release binaries"
	@echo "  make ci                    - What CI runs (needs Docker)"
	@echo "  make svu-version-check     - Check svu matches the mise.toml pin"
	@echo "  make bump                  - Run the gate on main, then create the next release tag (maintainers only)"
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
