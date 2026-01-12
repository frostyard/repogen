# Sysext Test Fixtures

Test fixture files for systemd-sysext repository generation.

## Filename Format

Sysext files must follow the naming convention:

```
NAME_VERSION_ARCH.raw[.COMPRESSION]
```

Where:

- `NAME`: Extension name (must not contain underscores)
- `VERSION`: Version string (must not contain underscores)
- `ARCH`: Architecture (e.g., x86-64, arm64; must not contain underscores)
- `COMPRESSION`: Optional compression suffix (`.zst`, `.xz`, or `.gz`)

## Examples

- `docker_24.0.5_x86-64.raw` - Docker extension v24.0.5 for x86-64 (uncompressed)
- `docker_24.0.5_x86-64.raw.zst` - Docker extension v24.0.5 for x86-64 (zstd compressed)
- `nvidia_550.54.14_arm64.raw.xz` - Nvidia driver extension for arm64 (xz compressed)

## Generated Output Structure

When repogen processes sysext files, it creates:

```
<output>/ext/<extension-name>/
    SHA256SUMS           # Checksum file for systemd-sysupdate
    <extension>.raw*     # Extension files
```

Example:

```
repo/ext/docker/
    SHA256SUMS
    docker_24.0.5_x86-64.raw.zst
    docker_25.0.0_x86-64.raw.zst
```

## SHA256SUMS Format

Standard checksum format compatible with `sha256sum`:

```
<sha256hash>  <filename>
```

Note: Two spaces between hash and filename per shasum convention.
