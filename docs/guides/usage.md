# Usage

> **⚠️ Alpha Software Warning**
>
> Repogen is alpha software and has not been extensively battle-tested in production environments. While it includes comprehensive test coverage and has been validated with package managers, use it with caution for critical infrastructure. Always verify generated repositories work correctly with your package manager before deploying to production.

Repogen is a CLI tool that generates static repository structures for multiple package managers. It scans directories for packages, generates appropriate metadata files, and signs repositories with GPG/RSA keys.

## Supported Package Types

- **Debian/APT** (.deb packages)
- **Alpine/APK** (.apk packages)
- **Arch Linux/Pacman** (.pkg.tar.zst, .pkg.tar.xz, .pkg.tar.gz)
- **Homebrew** (bottle files)
- **systemd-sysext** (.raw, .raw.zst, .raw.xz, .raw.gz)

> **RPM is not supported.** RPM generation was removed in v0.7.0
> ([ADR-0016](../adr/0016-remove-rpm-generation.md)) because its parser
> depended on the unmaintained `golang.org/x/crypto/openpgp` package
> (GO-2026-5932, no fix). `.rpm` files are ignored by `generate`, and the
> `--distro`, `--version` and `--gpg-key-url` flags no longer exist. Use
> Repogen v0.6.0 or earlier if you still need RPM repositories.

## Features

- **Automatic Package Detection**: Scans directories and auto-detects package types using magic bytes
- **Metadata Generation**: Creates all necessary index and metadata files for each repository type
- **Repository Signing**: Signs repositories with GPG (Debian/Pacman/sysext) or RSA (Alpine) keys
- **Deterministic Debian Metadata**: Canonical package, field, architecture,
  component, checksum, and gzip output with a controlled Release timestamp
- **Safe Debian No-op**: Preserves valid existing Release signatures when the
  canonical metadata is unchanged
- **Unsigned Repository Support**:
  - Always generates InRelease files (required by Debian Trixie)
  - InRelease contains Release content without signature for unsigned repos
  - Compatible with `[trusted=yes]` apt option
- **Static Output**: Generates static file structures that can be served by any web server
- **Simple Component Structure**: Uses single component/pool structure for simplicity

## Installation

### From a release

Each `vMAJOR.MINOR.PATCH` release publishes `repogen-linux-amd64`,
`repogen-linux-arm64` and `SHA256SUMS`. [RELEASING.md](../../RELEASING.md) holds
the release contract.

Run `scripts/install-release.sh` from a clone. It needs the exact tag and its
full 40-character commit:

```bash
scripts/install-release.sh --github-release v0.7.0 c3c9f63c0f9b04106cfdb84318f5ec935566ab89 amd64 /usr/local/bin/repogen
```

The destination must be writable; use `sudo` for `/usr/local/bin`. The run
prints:

```
installed 0.7.0 c3c9f63c0f9b04106cfdb84318f5ec935566ab89 from repogen-linux-amd64
```

The architecture is `amd64` or `arm64`; `x86_64` and `aarch64` are also
accepted. Get the commit of a tag with `git rev-list -n1 <tag>`.

Release binaries have one contract: the sole tag workflow runs pinned
GoReleaser and publishes `repogen-linux-{amd64,arm64}` with `SHA256SUMS`.
Each binary embeds the exact version and full commit shown by
`repogen version --short`. Consumers must name an exact tag and commit;
`scripts/install-release.sh --github-release` retrieves both files from the
fixed GitHub release origin, verifies the selected SHA-256 entry, and checks
embedded identity before installation. The checksum is unsigned and
same-origin, so authenticity depends on GitHub and repository release controls;
there is no independent release signature or attestation. No `latest` lookup
is accepted. Darwin assets from the retired workflow are not produced.

### From Source

```bash
# Clone the repository
git clone https://github.com/frostyard/repogen
cd repogen

# Build
go build -o repogen ./cmd/repogen

# Optional: Install to PATH
sudo cp repogen /usr/local/bin/
```

### Prerequisites

- Go 1.26.0 or later (the `toolchain` line in `go.mod` builds with Go 1.26.10)

## Usage

### Basic Usage

```bash
# Scan current directory and generate repositories
repogen generate

# Scan specific directory
repogen generate --input-dir /path/to/packages --output-dir /path/to/repo

# Debian packages need an explicit codename (there is no default)
repogen generate --input-dir ./debs --output-dir ./repo --codename trixie

# Enable verbose logging
repogen generate -v
```

### Frostyard Debian publication

