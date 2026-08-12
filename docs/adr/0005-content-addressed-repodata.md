# 0005 — Content-addressed repodata with open-checksum discipline

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Repogen repositories are served from object storage behind a CDN and updated
by incremental sync that never deletes (core ADR-0010). A mutable metadata
filename (`primary.xml.gz`) is poison in that environment: a CDN or client
cache can serve a stale index against a fresh `repomd.xml`, and checksum
validation then fails — or worse, silently mismatched metadata is used.

The createrepo convention already solves this: metadata files are named by
their own hash, and `repomd.xml` (small, always re-fetched) is the only
mutable entry point.

## Decision

The RPM generator names the primary metadata file by the SHA-256 of its
**compressed** bytes: `repodata/{sha256}-primary.xml.gz`, where the hash is
computed over the gzipped payload before writing
(`generateForVersionArch` in
[internal/generator/rpm/generator.go](../../internal/generator/rpm/generator.go)).

`repomd.xml` records the two hashes with strict discipline
(`generateRepomdXML`, same file):

- `<checksum>` — SHA-256 of the compressed file (identical to the hash in
  the filename);
- `<open-checksum>` — SHA-256 of the uncompressed `primary.xml`, computed
  independently, plus `size`/`open-size` for both forms.

Only `repomd.xml` (and its signature `repomd.xml.asc`) are written to fixed
names; everything else in `repodata/` is content-addressed.

## Consequences

- CDN caching and no-delete sync are safe: a new publish writes a new
  `{sha256}-primary.xml.gz` alongside the old one and atomically swaps the
  pointer by overwriting `repomd.xml`. No cache can pair stale primary data
  with fresh repomd without failing checksum verification.
- Repodata grows without bound: superseded `{sha256}-primary.xml.gz` files
  are never deleted by repogen or the never-delete publish pipeline. Accepted
  cost — each file is small; garbage collection would need out-of-band
  tooling and a new ADR.
- The filename hash and the recorded `<checksum>` must be the same value
  computed from the same bytes; any refactor that compresses twice or hashes
  the wrong form breaks client verification.

## Alternatives considered

- **Fixed `primary.xml.gz` name:** the classic stale-cache failure under CDN
  + incremental sync; rejected outright.
- **Deleting superseded repodata on publish:** conflicts with the org's
  never-delete publish semantics and would break clients mid-download during
  a sync window.
- **Content-addressing `repomd.xml` too:** something must be the fixed entry
  point; `repomd.xml` is the format-defined root and is signed to protect it.

## References

- Shapes: [specs/generators.md — Yum/RPM](../specs/generators.md#yumrpm-internalgeneratorrpm),
  [design/overview.md — Format-Specific Output Structures](../design/overview.md#format-specific-output-structures)
- Implementation: [internal/generator/rpm/generator.go](../../internal/generator/rpm/generator.go)
  (`generateForVersionArch`, `generateRepomdXML`)
- Builds on: [core ADR-0010 — publish via repogen action (never-delete)](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md)
- Related: [ADR-0004 — RPM `{version}/{arch}/` layout](0004-rpm-version-arch-layout-and-version-ladder.md)
