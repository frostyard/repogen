# 0011 — Incremental mode reconstructs state from published metadata

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

The org publish pipeline (core ADR-0010) adds packages to already-published
repositories: each CI run contributes new files and must regenerate indices
that still list everything published before. That requires knowing the
repository's current contents. Repogen runs are stateless CI jobs against a
synced copy of the output tree — there is no database, and any side-channel
state file could drift from what clients actually see.

## Decision

The repository's own published metadata is the only source of incremental
state. Every generator implements `ParseExistingMetadata`
([internal/generator/generator.go](../../internal/generator/generator.go)),
which reads the format's real index (Debian `Packages`/`Packages.gz`, RPM
`repomd.xml` → primary.xml, Alpine `APKINDEX.tar.gz`, Pacman `.db.tar.zst`,
Homebrew formula `.rb` files, sysext `SHA256SUMS`) and reconstructs
`models.Package` entries from it.

Under `--incremental`, `runGeneration`
([internal/cli/generate.go](../../internal/cli/generate.go)) merges those
reconstructed packages with the newly scanned ones and regenerates all
metadata from the union. Conflicts are detected by format-aware identity
(`PackageIdentity` in
[internal/utils/package_identity.go](../../internal/utils/package_identity.go):
name:version:arch, plus Release for RPM, name:version for Homebrew) and
either abort the run or are skipped with `--skip-duplicates`. If existing
metadata cannot be parsed, the run logs a warning and falls back to
non-incremental generation rather than failing.

Reconstructed packages need not exist as local files — their bytes live in
remote storage; ADR-0007's copy logic carries their entries forward without
touching them.

## Consequences

- Incremental publishing needs zero infrastructure beyond the repository
  itself: what clients can read is exactly what the next run builds on, so
  state can never drift from the published truth.
- Parsers must round-trip: every field a generator writes into metadata that
  it later needs must survive `ParseExistingMetadata`. A lossy parser
  silently degrades existing entries on the next incremental run — this is
  the standing test obligation for all six formats.
- The parse-failure fallback to normal mode is forgiving but dangerous in
  the wrong pipeline: combined with a sync that deletes, it would drop the
  existing repository. It is safe only because the org pipeline never
  deletes (core ADR-0010).
- Duplicate identity handling is coarse (whole-package identity, no version
  comparison); replacing a published package requires a version bump, not a
  re-upload.

## Alternatives considered

- **A state/manifest file next to the repository:** second source of truth
  that can disagree with the indices clients consume; must itself be synced
  and locked.
- **Listing package files in the output tree instead of parsing metadata:**
  the output tree is a partial local copy in CI (remote-only packages are
  absent), and file listings lack the metadata needed to regenerate indices
  without re-downloading and re-parsing every package.
- **Failing hard when existing metadata is unparsable:** safer in
  delete-capable pipelines, but blocks first-time publishes into empty
  prefixes and recovery from corrupt indices; the warn-and-fallback keeps
  those cases self-healing under never-delete semantics.

## References

- Shapes: [design/overview.md — Incremental Mode](../design/overview.md#incremental-mode),
  [specs/generators.md](../specs/generators.md) (per-format "Incremental
  Mode" sections)
- Implementation: [internal/generator/generator.go](../../internal/generator/generator.go)
  (`ParseExistingMetadata`),
  [internal/cli/generate.go](../../internal/cli/generate.go)
  (`runGeneration` incremental branch),
  [internal/utils/package_identity.go](../../internal/utils/package_identity.go)
- Builds on: [core ADR-0010 — publish via repogen action (never-delete)](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md),
  [ADR-0007 — checksums from copied bytes](0007-recompute-checksums-from-copied-bytes.md)
