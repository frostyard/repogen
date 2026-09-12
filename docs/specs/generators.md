# Generator Details

*Formerly `yeti/generators.md`. Design context:
[../design/overview.md](../design/overview.md) (pipeline and the
`generator.Generator` interface). Cross-cutting decisions:
[ADR-0007 — checksums from copied bytes](../adr/0007-recompute-checksums-from-copied-bytes.md),
[ADR-0008 — magic-bytes detection](../adr/0008-magic-bytes-detection-with-extension-fallback.md),
[ADR-0009 — GPG signing split](../adr/0009-gpg-cli-and-go-crypto-signing-split.md),
[ADR-0011 — incremental state from published metadata](../adr/0011-incremental-state-from-published-metadata.md).*

Each generator implements `generator.Generator` and produces a complete
repository structure from a list of `models.Package` entries — the exact
output layout, metadata file formats, and signing behavior per format.

## Frostyard production Debian preflight (`internal/cli/production.go`)

`repogen validate-production` is an R2 validation boundary, not a generator.
It accepts no implicit target values and writes no output. A valid request
must provide:

- distinct, non-overlapping input and future output paths;
- a lowercase immutable codename and an exactly matching suite (moving names
  including `stable`, `testing`, and `unstable` are rejected);
- exactly component `main`;
- exactly the initial architecture set `all,amd64`; and
- a tree containing at least one regular, non-symlink `.deb` and no other
  recognized package format.

Every Debian package is parsed strictly before success. Package names,
versions, architectures, control-field names, and emitted single-line values
are validated against the fixed production identity and for path/control
character ambiguity. Invalid, corrupt, mixed-format, traversal, symlink, and
identity-drift requests return a typed error without creating or modifying
the output path.

The preflight fixes `Origin: Repogen Repository` and
`Label: Frostyard Repository` internally rather than accepting caller
overrides. It does not restore existing metadata, generate repository files,
initialize a suite, sign, or publish. Those capabilities remain gated by
[Plan 0001](../plans/0001-frostyard-production-publisher.md) R3-R5.

### Shared immutable pool primitive (R4)

`internal/generator/deb/pool.go` defines a provider-neutral `SharedPool` used
only by later production phases. Its input is one stable, seekable staged
object plus a copied map of `pool/main/...` paths to size and lowercase
SHA-256 values obtained from signature- and checksum-verified Packages
indexes.

- Candidate bytes are streamed and must match their declared size and
  SHA-256 before the object store is called.
- A path present in the verified map is reusable only when the candidate,
  retained stanza, and stream-hashed remote bytes all agree.
- A path absent from the map is sent through atomic conditional
  create-if-absent. If creation loses a race, the winner is streamed and may
  be reused only when its size and SHA-256 match.
- Missing indexed bytes, short or failed reads, different bytes at the same
  path, unsafe paths, and malformed digests fail closed.
- Provider ETags are informational and are never treated as hashes. The
  object-store interface has no overwrite operation.

The fake-S3 tests cover indexed and unindexed reuse, opaque ETags, collisions,
unreadable streams, conditional-create races, concurrent same-byte writers,
and shared Trixie/Forky path safety. No provider adapter or advisory
permission is claimed to enforce the contract. R3 must still produce the
verified digest map, and R5 must wire a real conditional provider operation,
clean staging, ordered publication, and remote read-back.

## Debian/APT (`internal/generator/deb/`)

**Files**: `generator.go`, `parser.go`, `metadata.go`, `release.go`
**Decisions**: [ADR-0001 (unsigned InRelease)](../adr/0001-unsigned-debian-repos-emit-inrelease.md),
[ADR-0002 (shared pool layout)](../adr/0002-shared-debian-pool-layout.md),
[ADR-0003 (single `main` component)](../adr/0003-single-main-component.md)

### Output Structure

```
<output>/
  pool/main/{letter}/{name}/{file}.deb     # Package files
  dists/{codename}/
    Release                                 # Repository metadata
    InRelease                               # Cleartext-signed Release (or unsigned copy)
    Release.gpg                             # Detached GPG signature (if signed)
    main/binary-{arch}/
      Packages                              # Package index (plaintext)
      Packages.gz                           # Gzip-compressed index
```

