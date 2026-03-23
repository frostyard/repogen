# Generator Details

Each generator implements `generator.Generator` and produces a complete
repository structure from a list of `models.Package` entries.

## Debian/APT (`internal/generator/deb/`)

**Files**: `generator.go`, `parser.go`, `metadata.go`, `release.go`

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
- No signing support (transfer files set `Verify=false`).

### Parser

`ParsePackage()` extracts name, version, OS version, and architecture
from the filename using `_` as delimiter (exactly 4 parts expected).

### Incremental Mode

`ParseExistingMetadata()` scans `ext/*/SHA256SUMS` files and reconstructs
package metadata from the filenames listed in each checksums file.
