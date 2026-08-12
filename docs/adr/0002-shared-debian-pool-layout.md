# 0002 — Debian pool is shared, first-letter sharded, not codename-scoped

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

A Debian repository separates metadata (`dists/{codename}/…`) from package
files (`pool/…`), and every `Packages` index entry records a `Filename:` path
relative to the repository root. Repogen repositories are published to static
object storage with incremental, never-delete sync (core ADR-0010), so any
path already recorded in a published index is effectively frozen — moving a
package file breaks every client that has cached the index. Frostyard
publishes the same package builds under more than one codename.

## Decision

Package files live in a single pool shared by all codenames:
`pool/main/{letter}/{name}/{file}.deb`, where `{letter}` is the first
character of the package name if it is `a`–`z`, and the literal bucket `0`
otherwise (digits, uppercase, punctuation). The pool path deliberately does
**not** include the codename
(`generateForArch` in [internal/generator/deb/generator.go](../../internal/generator/deb/generator.go):
`poolDir := filepath.Join(config.OutputDir, "pool", "main")`).

## Consequences

- Publishing the same `.deb` under two codenames stores one object: both
  `dists/{codename}` trees point their `Filename:` entries at the same pool
  path.
- Pool paths are stable and cache-friendly; the never-delete publish pipeline
  never needs to rewrite them.
- The pool layout is now a compatibility contract: changing the sharding
  scheme would orphan every `Filename:` entry in already-published indices.
  Do not change it without a migration plan and a superseding ADR.
- The `0` bucket diverges from Debian's official convention (which uses
  `lib{x}` buckets and lowercases names) — acceptable because repogen names
  its own inputs; official-archive tooling compatibility is not a goal.
- Codenames cannot ship *different builds under the same filename*; the last
  publish of a given pool path wins for all codenames sharing it.

## Alternatives considered

- **Codename-scoped pool (`pool/{codename}/…`):** doubles storage for
  dual-published packages and, worse, changing to or from it breaks published
  `Filename:` indices.
- **Flat pool (no letter sharding):** simpler, but directory listings and
  HTML indices degrade with package count; the Debian-conventional shard is
  cheap and familiar.
- **Debian's exact convention (incl. `lib{x}`):** extra complexity with no
  consumer that requires it; apt only follows `Filename:` verbatim.

## References

- Shapes: [specs/generators.md — Debian/APT](../specs/generators.md#debianapt-internalgeneratordeb),
  [design/overview.md — Format-Specific Output Structures](../design/overview.md#format-specific-output-structures)
- Implementation: [internal/generator/deb/generator.go](../../internal/generator/deb/generator.go) (`generateForArch`)
- Builds on: [core ADR-0009 — single artifact origin](https://github.com/frostyard/core/blob/main/docs/adr/0009-single-artifact-origin-repository-frostyard-org.md),
  [core ADR-0010 — publish via repogen action (never-delete)](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md)
- Related: [ADR-0003 — single `main` component](0003-single-main-component.md)
  fixes the `main` segment of these paths