### Key Behaviors

- Packages are organized in `pool/main/{first-letter}/{name}/` directories.
- `Packages` file is sorted alphabetically by package name.
- `Release` includes MD5, SHA1, SHA256, SHA512 checksums for all metadata files.
- Unsigned repos still create `InRelease` with Release content for modern
  apt compatibility (`[trusted=yes]`).
- Cleartext signing (InRelease) shells out to `gpg` CLI because go-crypto's
  implementation doesn't produce apt-verifiable signatures.

### Parser

`ParsePackage()` reads `.deb` files as ar archives, finds `control.tar.*`
(supports gz, xz, zst compression), extracts the `control` file, and
parses Debian control format (key: value with continuation lines).

### Incremental Mode

`ParseExistingMetadata()` reads `Packages` or `Packages.gz` files from
existing `dists/` structure and reconstructs `Package` structs.

---

## Yum/RPM (`internal/generator/rpm/`)

**Files**: `generator.go`, `parser.go`
**Decisions**: [ADR-0004 (`{version}/{arch}/` layout and version ladder)](../adr/0004-rpm-version-arch-layout-and-version-ladder.md),
[ADR-0005 (content-addressed repodata)](../adr/0005-content-addressed-repodata.md),
[ADR-0010 (`.repo` file policy)](../adr/0010-dnf-repo-file-policy.md)

### Output Structure

```
<output>/
  {version}/{arch}/
    Packages/{file}.rpm                    # Package files
    repodata/
      repomd.xml                           # Repository metadata index
      repomd.xml.asc                       # GPG signature (if signed)
      {sha256}-primary.xml.gz              # Package metadata (content-addressed)
  {repo-name}.repo                         # dnf/yum config file (if --base-url set)
```

### Key Behaviors

- Packages are grouped by version and architecture into separate repositories.
- Version is determined by priority: `--version` flag > RPM `DistroVersion`
  metadata > distro-variant default (Fedora=40, CentOS/RHEL=9).
- `primary.xml.gz` filename includes its SHA256 hash (content-addressed).
- `repomd.xml` records both a checksum of the compressed file and an
  open-checksum computed from the uncompressed `primary.xml`.
- `.repo` file uses `$releasever/$basearch` variables for dnf substitution.
- `.repo` file name priority: `--repo-name` > `--distro` > sanitized origin.
- Fedora enables `repo_gpgcheck` when signed; RHEL/CentOS add
  `metadata_expire=86400`.

### Parser

`ParsePackage()` uses `go-rpmutils` to read RPM headers, extracting name,
version, release, architecture, description, and other fields. Stores
format-specific data (Release, BuildTime, Group, DistroVersion) in
`Package.Metadata`.

### Incremental Mode

`ParseExistingMetadata()` reads `repodata/repomd.xml` to find the
primary.xml.gz location, then parses primary.xml to reconstruct packages.

---

## Alpine/APK (`internal/generator/apk/`)

**Files**: `generator.go`, `parser.go`

### Output Structure

```
<output>/
  {arch}/
    {file}.apk                              # Package files
    APKINDEX.tar.gz                         # Package index (tar.gz containing DESCRIPTION + APKINDEX)
    APKINDEX.tar.gz.SIGN.RSA.{keyname}.pub  # RSA signature (if signed)
```

### Key Behaviors

