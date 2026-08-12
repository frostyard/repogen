# 0009 — GPG signing splits between the gpg CLI and go-crypto, per signature kind

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Repogen needs three kinds of OpenPGP signature from one key: cleartext
(Debian `InRelease`), detached ASCII-armored (Debian `Release.gpg`, RPM
`repomd.xml.asc`), and detached binary (Pacman `.sig` for database and
packages, sysext `SHA256SUMS.gpg`). Pure-Go signing via ProtonMail/go-crypto
would avoid any runtime dependency — but its cleartext signatures are not
verifiable by apt, and pacman expects binary signatures in the old packet
format, which the gpg CLI reliably produces. Shelling out to gpg needs a
keyring, and polluting the user's `~/.gnupg` is unacceptable.

## Decision

`GPGSigner` ([internal/signer/gpg.go](../../internal/signer/gpg.go)) routes
each signature kind to the implementation that the consuming package manager
verifies, and this routing is correctness-critical:

- `SignCleartext` (Debian `InRelease`) → **gpg CLI** (`--clearsign --armor`),
  because go-crypto's cleartext output is not apt-verifiable.
- `SignDetached` (Debian `Release.gpg`, RPM `repomd.xml.asc`) →
  **go-crypto** `ArmoredDetachSign`.
- `SignDetachedBinary` / `SignDetachedBinaryFromFile` (Pacman `.sig`, sysext
  `SHA256SUMS.gpg`) → **gpg CLI** (`--detach-sign`, unarmored), producing
  the old-packet-format binary signatures pacman expects; the `FromFile`
  variant signs large packages without loading them into memory.

All paths use SHA-512 as the digest (`--digest-algo SHA512` /
`DefaultHash: crypto.SHA512`).

CLI operations run against an ephemeral GPG home: a lazily created
`repogen-gpg-*` temp directory into which the private key is imported once
(`ensureGPGHome`, guarded by `sync.Once`), reused for all CLI signing in the
run, and removed by `Close()` — which `runGeneration` defers
([internal/cli/generate.go](../../internal/cli/generate.go)). The user's
`~/.gnupg` is never touched.

## Consequences

- Every consumer verifies its signatures: apt accepts `InRelease`, pacman
  accepts the binary `.sig` files, dnf accepts `repomd.xml.asc`.
- Signing (except the pure-go-crypto detached-ASCII path) carries a hard
  runtime dependency on the `gpg` binary; unsigned generation does not.
  CI and release images must install gnupg.
- The kind→implementation routing must not be "simplified": moving cleartext
  or binary signing to go-crypto silently breaks client verification, and
  regressions only surface on real apt/pacman clients — not in unit tests
  that merely check a signature exists.
- Key material transits an on-disk temp keyring for the CLI paths; the
  directory is mode-restricted, per-run, and deleted on close, which is
  accepted for the CI environments repogen runs in (one org key,
  core ADR-0014).

## Alternatives considered

- **go-crypto for everything:** no runtime gpg dependency, but produces
  apt-rejected cleartext signatures and armored-format signatures pacman
  cannot use — the exact failures this split exists to avoid.
- **gpg CLI for everything:** workable, but the detached-ASCII path works
  in-process today; keeping it in Go avoids extra temp files and subprocess
  overhead where compatibility does not force the CLI.
- **User's default GPG home instead of an ephemeral one:** contaminates the
  invoking user's keyring, breaks concurrent runs, and makes CI
  non-hermetic.

## References

- Shapes: [design/overview.md — Signing Strategy](../design/overview.md#signing-strategy),
  [specs/generators.md](../specs/generators.md) (signing notes per format)
- Implementation: [internal/signer/gpg.go](../../internal/signer/gpg.go)
  (`SignCleartext`, `SignDetached`, `SignDetachedBinary`,
  `SignDetachedBinaryFromFile`, `ensureGPGHome`, `Close`),
  [internal/cli/generate.go](../../internal/cli/generate.go) (deferred close)
- Builds on: [core ADR-0014 — one GPG repository key](https://github.com/frostyard/core/blob/main/docs/adr/0014-single-gpg-trust-root.md)
- Related: [ADR-0001 — unsigned InRelease](0001-unsigned-debian-repos-emit-inrelease.md),
  [ADR-0006 — Pacman dual database files](0006-pacman-dual-database-files.md)
