# Quality loop

Living document. Rationale:
[ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md).
Contracts: [specs/pr-review-rubric.md](../specs/pr-review-rubric.md),
[specs/pr-acceptance-metric.md](../specs/pr-acceptance-metric.md).
`docs/quality.md` is a conformance alias for this file (ADR-0012).

## Overview

How change quality is proposed, gated, observed, and learned from in this
repo. One loop, five stations:

```
PR template ──► review rubric ──► CI gates ──► corrections ──► promotion
(checklist)     (spec)            (ci.yml)     (.memory/)      (AGENTS.md,
     ▲                                                          docs, skills)
     └────────────── acceptance metric (spec) observes the stream ─────────┘
```

## Design

- **Declare** — [.github/pull_request_template.md](../../.github/pull_request_template.md)
  makes every PR walk the build-gate and docs-housekeeping checklists.
- **Review** — the [PR review rubric](../specs/pr-review-rubric.md) is the
  contract a review applies; the
  [review runbook](../../.github/prompts/review.prompt.md) is its
  task-shaped form for agents.
- **Gate** — [.github/workflows/ci.yml](../../.github/workflows/ci.yml)
  runs one job on every PR (see [design/ci-cd.md](ci-cd.md)): `make ci`, which
  is verify, govulncheck, the unit tests with race detection and coverage,
  the packages built in Docker and the e2e suite — real packages built for
  every supported type and the published repository output asserted on
  (entry point: [test/e2e/README.md](../../test/e2e/README.md)) — then the
  release builds. Docs integrity (`node scripts/check-docs.mjs`) is part of
  `make verify`: docs-index coverage, relative-link integrity, and symlink
  resolution against
  [.coverage-thresholds.json](../../.coverage-thresholds.json) — all 1.0,
  `never_relax: true` (the loop may tighten, never loosen).
- **Learn** — corrections land in
  [.memory/corrections/](../../.memory/README.md) (1 file for each record,
  five-field schema, never removed or renamed) and are promoted into `AGENTS.md`, docs, or skills;
  promotion is the only sanctioned duplication.
- **Enforce mechanically** — [.claude/settings.json](../../.claude/settings.json)
  denies the forbidden acts at the tool layer: merging PRs (`gh pr merge`),
  approving own work (`gh pr review --approve`), publishing releases
  (`gh release`), and pushing to `main`; session state lives in
  [.claude/session-summary.md](../../.claude/session-summary.md).
- **Observe** — the [PR acceptance metric](../specs/pr-acceptance-metric.md)
  summarizes the stream; it informs, never gates.

## Operational notes

Re-run every gate locally before pushing:

```
make check               # format, then the verify gate
make test-integration    # for generator/scanner/signer changes
```

`make check` is credential-free and writes no file beyond the formatting.
`make ci` runs the whole CI gate when Docker is available.

Failure modes: a broken alias or missing index line fails docs-gate (fix the
canonical target or the index, never the alias); integration failures mean a
generator's published output regressed — fix before merge, since downstream
repos consume that output via the `publish-to-r2` action.

## References

- Rationale: [ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md)
- Contracts: [specs/pr-review-rubric.md](../specs/pr-review-rubric.md),
  [specs/pr-acceptance-metric.md](../specs/pr-acceptance-metric.md)
- CI context: [design/ci-cd.md](ci-cd.md)
- Built in: the 2026-08-12 ACMM conformance PR (closes repogen#37–#55)