- Packages grouped by architecture into `{arch}/` directories.
- `APKINDEX` uses Alpine's letter-prefix format (`C:`, `P:`, `V:`, `A:`, etc.).
- Checksum in APKINDEX is SHA1 encoded as `Q1` + base64.
- `APKINDEX.tar.gz` is a tar.gz containing `DESCRIPTION` and `APKINDEX` files.
- Uses RSA PKCS1v15 with SHA1 for signing (Alpine's standard).

### Parser

`ParsePackage()` reads `.apk` files as gzipped tars, extracts `.PKGINFO`,
and parses Alpine's key=value format.

### Incremental Mode

`ParseExistingMetadata()` reads existing `APKINDEX.tar.gz` files,
extracts the `APKINDEX` entry, and parses it.

---

## Arch/Pacman (`internal/generator/pacman/`)

**Files**: `generator.go`, `parser.go`
**Decisions**: [ADR-0006 (dual database files)](../adr/0006-pacman-dual-database-files.md),
[ADR-0009 (binary signatures via gpg CLI)](../adr/0009-gpg-cli-and-go-crypto-signing-split.md)

### Output Structure

```
<output>/
  {arch}/
    {file}.pkg.tar.zst                      # Package files
    {file}.pkg.tar.zst.sig                  # Package signatures (if signed)
    {repo-name}.db.tar.zst                  # Database (zstd-compressed tar)
    {repo-name}.db                          # Copy of .db.tar.zst (compatibility)
    {repo-name}.db.tar.zst.sig             # Database signature (if signed)
    {repo-name}.db.sig                      # Copy of db sig (compatibility)
```

### Key Behaviors

- `--repo-name` is **required** for Pacman repositories.
- Database is a zstd-compressed tar containing `{name}-{version}/desc` entries.
- `desc` files use `%FIELD%\nvalue\n\n` format.
- Both `.db.tar.zst` and `.db` files are written (Pacman compatibility).
- Uses binary GPG signatures (not ASCII-armored) via `gpg` CLI for `.sig`
  files — both database and individual packages are signed.

### Parser

`ParsePackage()` decompresses `.pkg.tar.zst` (or `.xz`), finds `.PKGINFO`,
and parses the `key = value` format. Extracts dependencies, conflicts,
groups, build date, installed size, etc.

### Incremental Mode

`ParseExistingMetadata()` reads the existing `.db.tar.zst` database,
decompresses it, and parses each package's `desc` file.

---

## Homebrew (`internal/generator/homebrew/`)

**Files**: `generator.go`, `parser.go`

### Output Structure

```
<output>/
  Formula/{name}.rb                         # Ruby formula files
  bottles/{file}.bottle.tar.gz              # Bottle files
```

### Key Behaviors

- Bottles are grouped by package name (extracted from filename pattern
  `name--version.platform.bottle.tar.gz`).
- Generated Ruby formula uses `on_macos`/`on_linux` blocks with
  `Hardware::CPU.arm?`/`Hardware::CPU.intel?` conditionals.
- Formula class name is PascalCase conversion of package name.
- `--base-url` controls bottle download URLs in formulas.
- Uses `ShouldCopyPackage()` to skip redundant bottle copies when the
  destination already has an identical file.
- No signing support.

### Parser

Homebrew bottles don't require metadata parsing — the filename encodes
the package name, version, and platform.

### Incremental Mode

`ParseExistingMetadata()` reads existing `.rb` formula files and
reconstructs package metadata from bottle URLs and SHA256 values.

---

## systemd-sysext (`internal/generator/sysext/`)

**Files**: `generator.go`, `parser.go`

### Output Structure

```
<output>/
  ext/
    index                                    # Newline-separated list of extension names
    {name}/
      SHA256SUMS                             # Checksum file for systemd-sysupdate
      SHA256SUMS.gpg                         # Detached signature when GPG signing is configured
      {name}.transfer                        # systemd-sysupdate transfer config
      {name}_{version}_{osversion}_{arch}.raw[.compression]  # Extension images
```

### Key Behaviors

- `--base-url` is **required** for sysext repositories.
- Filename format: `NAME_VERSION_OSVERSION_ARCH.raw[.zst|.xz|.gz]`.
- Transfer files use systemd-sysupdate specifiers (`@v` for version,
  `%w` for OS version, `%a` for architecture).
- Transfer `MatchPattern` lists compressed variants in preference order
  (zst > xz > gz > raw).
- SHA256SUMS entries are deduplicated by filename.
- With `--gpg-key`, each manifest gets a detached binary `SHA256SUMS.gpg`
  signature and the generated transfer sets `Verify=true`; without a signer,
  the signature is omitted and the transfer sets `Verify=false`.

### Parser

`ParsePackage()` extracts name, version, OS version, and architecture
from the filename using `_` as delimiter (exactly 4 parts expected).

### Incremental Mode

`ParseExistingMetadata()` scans `ext/*/SHA256SUMS` files and reconstructs
package metadata from the filenames listed in each checksums file. Index
generation also enumerates those manifests, so a partial publish preserves
all previously published extension names in `ext/index`.
