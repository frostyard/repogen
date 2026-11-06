# CI/CD and GitHub Action

*Formerly `yeti/ci-cd.md`. Design context: [overview.md](overview.md).*

## Workflows

### CI (`.github/workflows/ci.yml`)

Runs on pull requests and pushes to `main`, as one job (`ci`, named
`make ci`, timeout 45 minutes). The workflow sets top-level
`permissions: contents: read` (least privilege). Every external action is
pinned to a full 40-character commit SHA with a trailing `# vX.Y.Z` comment:
`actions/checkout` v4.4.0, `actions/setup-go` v5.6.0, `jdx/mise-action`
v5.2.6, `codecov/codecov-action` v4.6.0
([core ADR-0021](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md)).
`release.yml` follows the same rule. The job runs these steps in order:

1. Checkout with `persist-credentials: false`, so the job cannot push with
   the workflow token.
2. Set up Go from `go.mod`: the module minimum is 1.26.0, and the
   `toolchain go1.26.10` line makes the go command use Go 1.26.10.
3. `jdx/mise-action` installs `golangci-lint` and `node` at the `mise.toml`
   pins, verified against `mise.lock` (Go still comes from step 2).
4. `make ci`, the one gate. It runs, in order:
   - `make verify`: `make verify-static` (tidy, vet, gofmt check, `make lint`
     at the mise pin, `node scripts/check-docs.mjs`) then `make test-unit`;
   - `make vuln` — `govulncheck` v1.9.0; a vulnerability that the code
     reaches fails the job ([ADR-0016](../adr/0016-remove-rpm-generation.md));
   - `make test-race` — unit tests with race detection and
     `coverage.txt`;
   - `make test-packages-docker` — builds the test packages in Docker;
   - `make test-integration`; and
   - `make build-cross` — the linux amd64 and arm64 release builds.

   `make docs-check` keeps the docs gate of
   [ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md) inside
   the same job: docs-index coverage, relative-link integrity, and
   symlink resolution against `.coverage-thresholds.json` (part of the
   [quality loop](quality-loop.md)).
5. Uploads `coverage.txt` to Codecov.

