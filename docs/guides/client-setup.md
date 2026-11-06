# Client setup

What repogen writes for each package format, and the commands a client uses to
read it.

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

## References

- [Debian Repository Format](https://wiki.debian.org/DebianRepository/Format)
- [Alpine APK Spec](https://wiki.alpinelinux.org/wiki/Apk_spec)
- [Homebrew Tap Documentation](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)
