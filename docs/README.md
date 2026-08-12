# Documentation

Docs are split by the question they answer
(shape defined by
[frostyard/core ADR-0025](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md);
this tree replaced the former `yeti/` AI-docs directory):

| Directory | Question | Contents |
| --- | --- | --- |
| [adr/](adr/) | **Why** did we choose this? | Repo-local Architecture Decision Records — immutable once accepted; superseded, never edited. Org-wide decisions live in frostyard/core instead |
| [design/](design/) | **How** does it fit together? | Living documents describing the current architecture |
| [specs/](specs/) | **What exactly** is the contract? | Precise, testable interface/format definitions, changed only alongside code |
| [plans/](plans/) | **When/in what order** do we build? | Roadmaps and phase plans; updated as work lands |

## Index

### Decisions (ADRs)

Repo-local decisions; org-wide ones go to frostyard/core and are listed in
[org-adrs.md](org-adrs.md).

- [ADR-0001 — Unsigned Debian repositories still emit InRelease](adr/0001-unsigned-debian-repos-emit-inrelease.md)
  — deliberately non-conformant unsigned `InRelease` so modern apt works
  with `[trusted=yes]`
- [ADR-0002 — Debian pool is shared, first-letter sharded, not codename-scoped](adr/0002-shared-debian-pool-layout.md)
  — one `pool/main/{letter}/{name}/` for all codenames; non-`a-z` names
  bucket under `0`; paths frozen once published
- [ADR-0003 — Debian repositories have a single component: `main`](adr/0003-single-main-component.md)
  — `main` hardcoded in dists/pool paths; `--components` only relabels the
  Release file and errors on anything else
- [ADR-0004 — RPM layout is `{version}/{arch}/` with a version-resolution ladder](adr/0004-rpm-version-arch-layout-and-version-ladder.md)
  — `--version` > RPM `DistroVersion` metadata > per-variant default; the
  shard makes `$releasever/$basearch` resolve
- [ADR-0005 — Content-addressed repodata with open-checksum discipline](adr/0005-content-addressed-repodata.md)
  — `{sha256}-primary.xml.gz` named by compressed-bytes hash; repomd records
  checksum and open-checksum separately; repodata grows unbounded
- [ADR-0006 — Pacman databases are written twice: `.db.tar.zst` and `.db`](adr/0006-pacman-dual-database-files.md)
  — byte-identical copies (and dual `.sig`) because static storage has no
  symlinks; the pair must never diverge
- [ADR-0007 — Checksums come from copied bytes, never from parsed metadata](adr/0007-recompute-checksums-from-copied-bytes.md)
  — recompute after every copy; three-stage skip check
  (path → size → sha256); missing-locally is OK under incremental
- [ADR-0008 — Package detection sniffs magic bytes first, falls back to filenames](adr/0008-magic-bytes-detection-with-extension-fallback.md)
  — 512-byte sniff with per-format OR/AND/filename semantics; detection
  routes, parsers validate
- [ADR-0009 — GPG signing splits between the gpg CLI and go-crypto](adr/0009-gpg-cli-and-go-crypto-signing-split.md)
  — cleartext and binary detached via gpg CLI in an ephemeral
  `repogen-gpg-*` home, ASCII detached via go-crypto; routing is
  correctness-critical
- [ADR-0010 — dnf `.repo` file naming, ID, and flag policy](adr/0010-dnf-repo-file-policy.md)
  — filename `--repo-name` > `--distro` > sanitized origin;
  `$releasever/$basearch` always appended; distro-conditional
  gpgcheck/metadata_expire flags
- [ADR-0011 — Incremental mode reconstructs state from published metadata](adr/0011-incremental-state-from-published-metadata.md)
  — the published indices are the only incremental state; format-aware
  conflict identity; warn-and-fallback on unparsable metadata
- [ADR-0012 — ACMM conformance via canonical aliases](adr/0012-acmm-conformance-via-canonical-aliases.md)
  — one canonical `AGENTS.md` plus committed relative symlinks satisfy the
  Hive ACMM path checks; directory criteria get real trees; the alias table
  is the registry

### Design

- [Repogen Overview](design/overview.md) — purpose, architecture, data flow,
  key patterns, configuration; the entry point for understanding the codebase
- [CI/CD and GitHub Action](design/ci-cd.md) — test/release workflows, the
  `publish-to-r2` composite action, Makefile targets, test fixtures
- [Quality loop](design/quality-loop.md) — how change quality is declared,
  reviewed, gated, observed, and learned from; `docs/quality.md` is its
  conformance alias

### Specs

- [Generator Details](specs/generators.md) — per-format output directory
  layout, metadata file formats, parser behavior, and signing for all six
  generators
- [PR acceptance metric](specs/pr-acceptance-metric.md) — the acceptance-rate
  definition and window rules; `docs/metrics.md` is its conformance alias
- [PR review rubric](specs/pr-review-rubric.md) — the checklist every PR
  review applies; `docs/review-rubric.md` is its conformance alias

### Plans

*(none yet)*

### Uncategorized

- [Org-wide decisions](org-adrs.md) — the frostyard/core ADRs that bind this
  repository

## Conventions

- **New docs start from their category's `TEMPLATE.md`** (in each directory).
- New decision → new ADR with the next number; if it reverses an old one, mark
  the old one `Superseded by NNNN` rather than editing it. Decisions that bind
  more than this repo become ADRs in frostyard/core plus a line in
  [org-adrs.md](org-adrs.md).
- Design docs are updated in place to always reflect reality.
- Specs change only alongside the code that implements them.
- Cross-link between categories in both directions (ADR ↔ design ↔ spec ↔
  plan).
- Adding a doc means adding it to the index above.
