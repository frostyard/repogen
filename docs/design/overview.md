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
    deb/                      Debian/APT repository generator
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
