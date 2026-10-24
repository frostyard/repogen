# Repogen - Universal Repository Generator

[![Test](https://github.com/frostyard/repogen/actions/workflows/test.yml/badge.svg)](https://github.com/frostyard/repogen/actions/workflows/test.yml)

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
> ([ADR-0016](docs/adr/0016-remove-rpm-generation.md)) because its parser
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
([ADR-0015](docs/adr/0015-remove-the-superseded-production-path.md); history
in [ADR-0013](docs/adr/0013-separate-generic-generation-from-production-publishing.md)).

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

Release binaries now have one contract: the sole tag workflow runs pinned
GoReleaser and publishes `repogen-linux-{amd64,arm64}` with `SHA256SUMS`.
Each binary embeds the exact version and full commit shown by
`repogen version --short`. Consumers must name an exact tag and commit;
`scripts/install-release.sh --github-release` retrieves both files from the
fixed GitHub release origin, verifies the selected SHA-256 entry, and checks
embedded identity before installation. The checksum is unsigned and
same-origin, so authenticity depends on GitHub and repository release controls;
there is no independent release signature or attestation. No `latest` lookup
is accepted. Darwin assets from the retired workflow are not produced.

The existing `repogen generate` command remains generic and keeps its
supported formats and unsigned behavior. It has no implicit Debian codename:
Debian input requires `--codename`, and a `--suite` must equal it. Its Debian
incremental mode initializes only when `dists/<codename>` is absent; an
unreadable, malformed or unselected existing index stops the run before any
format writes ([ADR-0014](docs/adr/0014-debian-incremental-restore-fails-closed.md)).

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
```

**S3 Workflow Examples:**

The incremental mode is particularly powerful when combined with S3. You can sync only the metadata files (not the packages themselves), add new packages, and regenerate.

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

  # Homebrew
      --base-url string         Base URL for Homebrew bottles
```

## Generated Repository Structures

### Debian/APT Repository

```
repo/
├── dists/
│   └── stable/
│       ├── InRelease              # Cleartext signed Release (or unsigned copy for unsigned repos)
│       ├── Release                # Main metadata
│       ├── Release.gpg            # Detached GPG signature (only for signed repos)
│       └── main/
│           └── binary-amd64/
│               ├── Packages        # Package metadata
│               ├── Packages.gz     # Compressed
│               └── Release
└── pool/
    └── main/
        └── {letter}/              # First letter of package name
            └── {package-name}/
                └── package.deb
```

**Using the Repository:**

```bash
# Add repository (unsigned)
echo "deb [trusted=yes] http://your-server.com/repo stable main" | sudo tee /etc/apt/sources.list.d/repo.list

# Add repository (signed)
# First, import the public key
wget -qO - http://your-server.com/repo/public.key | sudo apt-key add -
echo "deb http://your-server.com/repo stable main" | sudo tee /etc/apt/sources.list.d/repo.list

# Update and install
sudo apt update
sudo apt install package-name
```

### Alpine/APK Repository

```
repo/
└── x86_64/
    ├── APKINDEX.tar.gz         # Package index
    ├── APKINDEX.tar.gz.SIGN.RSA.repogen.pub  # RSA signature
    └── package-1.0.0-r0.apk
```

**Using the Repository:**

```bash
# Add repository
echo "http://your-server.com/repo" | sudo tee -a /etc/apk/repositories

# With signing (copy public key first)
sudo cp repogen.pub /etc/apk/keys/
echo "http://your-server.com/repo" | sudo tee -a /etc/apk/repositories

# Update and install
sudo apk update
sudo apk add package-name
```

### Arch Linux/Pacman Repository

```
repo/
└── x86_64/
    ├── myrepo.db.tar.zst       # Package database
    ├── myrepo.db               # Symlink/copy of .db.tar.zst
    ├── myrepo.db.tar.zst.sig   # GPG signature (if signed)
    ├── myrepo.db.sig           # Symlink/copy of signature
    ├── package-1.0.0-1-x86_64.pkg.tar.zst
    └── package-1.0.0-1-x86_64.pkg.tar.zst.sig  # Package signature (if signed)
```

**Using the Repository:**

```bash
# Add repository to /etc/pacman.conf
sudo tee -a /etc/pacman.conf <<EOF
[myrepo]
Server = http://your-server.com/repo/\$arch
SigLevel = Optional TrustAll
EOF

# With GPG signing (import public key first)
sudo pacman-key --add public.key
sudo pacman-key --lsign-key KEY_ID
# Update SigLevel in /etc/pacman.conf:
# SigLevel = Required DatabaseOptional

# Update and install
sudo pacman -Sy
sudo pacman -S package-name
```

### Homebrew Tap

```
repo/
├── Formula/
│   └── package-name.rb         # Ruby formula
└── bottles/
    └── package--1.0.0.monterey.bottle.tar.gz
```

**Using the Repository:**

```bash
# Add tap (assuming repo is in GitHub)
brew tap username/repo https://github.com/username/repo

# Install package
brew install package-name
```

### systemd-sysext Repository

systemd-sysext repositories are designed to work with systemd-sysupdate for automatic system extension updates.

**Filename Format:**

Sysext files must follow a strict naming convention:

```
NAME_VERSION_OSVERSION_ARCH.raw[.COMPRESSION]
```

Where:

- `NAME`: Extension name (must not contain underscores)
- `VERSION`: Version string (must not contain underscores)
- `OSVERSION`: Operating system version from `/etc/os-release` VERSION_ID (must not contain underscores)
- `ARCH`: Architecture using systemd naming (e.g., x86-64, arm64; must not contain underscores)
- `COMPRESSION`: Optional compression suffix (`.zst`, `.xz`, or `.gz`)

Examples:

- `docker_24.0.5_13_x86-64.raw.zst` (Debian Trixie, OS version 13)
- `nvidia_550.54.14_12_arm64.raw.xz` (Debian Bookworm, OS version 12)
- `podman_5.0.0_22.04_x86-64.raw.zst` (Ubuntu 22.04)

**Generated Structure:**

```
repo/
└── ext/
    └── docker/
        ├── SHA256SUMS                      # Checksum file for systemd-sysupdate
        ├── SHA256SUMS.gpg                  # Detached signature (when --gpg-key is set)
        ├── docker.transfer                 # systemd-sysupdate transfer configuration
        ├── docker_24.0.5_13_x86-64.raw.zst
        └── docker_25.0.0_13_x86-64.raw.zst
```

**Note:** The `--base-url` flag is required when generating sysext repositories. This is used to generate the `.transfer` configuration files with the correct source URL. Pass `--gpg-key` to create a detached binary `SHA256SUMS.gpg` signature and generate transfers with `Verify=true`. Without a signing key, no signature is emitted and generated transfers retain `Verify=false`.

```bash
repogen generate \
  --input-dir ./extensions \
  --output-dir ./repo \
  --base-url https://example.com/repo \
  --gpg-key ./repository-signing-key.asc
```

**Using with systemd-sysupdate:**

Repogen generates a `.transfer` file for each extension that can be copied to `/etc/sysupdate.d/`:

```bash
# Copy the generated transfer file
sudo cp repo/ext/docker/docker.transfer /etc/sysupdate.d/50-docker.conf

# Check for updates
systemd-sysupdate list

# Download and apply updates
systemd-sysupdate update
```

The generated transfer file looks like:

```ini
[Transfer]
Verify=true

[Source]
Type=url-file
Path=https://example.com/repo/ext/docker/
MatchPattern=docker_@v_%w_%a.raw.zst \
             docker_@v_%w_%a.raw.xz \
             docker_@v_%w_%a.raw.gz \
             docker_@v_%w_%a.raw

[Target]
Type=regular-file
Path=/var/lib/extensions.d/
MatchPattern=docker_@v_%w_%a.raw.zst \
             docker_@v_%w_%a.raw.xz \
             docker_@v_%w_%a.raw.gz \
             docker_@v_%w_%a.raw
```

**Specifier Reference:**

- `@v`: Version (e.g., `24.0.5`)
- `%w`: OS version from the running system (`VERSION_ID` in `/etc/os-release`); must match the `OSVERSION` embedded in the sysext filename (e.g., `13` for Debian Trixie, `22.04` for Ubuntu)
- `%a`: Architecture in systemd naming (e.g., `x86-64`, `arm64`)

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

## Repository Structure Details

### Debian Repository Format

Repogen generates Debian repositories following the standard format:

- **InRelease**: Cleartext signed Release file (preferred by modern apt). For unsigned repositories, contains the same content as Release file without signature wrapper.
- **Release**: Contains metadata and checksums of all index files
- **Release.gpg**: Detached signature of Release file (only for signed repositories)
- **Packages**: RFC 822-style package metadata
- **pool/**: Organized by first letter of package name

Key fields in Packages file:

- Package, Version, Architecture
- Filename (relative to repo root)
- Size, MD5sum, SHA1, SHA256, SHA512
- Description, Depends, Maintainer

### Alpine Repository Format

Repogen generates Alpine repositories in the apk v2 format:

- **APKINDEX.tar.gz**: Contains DESCRIPTION and APKINDEX files
- **APKINDEX**: Letter:value format package metadata
  - C: Checksum (Q1 prefix + base64 SHA1)
  - P: Package name
  - V: Version
  - A: Architecture
  - S: Size
  - T: Description
  - L: License
  - D: Dependencies (space-separated)

### Pacman Repository Format

Repogen generates Pacman (Arch Linux) repositories:

- **Database file** (e.g., `myrepo.db.tar.zst`): Tarball containing package metadata
- **desc files**: Package information in Pacman format within the database
- **Package files**: `.pkg.tar.zst`, `.pkg.tar.xz`, or `.pkg.tar.gz`
- **Signatures**: Binary GPG signatures (`.sig` files) for database and packages
- **Database structure**: Each package has a directory with `desc` file containing:
  - `%FILENAME%`, `%NAME%`, `%VERSION%`, `%DESC%`
  - `%CSIZE%`, `%ISIZE%` (compressed and installed size)
  - `%MD5SUM%`, `%SHA256SUM%`
  - `%ARCH%`, `%BUILDDATE%`, `%PACKAGER%`, `%URL%`, `%LICENSE%`
  - `%DEPENDS%`, `%CONFLICTS%`, `%GROUPS%`

### Homebrew Tap Format

Repogen generates Homebrew taps with:

- **Formula/**: Ruby formula files auto-generated from bottles
- **bottles/**: Binary packages
- Multi-architecture support (arm64, x86_64)
- Platform detection from filename patterns

Bottle filename format: `{package}--{version}.{platform}.bottle.tar.gz`

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

### Example 5: Pacman Repository with Signing

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

### Continuous Integration

Example GitHub Actions workflow:

```yaml
name: Test
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: "1.23"

      - name: Build test packages
        run: make test-packages-docker

      - name: Run tests
        run: make test
```

### Manual Testing

You can manually test repositories:

```bash
# Generate a test repository
./repogen generate --input-dir test/fixtures/debs --output-dir /tmp/test-repo

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

### Integration tests fail

- Ensure Docker is installed and running: `docker version`
- Build test packages first: `make test-packages`
- Check Docker can pull images: `docker pull debian:bookworm`
- Increase timeout for slow systems: `go test -timeout 30m ./test`

## GitHub Action

Repogen provides a legacy reusable GitHub Action for publishing non-Debian
package formats to repositories hosted on Cloudflare R2 storage.

> **Production status:** The current action is the generic legacy publisher.
> It requires an exact Repogen tag and commit but still uses broad
> synchronization for supported non-Debian formats. Debian is rejected
> structurally; the action cannot initialize or reconcile Frostyard
> Trixie/Forky. Frostyard Debian publication moved to `frostyard/apt-publisher`
> (core ADR-0055). This action stays for sysexts and other non-Debian
> formats.

### Quick Start

```yaml
- name: Publish to repository
  uses: frostyard/repogen/.github/actions/publish-to-r2@<reviewed-action-commit>
  with:
    r2-account-id: ${{ secrets.R2_ACCOUNT_ID }}
    r2-access-key-id: ${{ secrets.R2_ACCESS_KEY_ID }}
    r2-secret-access-key: ${{ secrets.R2_SECRET_ACCESS_KEY }}
    r2-bucket: my-repo-bucket
    packages-dir: ./dist
    package-type: sysext
    base-url: https://extensions.example.com/repo
    repogen-version: v1.2.3
    repogen-commit: <40-character-release-commit>
```

### Full Example: Publishing Sysext Images

```yaml
name: Build and Publish Sysext
on:
  push:
    tags:
      - "v*.*.*"

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<reviewed-checkout-commit>

      - name: Build sysext image
        run: |
          # Build your sysext (example)
          ./build-sysext.sh
          # Output: dist/myext_1.0.0_13_x86-64.raw.zst

      - name: Publish to repository
        uses: frostyard/repogen/.github/actions/publish-to-r2@<reviewed-action-commit>
        with:
          r2-account-id: ${{ secrets.R2_ACCOUNT_ID }}
          r2-access-key-id: ${{ secrets.R2_ACCESS_KEY_ID }}
          r2-secret-access-key: ${{ secrets.R2_SECRET_ACCESS_KEY }}
          r2-bucket: my-extensions
          repo-prefix: repo
          packages-dir: ./dist
          package-type: sysext
          base-url: https://extensions.example.com/repo
          repogen-version: v1.2.3
          repogen-commit: <40-character-release-commit>
```

### Action Inputs

| Input                  | Required | Default     | Description                                                                              |
| ---------------------- | -------- | ----------- | ---------------------------------------------------------------------------------------- |
| `r2-account-id`        | Yes      | -           | Cloudflare R2 Account ID                                                                 |
| `r2-access-key-id`     | Yes      | -           | Cloudflare R2 Access Key ID                                                              |
| `r2-secret-access-key` | Yes      | -           | Cloudflare R2 Secret Access Key                                                          |
| `r2-bucket`            | Yes      | -           | Cloudflare R2 Bucket name                                                                |
| `packages-dir`         | Yes      | -           | Directory containing packages to add                                                     |
| `package-type`         | Yes      | -           | Package type; `deb` is rejected by this legacy action                                    |
| `base-url`             | No\*     | -           | Base URL for the repository (\*required for `sysext`)                                    |
| `repo-prefix`          | No       | -           | Path prefix in R2 bucket                                                                 |
| `gpg-private-key`      | No       | -           | GPG private key (base64 or ASCII armored)                                                |
| `gpg-passphrase`       | No       | -           | GPG key passphrase                                                                       |
| `rsa-private-key`      | No       | -           | RSA private key for Alpine (PEM format)                                                  |
| `rsa-passphrase`       | No       | -           | RSA key passphrase                                                                       |
| `rsa-key-name`         | No       | `repogen`   | Key name for Alpine signatures                                                           |
| `codename`             | No       | -           | Codename for Debian repos; no default, required for `deb`; `stable` is refused           |
| `suite`                | No       | -           | Suite for Debian repos (defaults to codename); `stable` is refused                       |
| `components`           | No       | `main`      | Components for Debian repos                                                              |
| `architectures`        | No       | `all,amd64` | Architectures (comma-separated)                                                          |
| `origin`               | No       | -           | Repository origin name                                                                   |
| `label`                | No       | -           | Repository label                                                                         |
| `repo-name`            | No\*     | -           | Repository name (\*required for `pacman`)                                                |
| `repogen-version`      | Yes      | -           | Exact v-prefixed Repogen release tag                                                     |
| `repogen-commit`       | Yes      | -           | Exact 40-character commit embedded in the binary                                         |
| `skip-duplicates`      | No       | `false`     | Skip packages that already exist instead of failing                                      |
| `purge-cache`          | No       | `false`     | Purge Cloudflare cache after upload                                                      |
| `cloudflare-zone`      | No\*     | -           | Cloudflare Zone ID (\*required if `purge-cache` is `true`)                               |
| `cloudflare-api-token` | No\*     | -           | Cloudflare API Token with Cache Purge permission (\*required if `purge-cache` is `true`) |

### Action Outputs

| Output           | Description                                |
| ---------------- | ------------------------------------------ |
| `packages-added` | Number of packages added to the repository |

### How It Works

1. **Downloads repogen** from GitHub releases
2. **Syncs existing metadata** from R2 (only metadata files, not packages)
3. **Runs repogen** in incremental mode to add new packages
4. **Uploads** the updated repository back to R2

The action uses incremental mode, which means:

- Existing packages are preserved
- Only metadata files are synced locally; a sysext metadata restore failure
  aborts instead of being treated as an empty repository
- Sysext identity includes OSVersion, so matching OS 13 and OS 14 artifacts
  coexist
- A same-identity sysext is skipped only when its SHA-256 is unchanged;
  changed or unverifiable bytes fail

The action is not a Debian publisher. Debian uses the separate protected
writer. A sysext caller must serialize the full restore/generate/upload cycle
across all extensions because `ext/index` is shared. The current R2 upload
cannot atomically replace `SHA256SUMS` and `SHA256SUMS.gpg`; do not activate
new production credentials until that provider-side commit boundary has been
reviewed and approved.

### Setting Up R2 Credentials

1. Go to Cloudflare Dashboard → R2 → Manage R2 API Tokens
2. Create an API token with "Object Read & Write" permissions
3. Note the Access Key ID and Secret Access Key
4. Add these as repository secrets in GitHub:
   - `R2_ACCOUNT_ID`
   - `R2_ACCESS_KEY_ID`
   - `R2_SECRET_ACCESS_KEY`

### Setting Up Cache Purge (Optional)

If your R2 bucket is served through a Cloudflare domain, you can configure the action to automatically purge the CDN cache after uploading. This ensures clients immediately see the updated repository metadata.

1. Go to Cloudflare Dashboard → Your Domain → Overview (note the Zone ID in the right sidebar)
2. Go to Profile → API Tokens → Create Token
3. Create a Custom Token with permission: **Zone → Cache Purge → Purge**
4. Restrict the token to the specific zone hosting your repository
5. Add these as repository secrets in GitHub:
   - `CLOUDFLARE_ZONE` (the Zone ID)
   - `CLOUDFLARE_API_TOKEN` (the API token you created)
6. Enable cache purging in your workflow:

```yaml
- uses: frostyard/repogen/.github/actions/publish-to-r2@<reviewed-action-commit>
  with:
    # ... other inputs ...
    purge-cache: "true"
    cloudflare-zone: ${{ secrets.CLOUDFLARE_ZONE }}
    cloudflare-api-token: ${{ secrets.CLOUDFLARE_API_TOKEN }}
```

### Configuring Public Access

To serve your repository publicly:

1. In Cloudflare Dashboard → R2 → Your Bucket → Settings
2. Enable "Public Access" or connect a custom domain
3. Use the public URL as your `base-url`

## Contributing

Contributions are welcome! Please feel free to submit pull requests or open issues for bugs and feature requests.

### Development Workflow

```bash
# Clone and build
git clone https://github.com/frostyard/repogen
cd repogen
make build

# Make changes and test
make fmt              # Format code
make lint             # Run linter (requires golangci-lint)
make test             # Run all tests

# Before committing
make test-packages    # Ensure test packages build
make test             # Ensure all tests pass
```

## License

MIT License.

## For Maintainers

### Creating a Release

See **[RELEASING.md](RELEASING.md)** for the release process, the required
gates, and the authoritative asset contract. A release needs a human to create
and push the tag.

A `v*.*.*` tag runs `.github/workflows/release.yml`, which uses GoReleaser
(`.goreleaser.yml`) to publish three assets:

| Asset | Purpose |
|---|---|
| `repogen-linux-amd64` | Raw Linux amd64 executable |
| `repogen-linux-arm64` | Raw Linux arm64 executable |
| `SHA256SUMS` | SHA-256 digest list for both executables |

The release does not build native packages, Darwin binaries, or a repository
archive.

### CI/CD Workflows

- **Test Workflow** (`.github/workflows/test.yml`): Runs on PRs and pushes to main
- **Release Workflow** (`.github/workflows/release.yml`): Runs on version tags (v*.*.\*)

## Acknowledgments

- Built with [spf13/cobra](https://github.com/spf13/cobra) for CLI
- Uses [ProtonMail/go-crypto](https://github.com/ProtonMail/go-crypto) for GPG operations
- Uses [klauspost/compress](https://github.com/klauspost/compress) for fast compression

## See Also

- [Debian Repository Format](https://wiki.debian.org/DebianRepository/Format)
- [Alpine APK Spec](https://wiki.alpinelinux.org/wiki/Apk_spec)
- [Homebrew Tap Documentation](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)
