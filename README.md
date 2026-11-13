<!-- readme-kind: tool -->
# repogen

[![CI](https://github.com/frostyard/repogen/actions/workflows/ci.yml/badge.svg)](https://github.com/frostyard/repogen/actions/workflows/ci.yml)

repogen is a command-line tool that turns a directory of packages into a
static, signed package repository, for people who serve packages from plain
web or object storage. It writes the metadata that APT, apk, pacman, Homebrew
and systemd-sysupdate read, so any web server can host the result.

**Status:** pre-release. Frostyard publishes its systemd-sysext images with it; Frostyard's Debian packages go through frostyard/apt-publisher instead.

**Documentation:** [docs/README.md](docs/README.md)

## What it does

- **Finds packages.** It scans a directory and detects each package type by
  its magic bytes, with the file name as a fallback.
  [Usage](docs/guides/usage.md)
- **Writes repository metadata for 5 formats.** Debian/APT, Alpine/APK, Arch
  Linux/pacman, Homebrew bottles and systemd-sysext images. RPM was removed in
  v0.7.0 ([ADR-0016](docs/adr/0016-remove-rpm-generation.md)).
  [Generator spec](docs/specs/generators.md)
- **Signs what it writes.** GPG for Debian, pacman and sysext (a sysext gets
  `SHA256SUMS.gpg` and transfers with `Verify=true`), RSA for Alpine. Unsigned
  output stays possible. [Signing](docs/guides/usage.md#with-signing)
- **Adds to a published repository.** Incremental mode reads the published
  metadata and adds new packages without removing old ones.
  `--skip-published-identities` skips a sysext whose identity is already
  published
  ([ADR-0018](docs/adr/0018-opt-in-identity-level-skip-for-published-sysexts.md));
  known gap: a content change without a version or revision bump is not
  republished. [Incremental mode](docs/guides/usage.md#incremental-mode)
- **Publishes to Cloudflare R2.** The `publish-to-r2` GitHub Action uploads
  payloads before metadata and refuses Debian input.
  [publish-to-r2](docs/guides/publish-to-r2.md)
- **Shows clients how to use the result.** The APT, apk, pacman, Homebrew and
  systemd-sysupdate setup for each format. [Client setup](docs/guides/client-setup.md)

## Install

Each release publishes `repogen-linux-amd64`, `repogen-linux-arm64` and
`SHA256SUMS`. From a clone, install an exact tag and its full commit; the
script checks the SHA-256 and the version that the binary embeds:

```sh
git clone https://github.com/frostyard/repogen && cd repogen
sudo scripts/install-release.sh --github-release v0.7.2 b5dccc7fe10f40d55d34b092cf7ed92dd1473c26 amd64 /usr/local/bin/repogen
```

Take the newest tag from the
[releases page](https://github.com/frostyard/repogen/releases) and its commit
from `git rev-list -n1 <tag>`. [Install](docs/guides/usage.md#installation)
also covers a build from source.

## Use

```sh
repogen generate --input-dir ./debs --output-dir ./repo --codename trixie
repogen generate --input-dir ./extensions --output-dir ./repo \
  --base-url https://example.org/repo --gpg-key ./signing-key.asc
```

Debian input needs `--codename`; sysext input needs `--base-url`.
`repogen generate --help` and [Usage](docs/guides/usage.md#configuration-options)
list every flag.

## Credits

repogen uses [spf13/cobra](https://github.com/spf13/cobra),
[ProtonMail/go-crypto](https://github.com/ProtonMail/go-crypto) and
[klauspost/compress](https://github.com/klauspost/compress). It started as a
fork of [ralt/repogen](https://github.com/ralt/repogen).

## Develop

```sh
mise install   # Go from go.mod, and the pinned golangci-lint, node, svu
make check     # format, then the verify gate
make ci        # what CI runs; needs Docker
```

[Development](docs/guides/development.md) explains each target and the tests.

## Where to go next

- [AGENTS.md](AGENTS.md): the rules for agents and contributors.
- [docs/README.md](docs/README.md): the decisions, designs, specs, plans and guides.
- [RELEASING.md](RELEASING.md): the release process and the asset contract.
- [SECURITY.md](SECURITY.md): report a vulnerability privately.
- [LICENSE](LICENSE): MIT.
