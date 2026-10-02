# 0014 — Debian incremental restore fails closed

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

[ADR-0011](0011-incremental-state-from-published-metadata.md) makes published
metadata the only incremental state, and lets a run fall back to
non-incremental generation when that metadata cannot be parsed. For Debian
this fallback regenerates `Packages` and `Release` from the new packages
alone. A corrupt index for one architecture, an index for an architecture the
run did not select, or a truncated `Packages.gz` therefore silently drops
published packages from the suite. Core ADR-0048 and Plan 0007 (R3) require
strict restore of prior Debian state.

## Decision

For Debian, `ParseExistingMetadata` returns an error wrapping
`generator.ErrNoExistingMetadata` only when `dists/<codename>` is absent. That
is the one case in which `repogen generate --incremental` initializes a fresh
suite. In every other case restore is strict, and `generate` stops before any
format writes output when it finds any of these:

- an existing index outside the selected architectures and components;
- an existing suite with no selected index;
- an unreadable `Packages` or `Packages.gz` (both are read when both
  exist, a dangling symlink counts as existing, and their decoded bytes must
  be identical);
- a malformed field line or an invalid `Size`;
- a stanza missing `Package`, `Version`, `Architecture`, `Filename` or
  `SHA256`.

`generate` restores, checks conflicts and validates every format before it
writes any of them.

Other formats keep the ADR-0011 fallback unchanged. This ADR supersedes
ADR-0011 only for Debian's parse-failure fallback; the rest of ADR-0011
stands. Signed prior verification and explicit initialize remain in the
production path ([ADR-0013](0013-separate-generic-generation-from-production-publishing.md)).

## Consequences

- A Debian incremental run never drops packages it failed to read; damaged
  prior state needs operator repair rather than being overwritten.
- Narrowing `--arch` or `--components` against an existing suite is refused;
  callers must name every existing index.
- Hand-written indexes without `SHA256` no longer restore. Generated indexes
  always carry it.
- A mixed-format run that fails Debian restore writes no format's output.

## Alternatives considered

- **Keep the fallback with a louder warning:** still drops published packages
  in unattended CI.
- **Make every format strict:** changes sysext and other publishers that rely
  on ADR-0011 without a reviewed need.

## References

- Shapes: [specs/generators.md](../specs/generators.md),
  [design/overview.md](../design/overview.md)
- Builds on: [ADR-0011](0011-incremental-state-from-published-metadata.md),
  [ADR-0013](0013-separate-generic-generation-from-production-publishing.md)
