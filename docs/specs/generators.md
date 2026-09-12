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

## Frostyard production Debian validation (`internal/cli/production.go`)

`repogen validate-production` is an R2-R3 validation and strict-restore
boundary, not a generator. It accepts no implicit target values and writes no
output. A valid request must provide:

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
overrides. It then requires exactly one explicit operation:

- `initialize` requires no trusted key or expected prior digest and succeeds
  only when `dists/<codename>` is authoritatively absent; or
- `reconcile` requires one trusted public key and a 64-character lowercase
  expected prior Release SHA-256.

Reconcile reads only regular, non-symlink metadata files. It requires Release,
InRelease, and Release.gpg; verifies both signatures resolve to the same
trusted fingerprint; requires InRelease's signed payload to equal Release
byte-for-byte; enforces fixed Origin, Label, Suite, Codename, component,
architectures, `Acquire-By-Hash: yes`, a valid Date, and no Valid-Until; and
rejects unknown or repeated Release fields.

Each MD5Sum, SHA1, SHA256, and SHA512 section must advertise exactly
`main/binary-{all,amd64}/Packages{,.gz}`. Every size and digest is checked
against the local bytes. The gzip stream must contain no trailing bytes and
must expand exactly to its plain peer. Every package stanza must parse,
contain all required identity/path/size/digest fields, match its architecture
index, and use a safe `pool/main/` path. One successful architecture never
masks another architecture's failure. Each canonical index's SHA-256
by-hash object must also exist and be byte-identical, so the prior InRelease
remains usable while reconcile updates canonical paths.

Neither validation operation writes. The separate R4-R5 library boundary
described below is not invoked by this command and has no provider
credentials or external publication authority.

The R6 deterministic generation behavior above is implemented in the generic
generator but is not wired into this read-only production command. It confers
no production publication or storage authority.

### Shared immutable pool primitive (R4)

`internal/generator/deb/pool.go` defines a provider-neutral `SharedPool` used
by the R5 production transaction. Its input is one stable, seekable staged
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
and shared Trixie/Forky path safety.

### Signed publication transaction (R5)

`StageProductionTransaction` and `PublishProductionTransaction` compose
strict restore authority and `SharedPool`. Staging rejects an existing
directory, missing signing, unsafe package metadata, package digest drift,
mutable suite names, and incomplete reconcile authority. It emits canonical
all/amd64 indexes, deterministic gzip framing, SHA-256 by-hash copies,
Release/InRelease/Release.gpg, and compact request/result manifests.

Publishing first verifies the full expected-prior mutable object map under a
codename lock, then writes only:

1. conditionally created or verified `pool/main` objects;
2. immutable by-hash indexes;
3. canonical Packages indexes;
4. Release and Release.gpg; and
5. InRelease last.

Every successful write is streamed back and checked for exact size and
SHA-256. Reconcile uses compare-and-replace against each verified prior
digest. The interface exposes neither broad sync nor deletion. It has no
S3/R2 adapter, credentials, production CLI, or publication authority;
fake-store failure injection and local GPG/APT fixtures demonstrate the
transaction semantics without representing a production canary.

### Atomic local production generation (R7)

`GenerateLocalProductionRepository` composes production staging and local
commit. `CommitLocalProductionTransaction` accepts an already staged
transaction. Both remain library APIs with no production CLI or provider
credentials.

The local commit creates a complete sibling generation on the same
filesystem as the output:

1. verify every staged object and the exact reconcile prior;
2. copy the existing repository while excluding the target codename, rejecting
   symlinks and non-regular files;
3. preserve unrelated suite regular-file bytes, sizes, and complete
   file/directory modes; create regenerated suite directories and new pool
   parents at a fixed `0755` independent of process umask; and accept an
   existing pool object only when its size and SHA-256 match;
4. install and re-hash every staged pool, index, by-hash, Release, InRelease,
   and Release.gpg object;
5. verify the prior tree did not change during staging and synchronize the
   complete candidate; and
6. make one Linux `renameat2` commit, using no-replace for an absent output or
   exchange for an existing output.

There is no remove-then-rename interval and no rollback-shaped second rename.
Any error before the atomic switch removes only the private candidate and
leaves the old tree unchanged unless a concurrent external writer caused the
detected drift. Snapshots compare each regular file's SHA-256, size, and full
mode and each directory's full mode, including special bits. They do not
compare or preserve uid/gid ownership, extended attributes, ACLs, or
timestamps. After a successful exchange, obsolete prior bytes are private
cleanup and cannot turn the committed generation into a reported failure.
Tests inject failure before every observed initialize and reconcile staging,
signing, copy, verification, synchronization, and commit step. Production
staging rejects a nil signer; generic generation retains its existing
unsigned `InRelease` behavior.

