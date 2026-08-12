# CI/CD and GitHub Action

*Formerly `yeti/ci-cd.md`. Design context: [overview.md](overview.md).*

## Workflows

### Test (`.github/workflows/test.yml`)

Runs on PRs and pushes to main/master, two jobs:

`test`:
1. Sets up Go 1.23 with module caching.
2. Runs the required golangci-lint v2 gate through `make lint`.
3. Runs unit tests with race detection and coverage (`go test -v -short -race`).
4. Builds test packages in Docker (`make test-packages-docker`).
5. Runs integration tests (`make test-integration`).
6. Uploads coverage to Codecov.

`docs-gate`:
1. Runs `node scripts/check-docs.mjs` — docs-index coverage, relative-link
   integrity, and conformance-alias symlink resolution against
   `.coverage-thresholds.json`
   ([ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md); part
   of the [quality loop](quality-loop.md)).

### Release (`.github/workflows/release.yml`)

Triggered by `v*.*.*` tags:
1. Cross-compiles repogen for linux/darwin × amd64/arm64.
2. Creates `.deb`, `.rpm`, `.apk`, and `.bottle.tar.gz` packages.
3. Runs repogen itself to generate a repository from its own packages.
4. Archives the repository as zip and tar.gz.
5. Creates a GitHub release with all artifacts.

### GoReleaser (`.github/workflows/goreleaser.yml`)

Alternative release mechanism using GoReleaser (`.goreleaser.yml`):
- Builds linux amd64/arm64 only (CGO_ENABLED=0).
- Produces tar.gz archives with README and LICENSE.
- Auto-generates changelog excluding docs/test/ci commits.

## GitHub Action: `publish-to-r2`

**Location**: `.github/actions/publish-to-r2/action.yml`

A reusable composite action that publishes packages to an existing repogen
repository hosted on Cloudflare R2. Designed for CI/CD pipelines that
build packages and want to add them to a repository incrementally.

### How It Works

1. **Validate inputs** — checks package type, required flags (base-url for
   sysext, repo-name for pacman), directory existence.
2. **Install repogen** — downloads the specified version (or latest) from
   GitHub releases.
3. **Configure AWS CLI** — sets up R2 endpoint credentials.
4. **Sync existing metadata** — downloads only metadata files (not package
   binaries) from R2 for incremental mode. Sync strategy varies by format:
   - deb: `dists/` directory
   - sysext: `ext/` excluding `.raw*` files
   - rpm: `repodata/` directory
   - apk: everything except `.apk` files
   - pacman: everything except `.pkg.tar.*` and `.sig` files
   - homebrew: `Formula/` directory
5. **Prepare signing keys** — decodes GPG/RSA keys from secrets (base64 or
   ASCII armored).
6. **Run repogen** — always uses `--incremental` mode with all provided
   configuration.
7. **Upload to R2** — `aws s3 sync` without `--delete` to preserve
   existing packages not present locally.
8. **Purge Cloudflare cache** (optional) — purges by hostname via
   Cloudflare API.
9. **Cleanup** — removes temp key files, clears AWS credentials.

### Required Inputs

| Input | Description |
|-------|-------------|
| `r2-account-id` | Cloudflare R2 Account ID |
| `r2-access-key-id` | R2 Access Key ID |
| `r2-secret-access-key` | R2 Secret Access Key |
| `r2-bucket` | R2 Bucket name |
| `packages-dir` | Directory containing packages |
| `package-type` | One of: deb, sysext, rpm, apk, pacman, homebrew |

### Notable Optional Inputs

| Input | Default | Notes |
|-------|---------|-------|
| `base-url` | | Required for sysext; used for cache purge hostname |
| `repo-prefix` | | Path prefix in R2 bucket |
| `skip-duplicates` | `false` | Useful for nightly builds |
| `html-index` | `true` | Generates browsable directory pages |
| `purge-cache` | `false` | Requires `cloudflare-zone` and `cloudflare-api-token` |
| `repogen-version` | `latest` | Pin to specific version for reproducibility |

### Outputs

| Output | Description |
|--------|-------------|
| `packages-added` | Number of packages added to the repository |

## Dependabot

`.github/dependabot.yml` configures automated dependency updates:

- **Ecosystem**: `gomod` — monitors `go.mod` for dependency updates.
- **Schedule**: Weekly checks.
- **Commit prefix**: `deps:` — follows the conventional-commit style used in
  this repo (e.g., `perf:`, `fix:`, `docs:`).
- **Labels**: PRs are labeled `dependencies`.

Dependabot respects the `go` directive in `go.mod` and will not propose updates
requiring a newer Go version. Dependabot PRs trigger the `Test` workflow
automatically, providing CI validation before merge.

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build `./repogen` binary |
| `make test` | Run unit + integration tests |
| `make test-unit` | Unit tests only (`go test -short`) |
| `make test-integration` | Build test packages, then run integration tests |
| `make test-packages` | Build test packages locally |
| `make test-packages-docker` | Build test fixtures in Docker containers |
| `make lint` | Run golangci-lint |
| `make fmt` | Format code |
| `make install` | Install to `/usr/local/bin` |
| `make uninstall` | Remove from `/usr/local/bin` |
| `make deps` | Update dependencies (`go mod tidy` + `go mod download`) |
| `make clean` | Remove build artifacts and test outputs |
| `make help` | Show available targets |

## Test Fixtures

`test/fixtures/` contains pre-built packages for unit tests:
- `debs/` — 3 .deb files (including a gzip-compressed variant)
- `rpms/` — 2 .rpm files
- `apks/` — 2 .apk files
- `pacman/` — 1 .pkg.tar.zst file
- `bottles/` — 2 Homebrew bottles
- `gpg-keys/` — Test GPG keypair
- `sysext/` — (placeholder, see README)

Integration tests (`test/integration_test.go`) run the full pipeline with
these fixtures and verify the output structure.
