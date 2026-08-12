# Spec: PR review rubric

One paragraph: the checklist every frostyard/repogen pull-request review
applies. Consumers: human reviewers, the
[review runbook](../../.github/prompts/review.prompt.md), and the
[PR template](../../.github/pull_request_template.md), whose sections mirror
these checks. `docs/review-rubric.md` is a conformance alias for this file
([ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md)).

## Interface

Every review verifies each row; a PR merges only when all applicable rows
pass.

| Check | How to verify |
| --- | --- |
| Build gate green | `make build`, `make fmt`, `make lint` (`.golangci.yml`) all pass — the AGENTS.md after-every-code-change loop. |
| Tests | `make test-unit` passes; new functionality has tests (table-driven, success and error cases). Changes under `internal/generator/**`, `internal/scanner/**`, or `internal/signer/**` also pass `make test-integration`. |
| Format invariants hold | Generator-output changes respect the repo-local ADRs — checksums recomputed from copied bytes ([ADR-0007](../adr/0007-recompute-checksums-from-copied-bytes.md)), signing routed per ([ADR-0009](../adr/0009-gpg-cli-and-go-crypto-signing-split.md)) — and change [specs/generators.md](generators.md) in the same PR. |
| Docs housekeeping | New docs start from their category `TEMPLATE.md`, are indexed in [docs/README.md](../README.md), and cross-link in both directions. New significant decision ⇒ ADR first, in the same change. |
| Docs-integrity gate green | `node scripts/check-docs.mjs` passes: every doc indexed, every relative link resolving, every symlink alias intact (thresholds in `.coverage-thresholds.json`). |
| Aliases untouched | Conformance aliases ([ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md)) are not edited directly; canonical targets are. |
| Agent limits respected | The PR was not merged, approved, or released by the agent that authored it; mechanically backed by `.claude/settings.json`. |
| Fork targeting | Any `gh` invocation in scripts or docs passes `--repo frostyard/repogen` (AGENTS.md "Repo gotchas"). |

## Rules

- Each check is independently verifiable from the PR diff plus the commands
  named in its row — a review MUST NOT rely on out-of-band context.
- Rubric changes ride with the artifact that enforces them (the gate script,
  the workflow, or the template) in the same PR.
- The org squash-merges: the review covers the squashed result, not
  intermediate commits.

## References

- Rationale: [ADR-0012](../adr/0012-acmm-conformance-via-canonical-aliases.md)
- Context: [design/quality-loop.md](../design/quality-loop.md)
