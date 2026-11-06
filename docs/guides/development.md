# Development

```bash
mise install   # Go from go.mod, and the pinned golangci-lint, node and svu
make check     # format, then the verify gate: use it while you work
make ci        # the full CI gate; it needs Docker
```

`make check` never needs credentials and never needs Docker. `make ci` runs
everything the CI workflow runs.

## Gate targets

| Target | What it runs |
|---|---|
| `make verify` | The non-mutating gate: `go mod tidy -diff`, `go vet`, a gofmt check, `golangci-lint` at the `mise.toml` pin, `node scripts/check-docs.mjs`, and the unit tests. It writes no file, so a read-only reviewer can run it |
| `make check` | `make fmt`, then `make verify`. The developer gate |
| `make ci` | `make verify`, `make vuln`, `make test-race`, `make test-packages-docker` and `make test-integration`, then `make build-cross`. Needs Docker |
| `make vuln` | `govulncheck` at a pinned version; a reachable known vulnerability fails it |
| `make test-race` | The unit tests with the race detector and a `coverage.txt` profile |
| `make build-cross` | The release builds: linux amd64 and linux arm64 |
| `make bump` | Creates the next annotated release tag with `svu` (`.svu.yml`). A maintainer runs it and pushes the tag; agents never tag |

## Testing

Repogen includes a comprehensive test suite with Docker-based integration tests that verify each repository type works correctly in its native environment.

### Quick Start

```bash
# Build the binary
make build

# Build test packages
make test-packages

# Run all tests (unit + integration)
make test

# Run only integration tests
make test-integration
```

### Test Package Generation

Test packages are minimal dummy packages used to verify repository functionality:

```bash
# Build test packages natively (requires dpkg-deb)
make test-packages

# Build test packages using Docker (recommended if tools not available)
make test-packages-docker
```

This creates:

- `test/fixtures/debs/repogen-test_1.0.0_amd64.deb`
- `test/fixtures/apks/repogen-test-1.0.0-r0.apk`
- `test/fixtures/pacman/repogen-test-1.0.0-1-x86_64.pkg.tar.zst`
- `test/fixtures/bottles/repogen-test--1.0.0.x86_64_linux.bottle.tar.gz`

### Integration Tests

Integration tests use Docker to:

1. Generate repositories with test packages
2. Spin up distribution-specific containers
3. Configure package managers to use test repositories
4. Install test packages
5. Verify successful installation

**Tested Distributions:**

- **Debian**: Debian Bookworm and Trixie containers
- **Alpine**: Alpine latest container
- **Pacman**: Arch Linux latest container
- **Homebrew**: Formula validation (local)

**Running Integration Tests:**

```bash
# Requires Docker
make test-integration

# Or run directly with Go
go test -v -timeout 15m ./test

# Skip integration tests if Docker not available
go test -v -short ./...
```

### Test Output

Integration tests verify:

- ✓ Repository structure (all expected files present)
- ✓ Metadata files (Release, Packages, APKINDEX)
- ✓ Package manager can read repository metadata
- ✓ Package manager can install packages
- ✓ Installed binaries execute successfully

Example output:

```
=== RUN   TestIntegration
=== RUN   TestIntegration/Debian
    Generating Debian repository...
    Testing repository in Debian container...
    ✓ Debian repository test passed
=== RUN   TestIntegration/Alpine
    Generating Alpine repository...
    Testing repository in Alpine container...
    ✓ Alpine repository test passed
=== RUN   TestIntegration/Pacman
    Generating Pacman repository...
    Testing repository in Arch Linux container...
    ✓ Pacman repository test passed
=== RUN   TestIntegration/Homebrew
    Generating Homebrew repository...
    ✓ Homebrew repository test passed
```

### Manual Testing

You can manually test repositories:

```bash
# Generate a test repository
./repogen generate --input-dir test/fixtures/debs --output-dir /tmp/test-repo --codename stable

# Serve with Python
cd /tmp/test-repo
python3 -m http.server 8000

# In another terminal, test with Docker
docker run -it --rm debian:bookworm bash
# Inside container:
echo "deb [trusted=yes] http://host.docker.internal:8000 stable main" > /etc/apt/sources.list.d/test.list
apt update
apt install repogen-test
```

### Integration tests fail

- Ensure Docker is installed and running: `docker version`
- Build test packages first: `make test-packages`
- Check Docker can pull images: `docker pull debian:bookworm`
- Increase timeout for slow systems: `go test -timeout 30m ./test`

## Contributing

Contributions are welcome! Please feel free to submit pull requests or open issues for bugs and feature requests.

### Development Workflow

```bash
# Clone and build
git clone https://github.com/frostyard/repogen
cd repogen
make build

# Make changes; `make check` formats and then runs the verify gate
make check

# Before committing: run the full gate (it needs Docker)
make ci
```
