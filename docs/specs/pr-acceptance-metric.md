# Spec: PR acceptance metric

One paragraph: defines the single quality metric this repo tracks for its
pull-request stream — the acceptance rate — precisely enough that any agent
or human computes the same number from the same window. Consumers: the
[quality loop](../design/quality-loop.md) and periodic org reviews.
`docs/metrics.md` is a conformance alias for this file
([ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md)).
`scripts/check-docs.mjs` pins this file's two `##` headings so prose and
tooling cannot drift apart silently.

## Definition

```
acceptance_rate = (merged PRs not subsequently reverted) / (PRs opened)
```

over a rolling window of the **last 30 PRs opened** (or 90 days, whichever
is smaller).

| Term | Meaning |
| --- | --- |
| PRs opened | All PRs opened against `main` in the window, including still-open and closed-unmerged ones |
| Merged | Squash-merged to `main` |
| Reverted | A later commit or PR whose title starts with `Revert "` naming the PR's title, or whose body contains `Reverts frostyard/repogen#N` |

Data source (the `--repo` flag is mandatory — this repo is a fork):

```
gh pr list --repo frostyard/repogen --state all --limit 30 \
  --json number,state,mergedAt,title
```

## Rules

- The window slides by PR count first (last 30 opened), clipped to 90 days —
  a quiet quarter MUST NOT let stale history dominate.
- A reverted PR counts against acceptance even if the revert is itself later
  reverted (churn is the signal).
- Draft PRs are excluded until marked ready.
- The metric is observational: it never gates a merge by itself; gates live
  in the [review rubric](pr-review-rubric.md) and CI.

## References

- Rationale: [ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md)
- Context: [design/quality-loop.md](../design/quality-loop.md)
