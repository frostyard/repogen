# Install repogen

repogen runs on Linux. This page names the requirements, then installs repogen
from a release or builds it from source.

## Requirements

- A 64-bit Linux system, amd64 or arm64.
- To build from source, Go 1.27.4 or later (the `go` line in `go.mod`).

## Install from a release

Each release publishes `repogen-linux-amd64`, `repogen-linux-arm64` and
`SHA256SUMS`. From a clone, install an exact tag and its full commit; the
script checks the SHA-256 and the version that the binary embeds:

```sh
git clone https://github.com/frostyard/repogen && cd repogen
sudo scripts/install-release.sh --github-release v0.7.4 c2f9f54931520d7801c8339db8b9931d8737f395 amd64 /usr/local/bin/repogen
```

Take the newest tag from the
[releases page](https://github.com/frostyard/repogen/releases) and its commit
from `git rev-list -n1 <tag>`.

The [installation section of the usage guide](../guides/usage.md#installation)
holds the full release contract: the checksum check, the identity check and the
limits of an unsigned checksum.

## Build from source

```sh
git clone https://github.com/frostyard/repogen
cd repogen
go build -o repogen ./cmd/repogen
sudo cp repogen /usr/local/bin/   # optional
```

!!! note "Check the version"

    Run `repogen version --short` after the install. A release build prints
    the version and the full commit it was built from.

## First run

Generate a repository from a directory of packages. Debian input needs
`--codename`:

```sh
repogen generate --input-dir ./debs --output-dir ./repo --codename trixie
```

Continue with the [usage guide](../guides/usage.md), or look up every option in
the [command reference](../reference/cli.md).
