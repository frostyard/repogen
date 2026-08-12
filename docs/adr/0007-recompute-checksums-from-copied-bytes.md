# 0007 — Checksums come from copied bytes, never from parsed metadata

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Every repository format repogen emits publishes checksums that clients
verify against the exact bytes they download. Between parsing a package and
publishing it, the file is copied into the output tree; a stale source, a
truncated copy, or checksum fields carried in from parsed metadata (e.g.
reconstructed during incremental mode) can all make the published hash
disagree with the published bytes — the single worst failure a repository
can have. Copies are also the dominant cost of a large regeneration, so
redundant copies of unchanged packages must be skipped safely.

## Decision

Two rules, uniform across all six generators (deb, rpm, apk, pacman,
homebrew, sysext):

1. **After every copy, recompute.** Whenever a package file is copied into
   the output tree, all published checksums and the size are recalculated
   from the destination file, overwriting whatever the parser or existing
   metadata claimed (e.g. `utils.CalculateChecksums(finalDstPath)` after
   `utils.CopyFile` in each generator's copy loop).
2. **Skip copies only via the three-stage check** in
   `utils.ShouldCopyPackage`
   ([internal/utils/fileops.go](../../internal/utils/fileops.go)):
   same cleaned path → skip; destination missing → copy; size mismatch →
   copy; sizes equal → compare destination SHA-256 against the package's
   recorded hash, copying on mismatch or when hashing fails.

A deliberate carve-out supports incremental publishing against remote
storage: when a package listed in existing metadata exists at neither its
source path nor under the output directory, `ShouldCopyPackage` returns
"no copy, no error" — the bytes already live in S3/R2 and their metadata
entry is carried forward untouched (the recompute rule never fires because
no copy happened).

This is the invariant every new generator must repeat: copy through
`ShouldCopyPackage`, then hash the destination.

## Consequences

- Published metadata can never disagree with published bytes for any file
  repogen itself wrote; corruption during copy is caught at hash time on the
  next run (size or SHA mismatch forces re-copy).
- Unchanged packages cost one `stat` (plus one hash when sizes tie),
  making large incremental regenerations cheap.
- The missing-locally carve-out trusts previously published metadata for
  files repogen cannot see; a corrupted object in remote storage is not
  detected by repogen. That verification belongs to the publish pipeline.
- Generators must not "optimize away" the post-copy recompute — parser
  checksums exist only to drive the skip check, never to be published from
  a copied file.

## Alternatives considered

- **Trust parser-computed checksums:** hashes the source, not what was
  published; misses copy corruption and stale destinations.
- **Always copy, always hash:** correct but rewrites every package on every
  run, defeating incremental publishing and inflating sync uploads.
- **mtime-based skip check:** object-store round-trips and CI checkouts do
  not preserve mtimes reliably; size+hash is deterministic.

## References

- Shapes: [design/overview.md — Package Copy Optimization](../design/overview.md#package-copy-optimization),
  [specs/generators.md](../specs/generators.md) (all six generator sections)
- Implementation: [internal/utils/fileops.go](../../internal/utils/fileops.go)
  (`ShouldCopyPackage`, `CopyFile`),
  [internal/utils/checksum.go](../../internal/utils/checksum.go)
  (`CalculateChecksums`); copy loops in each
  `internal/generator/*/generator.go`
- Builds on: [core ADR-0010 — publish via repogen action (never-delete)](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md)
- Related: [ADR-0011 — incremental state from published metadata](0011-incremental-state-from-published-metadata.md)
