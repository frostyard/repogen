# 0006 — Pacman databases are written twice: `.db.tar.zst` and `.db`

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Pacman's server-side convention (repo-add) writes the database as
`{name}.db.tar.zst` and exposes `{name}.db` as a symlink to it; client
configurations reference the `.db` name. Repogen output is published to
object storage (S3/R2), which has no symlinks — a static tree must contain
real objects for both names or clients configured either way will 404.

## Decision

The Pacman generator writes the database bytes twice per architecture
directory: once as `{name}.db.tar.zst` and once as a byte-identical copy
`{name}.db` (`generateForArch` in
[internal/generator/pacman/generator.go](../../internal/generator/pacman/generator.go)).
When signing is configured, the detached binary signature is likewise written
twice: `{name}.db.tar.zst.sig` and `{name}.db.sig`, from the same signature
bytes.

Both copies are produced from a single in-memory `dbData` / `signature`
buffer in one generation pass — there is no path where one name is written
without the other.

## Consequences

- Both common client spellings of the database URL work from dumb static
  storage; no symlink or redirect support is required of the host.
- The pair (and the signature pair) is an invariant: the two objects must
  never diverge. Any future change that streams the database to disk instead
  of buffering it must still guarantee both names get identical bytes, and a
  partial publish that syncs one file but not the other serves inconsistent
  data until the next sync — the publish pipeline should treat the four
  files as a unit.
- Storage/transfer cost doubles for the database (it is small relative to
  packages).

## Alternatives considered

- **Only `{name}.db`:** works for default client configs, but breaks tooling
  and mirrors that expect the canonical `.db.tar.zst` artifact.
- **Only `{name}.db.tar.zst`:** breaks the standard client `Server=` usage
  where pacman requests `{name}.db`.
- **Server-side redirect/alias:** ties the repository to a specific host's
  rewrite features; repogen targets any static origin (core ADR-0009).

## References

- Shapes: [specs/generators.md — Arch/Pacman](../specs/generators.md#archpacman-internalgeneratorpacman),
  [design/overview.md — Format-Specific Output Structures](../design/overview.md#format-specific-output-structures)
- Implementation: [internal/generator/pacman/generator.go](../../internal/generator/pacman/generator.go)
  (`generateForArch`)
- Builds on: [core ADR-0009 — single artifact origin](https://github.com/frostyard/core/blob/main/docs/adr/0009-single-artifact-origin-repository-frostyard-org.md)
- Related: [ADR-0009 — GPG signing split](0009-gpg-cli-and-go-crypto-signing-split.md)
  explains why the `.sig` files are binary, CLI-produced signatures