R7 reads and hashes the complete prior repository and copies all retained
content, including `pool/`. A generation therefore incurs O(repository size)
read, write, hashing, and synchronization work and requires roughly 2x
transient repository space.

R8 persists an exclusive sibling recovery journal containing the output name,
generation name, existence state, and exact prior/candidate tree digests
before the atomic namespace switch. It synchronizes the output parent after
both journal creation and `renameat2`. Recovery re-hashes both possible trees:
it completes a pending switch only when the sibling is the exact candidate
and the output is the exact prior, or cleans private prior bytes when the
output is already the exact candidate. Unknown, corrupt, missing, symlinked,
or third-state trees fail closed. Cleanup after the durable switch is private
and cannot roll a visible candidate backward.

### Durable Debian intake and recovery (R8)

`internal/intake` defines retained, create-only records for provenance,
artifacts, canonical requests, per-target receipts, attempts, result
manifests, and result pointers. The local store synchronizes immutable files
and newly created directories, performs read-after-write verification, lists
receipts by prefix, and uses filesystem locks for process-safe target
serialization. A submission key can replay identical request bytes but cannot
name different bytes. The adapter-provided authenticated principal must match
the request producer; policy digests are retained and a non-nil current-policy
authorizer runs before every writer attempt.

Scheduled and manual recovery use the same full receipt enumeration. Receipts
for one Debian codename run in increasing sequence and stop on the first
failure; different codenames run independently. Every referenced object is
re-hashed on replay. Attempts are append-only, and neither workflow dispatch
nor process success records completion. A content-addressed result manifest
and its create-only request pointer are written only after the writer verifies
the complete public object set.

`ProductionRecoveryWriter` rebuilds the signed B5 transaction through a
caller-supplied intake-only builder. `RecoverProductionTransaction` holds the
codename lock and classifies every planned object before writing: exact
candidate bytes are idempotent no-ops, exact prior mutable bytes may advance,
and authoritatively absent immutable bytes may be created. Any permission or
transport error, unknown initialize-prefix object, missing reconcile prior,
checksum mismatch, incomplete body, or other third state stops before another
write. Shared-pool objects retain conditional-create and full-byte read-back,
and `InRelease` remains the final write.

This package supplies no HTTP service, object-store adapter, workflow,
credential, signer configuration, or production authorization. The local
store and tests are explicitly fixtures/protected-local primitives; a real
adapter must separately prove authentication and least-privilege enforcement.

## Debian/APT (`internal/generator/deb/`)

**Files**: `generator.go`, `parser.go`, `metadata.go`, `release.go`,
`production_transaction.go`, `local_generation.go`
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
      by-hash/SHA256/{digest}               # Production transaction only
```

### Key Behaviors

- Packages are organized in `pool/main/{first-letter}/{name}/` directories.
- `Packages` stanzas use a total package name, lexicographic version-string,
  architecture, and filename order. Remaining package fields break
  exact-identity ties, and arbitrary control fields are emitted in sorted
  field-name order.
- Generation validates all selected pool destinations before writing. Inputs
  that resolve to one path are reusable only when their size and SHA-256
  agree. Local source identities are derived from their bytes; unavailable
  incremental objects use the retained metadata identity. Conflicting
  contents fail without creating output.
- `Packages.gz` uses a fixed gzip timestamp so identical Packages bytes
  produce identical compressed bytes.
- `Release` includes MD5, SHA1, SHA256, SHA512 checksums for all metadata files.
- Release architectures, components, and checksum paths are sorted, and
  `GenerateReleaseFileAt`/`NewGeneratorWithClock` accept one explicit
  publication timestamp.
- If canonical Release bytes match the prior generation when rendered with
  its Date, both prior signatures must verify against the configured signer's
  public key before Release, InRelease, and Release.gpg are preserved without
  signing calls. Missing, malformed, wrong-key, or invalid signatures force a
  newly timestamped signed generation.
- Unsigned repos still create `InRelease` with Release content for modern
  apt compatibility (`[trusted=yes]`).
- Cleartext signing (InRelease) shells out to `gpg` CLI because go-crypto's
  implementation doesn't produce apt-verifiable signatures.
- The production transaction never emits unsigned metadata and switches the
  visible generation only by writing the fully read-back InRelease last.

### Parser

`ParsePackage()` reads `.deb` files as ar archives, finds `control.tar.*`
(supports gz, xz, zst compression), extracts the `control` file, and
parses Debian control format (key: value with continuation lines).

### Incremental Mode

`ParseExistingMetadata()` reads `Packages` or `Packages.gz` files from
existing `dists/` structure and reconstructs `Package` structs for the
generic incremental command. Its legacy fallback behavior is not used by the
production path; production reconcile uses the strict signed verifier
described above.

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
- Extension names and SHA256SUMS entries are sorted. Entries are deduplicated
  by filename only when their digests agree; conflicting duplicate filenames
  fail generation.
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
