# 0004 — RPM layout is `{version}/{arch}/` with a version-resolution ladder

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

dnf/yum clients expand `$releasever` and `$basearch` in a repo's `baseurl`,
so a single `.repo` file can serve every Fedora/CentOS/RHEL release and
architecture — but only if the published tree is actually sharded by those
two variables. RPM packages do not reliably carry the distro release they
were built for: some encode it in the Release tag (`…fc40…`), some don't.
Repogen must place every package into exactly one `{version}/{arch}/` shard.

## Decision

The RPM generator writes one complete repository (its own `repodata/` and
`Packages/`) per `{version}/{arch}/` pair under the output directory
(`generateForVersionArch` in
[internal/generator/rpm/generator.go](../../internal/generator/rpm/generator.go)).
The shard is exactly what makes the `.repo` file's
`baseurl=…/$releasever/$basearch` substitution resolve (ADR-0010).

The version for each package is resolved by a fixed ladder
(`getPackageVersion`, same file):

1. `--version` flag (`config.Version`) — explicit override wins;
2. the package's `DistroVersion` metadata, parsed from the RPM Release tag;
3. a hardcoded default per `--distro` variant: `fedora` → `40`,
   `centos` → `9`, `rhel` → `9`, anything else → `40`.

Architecture falls back to `x86_64` when the RPM header carries none.

## Consequences

- One `.repo` file serves all releases and arches; clients land on their own
  shard automatically.
- Packages with different resolved versions produce disjoint repositories —
  a mixed input directory fans out correctly in one run.
- The hardcoded defaults go stale: when Fedora 40 stops being a sensible
  default the constant must be bumped (or `--version` passed everywhere).
  A stale default silently files packages under an old release path.
- A wrong ladder outcome (e.g. missing `DistroVersion` metadata plus no flag)
  publishes into a shard clients of other releases never look at — the
  failure mode is a missing package, not an error.

## Alternatives considered

- **Flat layout (no version/arch sharding):** `$releasever/$basearch` in the
  baseurl would 404; a per-release `.repo` file would be needed instead,
  multiplying published config files.
- **Require `--version` always:** removes the stale-default risk but breaks
  the common case of publishing a mixed directory of packages built for
  several releases in one invocation.
- **Fail when the ladder reaches the default rung:** stricter, but the
  variant default is what lets metadata-poor packages publish at all; the
  chosen behavior favors publishing with a documented default.

## References

- Shapes: [specs/generators.md — Yum/RPM](../specs/generators.md#yumrpm-internalgeneratorrpm),
  [design/overview.md — Format-Specific Output Structures](../design/overview.md#format-specific-output-structures)
- Implementation: [internal/generator/rpm/generator.go](../../internal/generator/rpm/generator.go)
  (`generateForVersionArch`, `getPackageVersion`)
- Builds on: [core ADR-0009 — single artifact origin](https://github.com/frostyard/core/blob/main/docs/adr/0009-single-artifact-origin-repository-frostyard-org.md)
- Related: [ADR-0010 — dnf `.repo` file policy](0010-dnf-repo-file-policy.md)
  emits the `baseurl` this layout satisfies
