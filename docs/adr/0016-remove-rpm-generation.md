# 0016 — Remove RPM generation

- **Status:** Proposed
- **Date:** 2026-10-24

## Context

Vulnerability [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) reports
that `golang.org/x/crypto/openpgp` is unmaintained, unsafe by design, and has
known security issues. The advisory has no fixed version ("Fixed in: N/A").

Repogen reached that package through RPM. `github.com/sassoftware/go-rpmutils`
v0.3.0 imports `golang.org/x/crypto/openpgp`, and
`internal/generator/rpm/parser.go` imports go-rpmutils, so govulncheck found
the package reachable at init time (`rpm.init` calls `rpmutils.init`, which
calls `s2k.init`). A bump of `golang.org/x/crypto` cannot fix this.

No Frostyard consumer uses RPM generation. A scan on 2026-10-24 of all 72
frostyard repositories (66 public, 6 private, archived ones included; default
branches, shallow clones) found no workflow or script that calls Repogen with
`rpm`:

- The only live caller is snosi `.github/workflows/build.yml`. It uses
  `publish-to-r2` with `package-type: sysext` at v0.4.1.
- The archived repositories snowcat-cockpit, gchlog, nbc and igloo-old used
  `package-type: deb`.
- apt-publisher, omarchy-apps, homebrew-tap and incus do not call Repogen with
  `rpm`.
- `https://repository.frostyard.org/` lists only `ext/`. `/rpm/` and
  `/repodata/repomd.xml` return 404.
- Debian publication moved to apt-publisher (core
  [ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md)).
- Some tools still build `.rpm` files with GoReleaser nfpm (for example
  intuneme). They attach the files to GitHub releases and do not feed them to
  Repogen.

The RPM generator is about 983 lines in `internal/generator/rpm/`, plus its
fixtures, tests, flags and docs.

## Decision

Remove RPM generation from Repogen. These parts go:

- the `rpm` package type: the `TypeRpm` constant, its detection (the
  `0xED 0xAB 0xEE 0xDB` lead and the `.rpm` extension) in `internal/scanner/`,
  and its case in `internal/utils/package_identity.go`;
- `internal/generator/rpm/`, and its registration and parse case in
  `internal/cli/generate.go`;
- the RPM-only `generate` flags `--distro`, `--version` and `--gpg-key-url`,
  and the matching `RepositoryConfig` fields (`DistroVariant`, `Version`,
  `GPGKeyURL`);
- the `github.com/sassoftware/go-rpmutils` dependency, and with it
  `github.com/DataDog/zstd` and `github.com/xi2/xz`;
- the RPM fixtures (`test/fixtures/rpms/`), the RPM integration tests, and
  the RPM package build in `test/build-test-packages.sh` and the `Makefile`;
- `rpm` from the `publish-to-r2` action: the `package-type` list and its
  validation, the `distro-variant` and `version` inputs, and the RPM metadata
  sync.

Add a pinned govulncheck step to the `test` job of
`.github/workflows/test.yml`
(`go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`), so that a reachable
vulnerability fails CI.

Bump `golang.org/x/crypto` to v0.58.0 (it stays in the module graph through
`github.com/ProtonMail/go-crypto`) and `github.com/klauspost/compress` to
v1.18.7 (GO-2026-5841).

## Consequences

- Breaking change for any external user of RPM output. Proposed release:
  v0.7.0 (Repogen is at v0.6.0).
- `.rpm` files are no longer detected. The scanner classes them as unknown
  and skips them, whatever their extension or magic bytes. For a file with
  the `.rpm` extension it logs a warning that names the file and ADR-0016, so
  the skip is visible. A
  directory with only `.rpm` files gives "No packages found in input
  directory" (exit 0) in `generate`, and the `publish-to-r2` action stops with
  "No packages found". In a mixed directory, the other formats are generated
  and the `.rpm` files are ignored.
- `generate` has no `--type` flag: the type comes from the files. The old
  `--distro`, `--version` and `--gpg-key-url` flags now fail with the cobra
  error `unknown flag`.
- In the `publish-to-r2` action, `package-type: rpm` now fails in the
  validation step with `Invalid package-type 'rpm'. Must be one of: deb,
  sysext, apk, pacman, homebrew`. The inputs `distro-variant` and `version`
  no longer exist; GitHub only warns about unexpected inputs, and the action
  ignores them.
- Raising `golang.org/x/crypto` to v0.58.0 raises the `go` directive in
  `go.mod` from 1.24 to 1.26.0. The `toolchain` line stays `go1.26.10`, and
  it must stay equal to `GOTOOLCHAIN` in the golangci-lint install step.
- govulncheck still lists GO-2026-5932 as a module-level note, because
  `golang.org/x/crypto` stays required through `ProtonMail/go-crypto`. No
  package Repogen imports calls `openpgp`, so the code is not affected, and
  govulncheck exits 0.
- The signing code and the sysext, Debian, Alpine, Pacman and Homebrew
  generators do not change.
- Re-adding RPM later needs a new ADR and a parser that does not import
  `x/crypto/openpgp`. The record stays in git history and in ADR-0004,
  ADR-0005 and ADR-0010.

## Alternatives considered

- **Keep RPM and replace go-rpmutils with another parser (options A and B):**
  rejected. Repogen would own more code for a type nobody uses.
- **Keep RPM and suppress the finding:** rejected. It hides a reachable,
  unsafe package, and the advisory has no fix.

## References

- Supersedes: [ADR-0004](0004-rpm-version-arch-layout-and-version-ladder.md),
  [ADR-0005](0005-content-addressed-repodata.md),
  [ADR-0010](0010-dnf-repo-file-policy.md) in full; the RPM parts of
  [ADR-0007](0007-recompute-checksums-from-copied-bytes.md),
  [ADR-0008](0008-magic-bytes-detection-with-extension-fallback.md),
  [ADR-0009](0009-gpg-cli-and-go-crypto-signing-split.md) and
  [ADR-0011](0011-incremental-state-from-published-metadata.md)
- Builds on: [ADR-0015](0015-remove-the-superseded-production-path.md) (the
  precedent for a removal), core
  [ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md)
- Shapes: [design/overview.md](../design/overview.md),
  [design/ci-cd.md](../design/ci-cd.md),
  [specs/generators.md](../specs/generators.md)
