# 0003 — Debian repositories have a single component: `main`

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Debian repositories may split packages across components (`main`, `contrib`,
`non-free`, …), each with its own `dists/{codename}/{component}/binary-{arch}/`
index tree and pool subtree. Repogen serves single-vendor repositories where
every package has the same licensing and support status; component
classification has nothing to distinguish. Supporting arbitrary components
means threading a component dimension through package grouping, pool paths,
and index generation.

## Decision

The component is the constant `main`. The Debian generator hardcodes it in
both the index path (`dists/{codename}/main/binary-{arch}/`) and the pool path
(`pool/main/…`) — see `generateForArch` in
[internal/generator/deb/generator.go](../../internal/generator/deb/generator.go).

The `--components` flag (default `["main"]`,
[internal/cli/generate.go](../../internal/cli/generate.go)) is honored only by
`generateRelease`: it drives the `Components:` line of the `Release` file and
the list of index files whose checksums Release enumerates. It does **not**
create per-component trees.

Known sharp edge, accepted: passing any component other than `main` makes
generation fail — `generateRelease` asks
`CalculateReleaseFileInfos` ([internal/generator/deb/release.go](../../internal/generator/deb/release.go))
to checksum `{component}/binary-{arch}/Packages` files that were never
written, and the checksum of a missing file is a hard error. The failure is
loud rather than silently producing a broken Release, which is the acceptable
half of the edge.

## Consequences

- Generator code stays one-dimensional (arch only); pool and dists paths are
  predictable.
- `--components` is misleading as a flag: it looks like it adds components
  but only relabels the Release file, and any value beyond `main` aborts
  generation. Either the flag should be removed/validated or real component
  support added — whichever happens, it supersedes this ADR.
- Repositories that later need a second component (e.g. a non-free add-on)
  need a new ADR and a layout migration constrained by ADR-0002's frozen
  pool paths.

## Alternatives considered

- **Full multi-component support:** real work across grouping, paths, and
  incremental metadata parsing, with no current consumer; every frostyard
  repo publishes one component.
- **Remove the `--components` flag:** cleaner, but the flag documents the
  Release-file field and its default keeps `Release` conformant; removal can
  ride along with whichever change resolves the sharp edge.

## References

- Shapes: [specs/generators.md — Debian/APT](../specs/generators.md#debianapt-internalgeneratordeb),
  [design/overview.md — Configuration](../design/overview.md#configuration)
- Implementation: [internal/generator/deb/generator.go](../../internal/generator/deb/generator.go)
  (`generateForArch`, `generateRelease`),
  [internal/generator/deb/release.go](../../internal/generator/deb/release.go)
  (`CalculateReleaseFileInfos`),
  [internal/cli/generate.go](../../internal/cli/generate.go) (flag default)
- Builds on: [ADR-0002 — shared Debian pool layout](0002-shared-debian-pool-layout.md)
