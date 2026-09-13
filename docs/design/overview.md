# Repogen Overview

*Formerly `yeti/OVERVIEW.md`; folded into `docs/` per
[frostyard/core ADR-0025](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md).
Rationale for the behaviors described here lives in the repo-local ADRs
([ADR-0001](../adr/0001-unsigned-debian-repos-emit-inrelease.md)–[ADR-0011](../adr/0011-incremental-state-from-published-metadata.md);
see the [docs index](../README.md#decisions-adrs)).*

## Purpose

Repogen is a Go CLI tool that scans directories for software package files and
generates static repository structures with metadata, signatures, and checksums.
The generated repositories can be served as static websites (e.g., from S3/R2)
and consumed directly by package managers like apt, dnf, apk, pacman, and
Homebrew. It also supports systemd-sysext image repositories for
systemd-sysupdate.

## Architecture

```
cmd/repogen/main.go          Entry point — sets up logrus, calls cli.NewRootCmd()
internal/
  cli/
    root.go                   Cobra root command ("repogen")
    generate.go               "generate" subcommand — orchestrates the full pipeline
    production.go             R2 production Debian preflight — validation only, no writes
  models/
    package.go                Package struct — universal package metadata model
    repository.go             RepositoryConfig struct — all CLI flags/config
    errors.go                 Typed error system (RepoGenError with ErrorType enum)
  scanner/
    scanner.go                Scanner interface + PackageType enum (deb/rpm/apk/pacman/brew/sysext)
    filesystem.go             FileSystemScanner — walks directory tree, detects types
    detector.go               Magic-byte + extension detection for all package formats
  generator/
    generator.go              Generator interface (Generate, ValidatePackages, ParseExistingMetadata)
    deb/                      Debian/APT generator plus R3-R5 production transaction primitives
    rpm/                      Yum/DNF repository generator
    apk/                      Alpine APK repository generator
    pacman/                   Arch Linux Pacman repository generator
    homebrew/                 Homebrew tap/formula generator
    sysext/                   systemd-sysext repository generator; signs SHA256SUMS when GPG is configured
  signer/
    signer.go                 Signer + RSASigner interfaces
    gpg.go                    GPG signing (cleartext, detached ASCII, detached binary)
    rsa.go                    RSA PKCS1v15 signing for Alpine APK
  utils/
    checksum.go               Multi-hash checksum calculation (MD5/SHA1/SHA256/SHA512)
    compression.go            Gzip compress/decompress helpers
    fileops.go                CopyFile, WriteFile, EnsureDir, ShouldCopyPackage
    package_identity.go       Conflict detection for incremental mode
  dirindex/
    dirindex.go               HTML directory index generator (Apache-style)
```

## Data Flow

The `generate` subcommand (`cli/generate.go:runGeneration`) drives this pipeline:

1. **Scan** — `FileSystemScanner` walks the input directory, uses magic bytes
   and file extensions to classify each file into a `PackageType`.
2. **Parse** — Each scanned package is parsed by its format-specific parser
   (e.g., `deb.ParsePackage` extracts the control file from the ar archive).
   Parsers populate the universal `models.Package` struct. Homebrew bottles
   are an exception — they skip parsing and derive metadata from the filename.
3. **Incremental merge** (optional) — If `--incremental` is set, existing
   repository metadata is parsed via `Generator.ParseExistingMetadata()` and
   merged with new packages. Conflicts are detected using `PackageIdentity()`
   and either cause an error or are skipped (`--skip-duplicates`).
4. **Validate** — `Generator.ValidatePackages()` checks format-specific
   requirements (e.g., deb packages must have name, version, architecture,
   and `.deb` extension).
5. **Generate** — The format-specific generator copies package files to the
   output directory structure, recalculates checksums, and writes metadata
   files (Packages, repomd.xml, APKINDEX.tar.gz, etc.).
6. **Sign** — If signing keys are provided, metadata and/or packages are
   signed. GPG for deb/rpm/pacman, RSA for Alpine APK.
7. **HTML index** (optional) — If `--html-index` is set, `dirindex.Generate()`
   writes an `index.html` at every directory level.

The separate `validate-production` command is deliberately not another
generator. It validates explicit Frostyard Debian target identity,
non-overlapping paths, the fixed component and architecture set, regular
non-symlink package paths, strict `.deb` parsing, allowed package
architectures, and control-field safety. It rejects every other recognized
package format in the input tree.

The same command then requires one explicit prior-state operation.
`initialize` proves only that `dists/<codename>` is absent. `reconcile`
uses `generator/deb/production_restore.go` to verify Release against the
request digest, verify both signed forms with one accepted public key,
enforce fixed Release identity and by-hash policy, validate all four checksum
sections over the exact all/amd64 index set, require gzip/plain equivalence,
and strictly parse every package stanza. Both operations are read-only.

R4 adds `generator/deb.SharedPool` as a provider-neutral boundary for
immutable `pool/main` objects. It consumes digest authority from already
verified Packages indexes, verifies staged input bytes, stream-hashes every
existing object, and permits only conditional create-if-absent. An indexed
path must already exist with the exact indexed bytes; an unindexed path can
be created once, and a lost create race is resolved only by hashing the
winner. ETags are exposed only as informational provider metadata and never
participate in equality.

R5 composes strict restore and the shared pool in
`production_transaction.go`. Staging requires a new directory, validated
package bytes, an explicit release time, and a real signer. It creates
canonical and SHA-256 by-hash indexes, a signed Release set, and compact
secret-free manifests. Publication holds a target lock, verifies complete
expected-prior state before writes, conditionally creates shared/immutable
objects, compare-and-replaces only the enumerated suite metadata, reads every
object back, and writes InRelease last. The provider-neutral interface has
failure-injected fake-store and real local `gpgv`/apt fixture coverage; there
is intentionally no R2 adapter, production CLI, credential path, or claim
that repository permissions enforce the interface.

R6 makes the existing Debian metadata generator deterministic. Package
stanzas use a total name/lexicographic-version-string/architecture/filename
order with sorted arbitrary fields, gzip headers are fixed, Release
architectures, components, and checksum entries are sorted, and
`NewGeneratorWithClock` supplies one controlled publication timestamp. All
selected Debian pool destinations are validated before output is created:
same-path inputs can be reused only when their size and SHA-256 agree, using
local source bytes when available and retained metadata identity for an
unavailable incremental object. Conflicting contents fail closed. A repeated
generation re-renders Release with the prior Date and skips rewriting or
signing only when the bytes match exactly and both prior signatures verify
against the configured signer's public key. Changed metadata uses the current
controlled timestamp and signs a new generation. Sysext extension processing
and checksum manifests are sorted, and a duplicate filename with conflicting
digests fails instead of depending on map iteration.

R7 adds `GenerateLocalProductionRepository` and
`CommitLocalProductionTransaction` as production-only local primitives. A
candidate is fully signed in its clean transaction staging directory. The
commit path copies the prior repository into a sibling generation while
excluding the replaced codename, verifies the expected prior state and
immutable shared-pool collisions, installs and re-hashes every candidate
object, synchronizes the complete tree, and performs one Linux `renameat2`
no-replace or exchange operation. Internal fault injection runs before every
staging, signing, copy, install, verification, synchronization, and commit
step; every pre-commit failure leaves the prior output unchanged unless an
external writer caused the detected drift. Unrelated regular-file content,
size, and complete file/directory modes are copied unchanged. Regenerated
suite directories and newly created pool parents receive fixed `0755` modes,
independent of the process umask, while newly installed generated files
receive `0644`. Ownership, extended attributes, ACLs, and timestamps are not
preserved or compared. The generic generator still supports unsigned output
and direct local writes.

R8 makes the local switch crash-durable. Before `renameat2`, Repogen records
the exact prior and candidate tree digests in an exclusive recovery journal
and synchronizes both candidate and parent. After the switch it synchronizes
the parent before reporting success. Recovery accepts only three safe facts:
the output is the exact candidate, the sibling is the exact candidate while
the output is the exact prior, or the output is the exact candidate after
private cleanup already completed. Any other state stops without another
rename. A visible candidate is never rolled back. Failure to remove private
prior bytes or the journal is returned to the caller, with the journal
retained whenever cleanup is incomplete so a later recovery can retry it.
Once the visible output matches the journaled candidate digest, the sibling
is unconditionally obsolete; recovery removes it even when an interrupted
cleanup already deleted only part of that private tree.

R8 also introduces `internal/intake` for retained immutable records. Its local
file store uses an atomic hard-link create-if-absent operation, file and
directory synchronization, read-back, prefix enumeration, and process-safe
target locks. Every provider adapter must supply an equivalent single atomic
create-if-absent operation; a read followed by an unconditional write does
not satisfy the interface. The recorder binds an adapter-provided
authenticated principal to the request, verifies digest-addressed provenance
and artifacts, and allocates monotonic per-target receipts. The reconciler
enumerates receipts rather than workflow history, processes one target in
sequence, permits independent targets to run concurrently, rechecks current
authorization before each new writer attempt, retains append-only attempts,
and creates a result pointer only after public verification. A completed
result bypasses the new-write authorization check but must pass retained
integrity and public-state verification on every replay. The Debian adapter
uses the scoped production transaction and can resume a partial publication
only when each observed object is exact prior bytes, exact candidate bytes,
or authoritatively absent where allowed.

These primitives do not connect `validate-production` to a network endpoint,
provide an R2 adapter or credential path, configure a schedule, or authorize a
production operation. Capability separation still depends on a future
provider adapter and credential configuration and is not technically enforced
by these library interfaces. Atomic local replacement requires Linux
`renameat2`; unsupported platforms fail rather than use a two-rename fallback.
Each local generation still costs O(repository size) I/O and hashing and
requires roughly twice the repository's disk space while the sibling exists.

## Key Patterns

### Generator Interface

All six generators implement `generator.Generator`:

```go
type Generator interface {
    Generate(ctx context.Context, config *RepositoryConfig, packages []Package) error
    ValidatePackages(packages []Package) error
    GetSupportedType() PackageType
    ParseExistingMetadata(config *RepositoryConfig) ([]Package, error)
}
```

Each generator is instantiated in `runGeneration()` with its signer, then
dispatched by package type from the `packagesByType` map.

### Universal Package Model

All formats are normalized into `models.Package` — a flat struct with core
fields (Name, Version, Architecture, Description, Maintainer, Homepage,
License, Dependencies, Conflicts, Groups), file-level fields (Filename, Size,
MD5Sum, SHA1Sum, SHA256Sum, SHA512Sum), plus a `Metadata
map[string]interface{}` for format-specific data (e.g., RPM's `Release`,
`BuildTime`, `DistroVersion`; Pacman's `BuildDate`, `InstalledSize`; sysext's
`OSVersion`).

### Signing Strategy

*Decisions: [ADR-0009 — GPG CLI / go-crypto split](../adr/0009-gpg-cli-and-go-crypto-signing-split.md),
[ADR-0001 — unsigned InRelease](../adr/0001-unsigned-debian-repos-emit-inrelease.md).*

- **GPG (deb, rpm, pacman)**: `signer.GPGSigner` uses ProtonMail/go-crypto for
  detached ASCII signatures and shells out to `gpg` CLI for cleartext signing
  (InRelease) and binary detached signatures (Pacman `.sig` files) due to
  compatibility requirements. The GPG CLI operations use a cached temporary
  home directory (`ensureGPGHome()` via `sync.Once`) that is created lazily on
  first use and reused across all signing operations. `GPGSigner` implements
  `io.Closer` to clean up this directory; `runGeneration()` defers `Close()`
  after initializing the signer.
- **RSA (apk)**: `signer.AlpineRSASigner` uses Go stdlib crypto/rsa with
  SHA1/PKCS1v15, matching Alpine's expected signature format.
- Unsigned repos are supported — deb generates an InRelease with unsigned
  Release content for `[trusted=yes]` compatibility.

### Incremental Mode

*Decision: [ADR-0011 — incremental state from published metadata](../adr/0011-incremental-state-from-published-metadata.md).*

`--incremental` merges new packages into an existing repository without
removing old ones. The workflow:
1. Parse existing metadata (`ParseExistingMetadata`)
2. Detect conflicts using `PackageIdentity()` (format-aware: e.g., RPM
   includes Release field, Homebrew uses name+version only)
3. Either error on conflicts or skip them (`--skip-duplicates`)
4. Concatenate existing + new, regenerate all metadata

### Pre-compiled Regexes

Regex patterns used inside loops (RPM distro version parsing, Homebrew formula
parsing) are compiled once at package init time as `var` declarations, avoiding
repeated compilation during scanning.

### Package Copy Optimization

*Decision: [ADR-0007 — checksums from copied bytes](../adr/0007-recompute-checksums-from-copied-bytes.md).*

`utils.ShouldCopyPackage()` avoids redundant file copies by checking:
1. Whether source == destination path
2. Whether file sizes match
3. Whether SHA256 checksums match

Used by all six generators to skip unnecessary copies during generation.
For existing packages in remote storage (S3/R2), the file may not exist
locally — this is handled gracefully by returning `needsCopy=false`.

## Configuration

All configuration is passed via CLI flags to `models.RepositoryConfig`:

| Flag | Default | Description |
|------|---------|-------------|
| `--input-dir` / `-i` | `.` | Directory to scan for packages |
| `--output-dir` / `-o` | `./repo` | Output directory for repository |
| `--gpg-key` / `-k` | | GPG private key path (deb/rpm/pacman signing) |
| `--gpg-passphrase` / `-p` | | GPG key passphrase |
| `--rsa-key` | | RSA private key path (Alpine signing) |
| `--rsa-passphrase` | | RSA key passphrase |
| `--key-name` | `repogen` | Key name for Alpine signatures |
| `--origin` | `Repogen Repository` | Repository origin name |
| `--label` | (same as origin) | Repository label |
| `--repo-name` | | Repository name (required for Pacman, optional for RPM .repo naming) |
| `--codename` | `stable` | Debian codename |
| `--suite` | (same as codename) | Debian suite |
| `--components` | `main` | Debian components |
| `--arch` | `amd64` | Architectures to support |
| `--base-url` | | Base URL for Homebrew bottles, RPM .repo files, sysext transfers (required for sysext) |
| `--gpg-key-url` | | GPG key URL for RPM .repo files (supports `$releasever`/`$basearch` variables) |
| `--distro` | `fedora` | RPM distribution variant (fedora/centos/rhel) |
| `--version` | | RPM release version (auto-detected if not set) |
| `--incremental` | `false` | Merge with existing repository |
| `--skip-duplicates` | `false` | Skip conflicting packages in incremental mode |
| `--html-index` | `false` | Generate HTML directory index pages |
| `-v` / `--verbose` | `false` | Debug-level logging |

## Format-Specific Output Structures

See [Generator Details](../specs/generators.md) for the output directory
layout, metadata file formats, and signing behavior of each generator.
Layout decisions: [ADR-0002 (Debian pool)](../adr/0002-shared-debian-pool-layout.md),
[ADR-0003 (single `main` component)](../adr/0003-single-main-component.md),
[ADR-0004 (RPM version/arch shard)](../adr/0004-rpm-version-arch-layout-and-version-ladder.md),
[ADR-0005 (content-addressed repodata)](../adr/0005-content-addressed-repodata.md),
[ADR-0006 (Pacman dual database files)](../adr/0006-pacman-dual-database-files.md).
Package-type detection: [ADR-0008](../adr/0008-magic-bytes-detection-with-extension-fallback.md).

## CI/CD & GitHub Action

See [CI/CD and GitHub Action](ci-cd.md) for the test/release workflows
and the `publish-to-r2` composite action.

## Planned Frostyard Production Boundary

The generic command and action behavior described above remains unchanged.
The R2-R3 `validate-production` path implements the fail-before-write target,
Debian-input, explicit initialize, and strict signed-reconcile boundaries,
while the isolated R4 primitive enforces conditional no-overwrite handling
for shared-pool bytes in fixtures. Neither can generate or publish a
production repository.
The complete fail-closed Frostyard production publisher remains specified in
[Plan 0001](../plans/0001-frostyard-production-publisher.md). The plan
preserves generic local and unsigned generation while defining the proposed
production-only target, identity, restore, shared-pool, staging, manifest,
publication, and rollback contracts. The architectural rationale is recorded
in proposed
[ADR-0013](../adr/0013-separate-generic-generation-from-production-publishing.md).

## Dependencies

| Module | Purpose |
|--------|---------|
| `github.com/spf13/cobra` | CLI framework |
| `github.com/sirupsen/logrus` | Structured logging |
| `github.com/ProtonMail/go-crypto` | OpenPGP signing |
| `github.com/klauspost/compress` | gzip + zstd compression |
| `github.com/sassoftware/go-rpmutils` | RPM package parsing |
| `github.com/ulikunitz/xz` | XZ decompression (deb control.tar.xz) |

Go version: 1.23.5
