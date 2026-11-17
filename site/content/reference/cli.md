# Commands

repogen has 2 commands: `repogen generate` and `repogen version`.
`repogen generate` scans an input directory for packages and writes repository
metadata, and signatures when a key is given.
Use `repogen generate --help` for the list that matches your build.

```sh
repogen generate [flags]
```

Debian input needs `--codename`, sysext input needs `--base-url`, and pacman
input needs `--repo-name`.

## Input and output

| Flag | Meaning |
| --- | --- |
| `-i`, `--input-dir` | Input directory to scan (default `.`). |
| `-o`, `--output-dir` | Output directory (default `./repo`). |
| `-v`, `--verbose` | Enable verbose logging. |
| `--html-index` | Generate HTML directory index pages for browser-friendly navigation. |

## Repository metadata

| Flag | Meaning |
| --- | --- |
| `--origin` | Repository origin name. |
| `--label` | Repository label. |
| `--repo-name` | Repository name for pacman database files (required for pacman). |
| `--codename` | Codename for Debian repositories (required when Debian packages are present). |
| `--suite` | Suite for Debian repositories (defaults to the codename; when given, it must equal `--codename`). |
| `--components` | Components for Debian repositories (default `main`). |
| `--arch` | Architectures to support (default `amd64`). |
| `--base-url` | Base URL for Homebrew bottles and sysext transfer files (required for sysext). |

## Signing

| Flag | Meaning |
| --- | --- |
| `-k`, `--gpg-key` | Path to the GPG private key (Debian, pacman, sysext). |
| `-p`, `--gpg-passphrase` | GPG key passphrase. |
| `--rsa-key` | Path to the RSA private key (Alpine). |
| `--rsa-passphrase` | RSA key passphrase. |
| `--key-name` | Key name for Alpine signatures (default `repogen`). |

## Incremental mode

| Flag | Meaning |
| --- | --- |
| `--incremental` | Add new packages to an existing repository without removing existing ones. |
| `--skip-duplicates` | In incremental mode, skip packages that already exist instead of failing. |
| `--skip-published-identities` | In incremental mode, skip each sysext whose identity (name, version, OS version, arch) is already listed in the signature-verified `SHA256SUMS`, whatever its bytes or compression suffix (sysext only). Needs `--incremental` and `--gpg-key`. |
| `--skipped-report` | With `--skip-published-identities`, write the published payload path (`ext/NAME/FILE`) of each skipped sysext to this file, one per line. |
| `--sysext-keep-versions` | Keep only the newest N versions of each sysext per name, OS version and arch in `SHA256SUMS`, in systemd-sysupdate version order (sysext only; `0`, the default, keeps every version). |
| `--pruned-report` | With `--sysext-keep-versions`, write the published payload path of each pruned sysext to this file, one per line. |

!!! warning "Known gap"

    A content change without a version or revision bump keeps its identity and
    is not republished. Bump the version or the revision to publish new bytes.

The [usage guide](../guides/usage.md#incremental-mode) explains incremental
mode, and the [signing section](../guides/usage.md#with-signing) shows signed
runs.

## Other commands

`repogen version` prints the version and the full commit that the binary
embeds. `--short` prints them as machine-readable fields, and `--json` prints
them as JSON.