Repogen does not publish Frostyard Debian packages.
[`frostyard/apt-publisher`](https://github.com/frostyard/apt-publisher) is the
only Frostyard Debian writer (core
[ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md)).
Repogen is a general-purpose generator plus the `publish-to-r2` action for
sysext and other non-Debian formats. The former production commands, which
shipped in v0.5.2, were removed
([ADR-0015](../adr/0015-remove-the-superseded-production-path.md); history
in [ADR-0013](../adr/0013-separate-generic-generation-from-production-publishing.md)).

The `publish-to-r2` action refuses the `stable` suite and any Debian input
(`scripts/validate-publish-target.sh`, `TestPublishTargetRefusesStable`,
`TestPublishTargetRefusesDebianPackagesForAnyType`). Generic
`repogen generate` writes `dists/stable` locally when `--codename stable` is
passed explicitly.

#### Debian `generate` requirements

| Item | Code | Tests |
|------|------|-------|
| Explicit codename | `internal/cli/generate.go`: Debian input requires `--codename`, and a `--suite` must equal it | `TestGenerateDebianRequiresExplicitCodename`, `TestGenerateDebianRejectsSuiteDifferentFromCodename` |
| Strict incremental restore | `internal/generator/deb/parser.go` (ADR-0014) | `TestParseExistingMetadata*`, `TestGenerateIncrementalDebianFailsOnCorruptArchWithoutMutation`, `TestGenerateIncrementalDebianCorruptionStopsMixedRunBeforeAnyWrite`, `TestGenerateIncrementalDebianInitializesEmptyOutput` |
| Signed no-op reuse | `internal/generator/deb/release_signature.go` | `TestParseTrustedKeyRing`, `TestVerifyReleaseSignatures` |

The existing `repogen generate` command remains generic and keeps its
supported formats and unsigned behavior. It has no implicit Debian codename:
Debian input requires `--codename`, and a `--suite` must equal it. Its Debian
incremental mode initializes only when `dists/<codename>` is absent; an
unreadable, malformed or unselected existing index stops the run before any
format writes ([ADR-0014](../adr/0014-debian-incremental-restore-fails-closed.md)).

### Incremental Mode

Incremental mode allows you to add new packages to an existing repository without regenerating everything from scratch. This is useful when:

- You have a large repository and only want to add new package versions
- You're syncing from S3 and don't want to download all package files locally
- You want faster repository updates

**How It Works:**

1. Reads existing metadata files (Packages, trust.db, repomd.xml, etc.) from the output directory
2. Adds only new packages without removing existing ones
3. Errors if a package with the same name+version already exists (use `--skip-duplicates` to skip instead)
4. Regenerates metadata files with both existing and new packages
5. Re-signs changed metadata if signing is enabled; unchanged canonical
   Debian metadata preserves cryptographically verified Release signatures

**Basic Incremental Usage:**

```bash
# Add new packages to existing repository
# (Debian input also needs --codename)
repogen generate \
  --input-dir ./new-packages \
  --output-dir ./repo \
  --incremental

# Skip duplicates instead of failing (useful for nightly builds)
repogen generate \
  --input-dir ./new-packages \
  --output-dir ./repo \
  --incremental \
  --skip-duplicates

# Skip rebuilt sysexts whose identity is already published (sysext only)
repogen generate \
  --input-dir ./built-extensions \
  --output-dir ./repo \
  --incremental \
  --gpg-key /path/to/private.asc \
  --skip-published-identities \
  --skipped-report /tmp/repogen-skipped.txt
```

`--skip-published-identities` reads the published identities from the
signature-verified `SHA256SUMS` of each extension and skips every incoming
sysext whose identity `name:version:OSVersion:arch` is already listed, whatever
its bytes and whatever its compression suffix. It requires `--incremental` and
`--gpg-key`, it accepts only sysext input, and it fails closed: it refuses to
run without a signing key, and a missing or unverifiable manifest signature
stops the run. A repository published unsigned therefore cannot use the mode.
`--skipped-report FILE` writes the published
payload path (`ext/NAME/FILE`) of each skipped sysext, one per line. When every
identity was skipped the run succeeds without writing output. mkosi builds are
not byte-reproducible, so this is how a nightly rebuild of the same extension
succeeds ([ADR-0018](../adr/0018-opt-in-identity-level-skip-for-published-sysexts.md)).

**Known gap:** a content change without a version or `SYSEXT_REVISION` bump
keeps its identity, so it is skipped and **is not republished**; the old bytes
stay live. Bump the version or the revision to publish new bytes.

**S3 Workflow Examples:**

The incremental mode is particularly powerful when combined with S3. You can sync only the metadata files (not the packages themselves), add new packages, and regenerate.

A manual upload must also upload packages before metadata, so live metadata
never lists a package that is not in the bucket
([ADR-0017](../adr/0017-upload-payloads-before-metadata.md)). For sysext,
the two commands are joined with `&&`, so a failed payload upload stops before
the metadata goes up:

```bash
# Payloads first; the metadata upload runs only if that one succeeded
aws s3 sync ./repo s3://my-bucket/repo --exclude '*' \
    --include '*.raw' --include '*.raw.zst' \
    --include '*.raw.xz' --include '*.raw.gz' \
  && aws s3 sync ./repo s3://my-bucket/repo \
    --exclude '*.raw' --exclude '*.raw.zst' \
    --exclude '*.raw.xz' --exclude '*.raw.gz'
```

`scripts/upload-to-r2.sh` does the full ordered upload: it verifies every
payload is in the bucket, uploads metadata and indexes last, and retries each
phase.

#### Debian Repository

```bash
# Sync only metadata from S3 (not package files)
aws s3 sync s3://my-bucket/repo/dists ./repo/dists --delete

# Add new packages with repogen
repogen generate --input-dir ./new-packages --output-dir ./repo --codename trixie --incremental

# Sync everything back to S3 (without --delete to preserve existing packages)
aws s3 sync ./repo s3://my-bucket/repo
```

#### Pacman Repository

```bash
# Sync only database files (exclude actual package files)
aws s3 sync s3://my-bucket/repo/x86_64 ./repo/x86_64 \
  --exclude "*.pkg.tar.zst" \
  --exclude "*.pkg.tar.zst.sig"

# Add new packages
repogen generate \
  --input-dir ./new-packages \
  --output-dir ./repo \
  --repo-name myrepo \
  --incremental

# Sync back (without --delete to preserve existing packages)
aws s3 sync ./repo s3://my-bucket/repo
```

#### APK Repository

```bash
# Sync metadata only
aws s3 cp s3://my-bucket/repo/x86_64/APKINDEX.tar.gz ./repo/x86_64/APKINDEX.tar.gz

# Add new packages
repogen generate --input-dir ./new-packages --output-dir ./repo --incremental

# Sync back (without --delete to preserve existing packages)
aws s3 sync ./repo s3://my-bucket/repo
```

#### systemd-sysext Repository

```bash
# Sync only SHA256SUMS metadata files
aws s3 sync s3://my-bucket/repo/ext ./repo/ext \
  --exclude "*.raw" \
  --exclude "*.raw.zst" \
  --exclude "*.raw.xz" \
  --exclude "*.raw.gz"

# Add new extensions
repogen generate --input-dir ./new-extensions --output-dir ./repo --incremental

# Sync back (without --delete to preserve existing extensions)
aws s3 sync ./repo s3://my-bucket/repo
```

**Important Notes:**

- Incremental mode will error if a package with the same name+version already exists (conflict detection)
- Use `--skip-duplicates` to silently skip packages that already exist instead of failing (useful for nightly builds)
- Use `--skip-published-identities` (sysext only, needs `--gpg-key`) to skip
  rebuilt extensions
  whose identity the signature-verified `SHA256SUMS` already lists, whatever
  their bytes; `--skipped-report FILE` records what was skipped. A content
  change without a version or revision bump keeps its identity and is not
  republished (ADR-0018)
- If metadata files don't exist, it falls back to normal mode automatically.
  For Debian, existing but unreadable, malformed or unselected metadata is an
  error, never a silent fallback (ADR-0014)
- Package files from existing metadata don't need to be present locally
- You can use incremental mode with or without signing
- For systemd-sysext repositories, an existing `ext/` tree is always verified,
  merged, and retained even without `--incremental`; omitting the flag never
  authorizes deletion of manifest-backed extensions

### With Signing

#### Debian/Pacman (GPG Signing)

```bash
# Generate a signed Debian repository (--codename is required for Debian)
repogen generate \
  --input-dir ./packages \
  --output-dir ./repo \
  --codename trixie \
  --gpg-key /path/to/private.key \
  --gpg-passphrase "your-passphrase"

# Generate signed Pacman repository (requires --repo-name)
repogen generate \
  --input-dir ./packages \
  --output-dir ./repo \
  --repo-name "myrepo" \
  --gpg-key /path/to/private.key \
  --gpg-passphrase "your-passphrase"
```

#### Alpine (RSA Signing)

```bash
# Generate signed Alpine repository
repogen generate \
  --input-dir ./packages \
  --output-dir ./repo \
  --rsa-key /path/to/rsa-private.pem \
  --rsa-passphrase "your-passphrase" \
  --key-name "mykey"
```

### Configuration Options

```bash
repogen generate [flags]

Flags:
  # Input/Output
  -i, --input-dir string        Input directory to scan (default ".")
  -o, --output-dir string       Output directory (default "./repo")
  -v, --verbose                 Enable verbose logging

  # Incremental Mode
      --incremental             Add new packages to existing repository without removing existing ones
      --skip-duplicates         Skip packages that already exist instead of failing
      --skip-published-identities
                                Skip sysexts whose identity is already in the signature-verified SHA256SUMS
      --skipped-report string   Write the published payload path of each skipped sysext to this file

  # GPG Signing (Debian, Pacman, sysext)
  -k, --gpg-key string          Path to GPG private key
  -p, --gpg-passphrase string   GPG key passphrase

  # RSA Signing (Alpine)
      --rsa-key string          Path to RSA private key
      --rsa-passphrase string   RSA key passphrase
      --key-name string         Key name for Alpine signatures (default "repogen")

  # Repository Metadata
      --origin string           Repository origin name
      --label string            Repository label
      --repo-name string        Repository name (required for Pacman)
      --codename string         Codename for Debian repos (required when Debian packages are present)
      --suite string            Suite for Debian repos (defaults to codename)
      --components strings      Components for Debian repos (default [main])
      --arch strings            Architectures to support (default [amd64])

  # Output
      --html-index              Generate HTML directory index pages for browser-friendly navigation

  # Homebrew and sysext
      --base-url string         Base URL for Homebrew bottles and sysext transfer files (required for sysext)
```

## GPG Key Setup

### Generate GPG Key for Signing

```bash
# Generate key
gpg --full-generate-key

# Export private key
gpg --export-secret-keys YOUR_KEY_ID > private.key

# Export public key (for distribution)
gpg --export --armor YOUR_KEY_ID > public.key
```

### Generate RSA Key for Alpine

```bash
# Generate RSA private key
openssl genrsa -out private.pem 2048

# Extract public key
openssl rsa -in private.pem -pubout -out public.pem

# With passphrase
openssl genrsa -aes256 -out private.pem 2048
```

## Examples

### Example 1: Simple Debian Repository

```bash
# Organize packages
mkdir -p packages
cp *.deb packages/

# Generate repository
repogen generate --input-dir packages --output-dir /var/www/repo

# Serve with nginx
sudo ln -s /var/www/repo /usr/share/nginx/html/repo
```

### Example 2: Multi-Architecture Debian Repository

```bash
repogen generate \
  --input-dir packages \
  --output-dir /var/www/repo \
  --arch amd64,arm64,i386 \
  --codename bookworm \
  --origin "My Company" \
  --label "Production Packages"
```

### Example 3: Homebrew Tap with Multiple Bottles

```bash
# Organize bottles
mkdir bottles
cp *.bottle.tar.gz bottles/

# Generate tap
repogen generate \
  --input-dir bottles \
  --output-dir homebrew-tap \
  --base-url "https://github.com/username/homebrew-tap/releases/download/v1.0"
```

### Example 4: Pacman Repository with Signing

```bash
# Organize packages
mkdir packages
cp *.pkg.tar.zst packages/

# Generate signed repository
repogen generate \
  --input-dir packages \
  --output-dir /var/www/repo \
  --repo-name "myrepo" \
  --arch x86_64,aarch64 \
  --gpg-key ~/.gnupg/secring.gpg \
  --gpg-passphrase "secret"

# Export public key for users
gpg --export --armor YOUR_KEY_ID > /var/www/repo/myrepo.key
```

## Troubleshooting

### No packages found

- Check that package files have correct extensions (.deb, .apk, .pkg.tar.zst/.pkg.tar.xz/.pkg.tar.gz, .bottle.tar.gz)
- Verify magic bytes in files (packages may be corrupted)
- Use `--verbose` flag to see detailed scanning output

### GPG signing fails

- Verify GPG key is not encrypted or provide correct passphrase
- Check that private key file is readable
- Ensure go-crypto library supports your key type

### Repository not working

- Verify all metadata files were generated in output directory
- Check file permissions (should be readable by web server)
- Test with unsigned repository first (`[trusted=yes]` for apt)
- Review web server logs for 404s

### Debian Trixie requires InRelease files

Even with `[trusted=yes]`, Debian Trixie expects InRelease files to exist. Repogen now automatically generates InRelease files for all repositories:

- **Signed repositories**: InRelease contains cleartext signature
- **Unsigned repositories**: InRelease contains Release content (no signature)

This ensures compatibility with both old (Bookworm) and new (Trixie) Debian releases.
