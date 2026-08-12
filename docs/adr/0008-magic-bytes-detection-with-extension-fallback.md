# 0008 — Package detection sniffs magic bytes first, falls back to filenames

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Repogen scans an input directory and must classify each file as one of six
package types (deb, rpm, apk, pacman, homebrew bottle, sysext image) before
any format-specific parser runs. File extensions are convention, not proof —
CI artifacts get renamed, and some formats share containers (an `.apk` is a
gzipped tar; pacman packages are tars under zstd/xz/gzip). Some target
formats have no distinctive container at all: a homebrew bottle is just a
`tar.gz`, and a sysext image is a filesystem image whose only reliable
marker is the org's filename grammar (core ADR-0007).

## Decision

`DetectPackageType`
([internal/scanner/detector.go](../../internal/scanner/detector.go)) reads
the first 512 bytes of each file and classifies with hardcoded well-known
magic constants, mixed with filename checks per format:

- **deb, rpm — OR semantics:** matching magic (`!<arch>\ndebian` ar header,
  `0xED 0xAB 0xEE 0xDB` RPM lead) **or** the `.deb`/`.rpm` extension is
  sufficient. A correctly-built package with a wrong name and a correctly
  named file with unsniffable content are both accepted.
- **apk — AND semantics:** gzip magic **and** the `.apk` extension, because
  gzip alone matches too much.
- **pacman — filename gate plus per-compression check:** the name must
  contain `.pkg.tar.`; within that, zstd/xz magic or the matching suffix
  (or bare `.pkg.tar`) decides.
- **homebrew, sysext — filename only:** `.bottle.tar.gz`/`.bottle.tar`
  substring, and the `.raw[.zst|.xz|.gz]` suffixes respectively; their
  content is not distinctive enough to sniff.

Unmatched files return `TypeUnknown` and are skipped by the scanner rather
than failing the run.

## Consequences

- Renamed or extension-less deb/rpm artifacts are still detected; the
  scanner tolerates the messy directories CI produces.
- OR semantics accept mislabeled files: `foo.deb` containing garbage is
  classified as a deb and only fails later, in the parser, with a per-file
  warning (parse failures skip the file, they don't abort the run —
  `runGeneration` in [internal/cli/generate.go](../../internal/cli/generate.go)).
  Detection is a router, not a validator; validation belongs to parsers.
- Filename-only formats (homebrew, sysext) are trivially spoofable and
  wholly dependent on naming discipline — for sysext that discipline is the
  org filename grammar of core ADR-0007.
- New formats must slot into this ladder explicitly and mind ordering: the
  gzip magic is shared by apk, `.pkg.tar.gz`, and bottles, so their checks
  are disambiguated by filename and must stay that way.

## Alternatives considered

- **Extension-only detection:** simplest, but breaks on renamed artifacts
  and cannot tell `.pkg.tar.gz` compression variants from other tarballs
  without the same filename logic anyway.
- **Magic-only detection:** impossible for homebrew/sysext (no distinctive
  magic) and ambiguous for anything gzipped.
- **Full container parsing at detection time:** accurate but reads every
  archive twice; the parser already does the deep read immediately after.

## References

- Shapes: [design/overview.md — Data Flow](../design/overview.md#data-flow)
  (scan step), [specs/generators.md](../specs/generators.md) (per-format
  parser sections)
- Implementation: [internal/scanner/detector.go](../../internal/scanner/detector.go)
  (`DetectPackageType`),
  [internal/scanner/filesystem.go](../../internal/scanner/filesystem.go)
- Builds on: [core ADR-0007 — sysext filename pattern](https://github.com/frostyard/core/blob/main/docs/adr/0007-frostyard-sysext-filename-pattern.md)