[Core ADR-0073](https://github.com/frostyard/core/blob/main/docs/adr/0073-let-ci-run-the-make-ci-targets-as-separate-jobs.md)
allows CI to run the `make ci` targets as separate jobs; this repo keeps 1
job, so the Makefile stays the only place that names the order.

### Risk paths (`.github/workflows/risk-paths.yml`)

Runs on `pull_request_target` (opened, synchronize, reopened, edited). It
checks out nothing and runs no pull-request code: it calls
`frostyard/core/.github/workflows/risk-paths.yml` at a pinned commit, so the
base branch's risk list and caller apply. A pull request that touches a glob
in [`.github/risk-paths.json`](../../.github/risk-paths.json) gets the label
`risk:high`
([core ADR-0068](https://github.com/frostyard/core/blob/main/docs/adr/0068-label-pull-requests-that-touch-high-risk-paths.md)).
It is not a required check, and its job holds `pull-requests: write` only.

### Release (`.github/workflows/release.yml`)

Triggered by `v*.*.*` tags. The workflow has `permissions: {}` at the top and
`contents: write` on its `goreleaser` job, so no other job inherits write
access:
1. Checks out the tag with full history and `persist-credentials: false`,
   and sets up Go, all with commit-pinned actions; Go setup reads the same
   `go.mod`, so release binaries build with the `toolchain` version
   (go1.26.10) that CI uses.
2. Runs exact GoReleaser version `v2.18.1`.
3. Builds raw linux amd64/arm64 binaries with the exact tag version and full
   source commit embedded.
4. Generates the authoritative `SHA256SUMS`.
5. Creates the GitHub release with those assets.

The former parallel hand-built workflow and its unrelated package/repository
artifacts were removed. The sole GoReleaser contract publishes
`repogen-linux-amd64`, `repogen-linux-arm64`, and `SHA256SUMS`.
The Darwin amd64/arm64 assets from the retired workflow are intentionally not
published. `SHA256SUMS` is unsigned and same-origin with the binaries, so its
digest check does not provide independent authenticity; consumers trust
GitHub's HTTPS release origin and repository release controls.

R10 deliberately keeps three identities separate: the independently reviewed
candidate, the exact human-merged tree, and the separately human-published
release. The complete local and external evidence contract is
[R10 release acceptance](../specs/r10-release-acceptance.md), and the
step-by-step operator procedure is
[`r10-release.prompt.md`](../../.github/prompts/r10-release.prompt.md).
Neither a local snapshot nor a successful release workflow alone proves the
published assets and embedded identities.

## GitHub Action: `publish-to-r2`

**Location**: `.github/actions/publish-to-r2/action.yml`

A reusable composite action that publishes packages to an existing repogen
repository hosted on Cloudflare R2. Designed for CI/CD pipelines that
build packages and want to add them to a repository incrementally.

The action restores partial paths, uses generic incremental fallback
behavior, and synchronizes non-Debian output in a fixed order: payloads, then
metadata, then directory indexes
([ADR-0017](../adr/0017-upload-payloads-before-metadata.md)). It rejects Debian
structurally: `frostyard/apt-publisher` is the only Frostyard Debian writer
([ADR-0015](../adr/0015-remove-the-superseded-production-path.md)).
For sysext, a restore error is fatal and the calling workflow must serialize
the complete cycle globally because `ext/index` is shared. The action does not
make fixed R2 `SHA256SUMS` and `SHA256SUMS.gpg` objects atomic.

### How It Works

1. **Validate inputs** — runs `scripts/validate-publish-target.sh` first,
   which refuses a `codename` or `suite` of `stable` (any case, whitespace
   trimmed) for every package type because the signed stable suite is frozen
   (core ADR-0048, ADR-0054), refuses target names that are not plain names
   (such as `stable/`), refuses any Debian package found in `packages-dir`
   whatever the declared type (repogen generates every detected type), and
   requires `codename` for `deb` (there is no default). It then checks package type, required flags (base-url for
   sysext, repo-name for pacman), directory existence, and refuses
   `skip-published-identities: true` unless `package-type` is `sysext` and
   `gpg-private-key` is set (published identities are read only from
   signature-verified manifests; the step sees the key's presence, never the
   key).
   `rpm` is not a valid
   package type ([ADR-0016](../adr/0016-remove-rpm-generation.md)).
2. **Install repogen** — requires an exact tag and commit, uses a fixed GitHub
   release origin, downloads the exact architecture asset plus unsigned
   `SHA256SUMS`, verifies the selected digest, and verifies the embedded
   version/commit before installation.
3. **Configure AWS CLI** — sets up R2 endpoint credentials.
4. **Sync existing metadata** — downloads only metadata files (not package
   binaries) from R2 for incremental mode. Sync strategy varies by format:
   - deb: `dists/` directory
   - sysext: `ext/` excluding `.raw*` files; any restore error aborts
   - apk: everything except `.apk` files
   - pacman: everything except `.pkg.tar.*` and `.sig` files
   - homebrew: `Formula/` directory
5. **Prepare signing keys** — decodes GPG/RSA keys from secrets (base64 or
   ASCII armored).
6. **Run repogen** — always uses `--incremental` mode with all provided
   configuration. With `skip-published-identities: true` it also passes
   `--skip-published-identities` and `--skipped-report
   "$RUNNER_TEMP/repogen-skipped.txt"`, and exports `SKIPPED_REPORT`. An
   empty `packages-dir` stays an error. Before repogen runs, the step counts
   the payloads (`*.raw`, `*.raw.zst`, `*.raw.xz`, `*.raw.gz`) that are
   already under `./repo` and refuses the run when there are any, because
   `packages-added` below counts the payloads in `./repo` and a payload left by
   an earlier step in the same job would be counted as added. After repogen
   runs, `packages-added` is that payload count, so a
   run that skipped every built identity writes `packages-added=0` and
   `published=false` with a `::notice::`.
7. **Check skipped identities have their payloads** — runs
   `scripts/check-published-payloads.sh` when `skip-published-identities` is
   `true`. It sends one `head-object` request per skipped identity and fails
   when a payload named in the report is missing or unreadable, because the
   rebuild cannot repair that identity
   ([ADR-0018](../adr/0018-opt-in-identity-level-skip-for-published-sysexts.md)).
   The key is a request parameter, so no response decoding can confuse two
   keys. An empty report makes no request.
8. **Upload to R2** — gated on `steps.run-repogen.outputs.published == 'true'`,
   so a fully skipped run uploads nothing. It runs `scripts/upload-to-r2.sh`,
   which never uses
   `--delete` (existing packages that are not local are preserved) and works in
   this order:
   0. classify: refuse the run, before any upload, when a base name has one of
      the two non-end-anchored detector shapes (`.pkg.tar.` or `.bottle.tar`)
      and no payload pattern matches the path; only the generated sysext
      transfer file `ext/<name>/<name>.transfer` and payload shapes inside a
      directory name are exempt;
   1. payloads only (`--exclude '*'` plus one `--include` per payload pattern);
   2. a verify pass with `--dryrun --size-only` over the same filters, which
      fails the step before any metadata is uploaded if a payload is missing
      from the bucket or has another size (it does not compare bytes);
   3. metadata (everything that is not a payload and not a directory index);
   4. directory indexes (`index`, `*/index`, `index.html`, `*/index.html`)
      last, because an index enumerates the other two groups.

   The payload patterns are end-anchored: `*.raw`, `*.raw.zst`, `*.raw.xz`,
   `*.raw.gz`, `*.apk`, `*.pkg.tar`, `*.pkg.tar.zst`, `*.pkg.tar.xz`,
   `*.pkg.tar.gz`, their `.sig` files, `*.bottle.tar` and `*.bottle.tar.gz`. A
   trailing `*` (for example `*.pkg.tar.*`) would also match a file inside a
   directory whose name looks like a payload, such as
   `ext/foo.pkg.tar.x/SHA256SUMS`, and phase 1 would upload that manifest
   beside its payload.

   Phases 1, 3 and 4 retry up to `UPLOAD_ATTEMPTS` times (default 4, delay
   `UPLOAD_RETRY_DELAY * N`, default 15s); `aws s3 sync` is idempotent, so a
   retry sends only what is still missing. The rule the order enforces: live
   metadata must never reference a payload that is not in the bucket
   ([ADR-0017](../adr/0017-upload-payloads-before-metadata.md), issue #84).
9. **Purge Cloudflare cache** (optional) — purges by hostname via
   Cloudflare API. Gated on `purge-cache == 'true'` **and**
   `steps.run-repogen.outputs.published == 'true'`.
10. **Cleanup** — removes temp key files, clears AWS credentials.

### Required Inputs

| Input | Description |
|-------|-------------|
| `r2-account-id` | Cloudflare R2 Account ID |
| `r2-access-key-id` | R2 Access Key ID |
| `r2-secret-access-key` | R2 Secret Access Key |
| `r2-bucket` | R2 Bucket name |
| `packages-dir` | Directory containing packages |
| `package-type` | Listed format; `deb` is rejected by this legacy action |
| `repogen-version` | Exact v-prefixed release tag |
| `repogen-commit` | Exact 40-character embedded source commit |

### Notable Optional Inputs

| Input | Default | Notes |
|-------|---------|-------|
| `base-url` | | Required for sysext; used for cache purge hostname |
| `codename` | | No default; required for `deb`; `stable` refused |
| `suite` | | Defaults to codename; `stable` refused |
| `repo-prefix` | | Path prefix in R2 bucket |
| `skip-duplicates` | `false` | Useful for nightly builds |
| `skip-published-identities` | `false` | sysext only: skip each built sysext whose identity the signature-verified `SHA256SUMS` already lists, whatever its bytes; nothing left means nothing is uploaded and the run succeeds. Requires `gpg-private-key` and a `./repo` without payloads. Known gap: a content change without a version or revision bump is not republished ([ADR-0018](../adr/0018-opt-in-identity-level-skip-for-published-sysexts.md)) |
| `html-index` | `true` | Generates browsable directory pages |
| `purge-cache` | `false` | Requires `cloudflare-zone` and `cloudflare-api-token` |

### Outputs

| Output | Description |
|--------|-------------|
| `packages-added` | Number of packages added to the repository |
| `published` | `'true'` when the action uploaded to R2; `'false'` when `skip-published-identities` left nothing to publish |

## Dependabot

`.github/dependabot.yml` configures automated dependency updates:

- **Ecosystem**: `gomod` — monitors `go.mod` for dependency updates.
- **Schedule**: Weekly checks.
- **Commit prefix**: `deps:` — follows the conventional-commit style used in
  this repo (e.g., `perf:`, `fix:`, `docs:`).
- **Labels**: PRs are labeled `dependencies`.
- **Ecosystem**: `github-actions` — keeps the SHA-pinned `uses:` lines current
  (it updates the SHA and the `# vX.Y.Z` comment together). It scans
  `/` (the workflows) and `/.github/actions/*` (the composite actions), weekly,
  with commit prefix `ci:` and the `dependencies` label.

Dependabot respects the `go` directive in `go.mod` and will not propose updates
requiring a newer Go version. Dependabot PRs trigger the `CI` workflow
automatically, providing CI validation before merge.

## Makefile Targets

| Target | Description |
|--------|-------------|
| `make build` | Build `./repogen` binary |
| `make test` | Run unit + integration tests |
| `make test-unit` | Unit tests only (`go test -short`) |
| `make test-integration` | Build test packages, then run integration tests |
| `make test-integration-quick` | Integration tests in short mode, skipped when unavailable |
| `make test-packages` | Build test packages locally |
| `make test-packages-docker` | Build test fixtures in Docker containers |
| `make test-race` | Unit tests with the race detector and `coverage.txt` |
| `make lint` | Run golangci-lint at the `mise.toml` pin (checks the pin first) |
| `make lint-version-check` | Fail unless golangci-lint matches the pin and its Go is new enough |
| `make tidy-check` | `go mod tidy -diff` (no write) |
| `make vet` | `go vet ./...` |
| `make fmt-check` | Every tracked Go file is gofmt-clean (no write) |
| `make docs-check` | `node scripts/check-docs.mjs` |
| `make verify-static` | tidy, vet, fmt-check, lint, docs-check |
| `make verify` | `verify-static` plus the unit tests; writes no file |
| `make check` | `make fmt`, then `make verify` — the developer gate |
| `make vuln` | `govulncheck` at the pinned version |
| `make build-cross` | The linux amd64 and arm64 release builds |
| `make ci` | What CI runs: verify, vuln, test-race, test-packages-docker, test-integration, build-cross |
| `make svu-version-check` | Fail unless `svu` matches the `mise.toml` pin |
| `make bump` | Create the next annotated release tag with `svu`; a maintainer pushes it |
| `make fmt` | Format code |
| `make install` | Install to `/usr/local/bin` |
| `make uninstall` | Remove from `/usr/local/bin` |
| `make deps` | Update dependencies (`go mod tidy` + `go mod download`) |
| `make clean` | Remove build artifacts and test outputs |
| `make help` | Show available targets |

## Test Fixtures

`test/fixtures/` contains pre-built packages for unit tests:
- `debs/` — 3 .deb files (including a gzip-compressed variant)
- `apks/` — 2 .apk files
- `pacman/` — 1 .pkg.tar.zst file
- `bottles/` — 2 Homebrew bottles
- `gpg-keys/` — Test GPG keypair
- `sysext/` — (placeholder, see README)

Integration tests (`test/integration_test.go`) run the full pipeline with
these fixtures and verify the output structure.
