# 0012 — ACMM conformance via canonical aliases

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

The Hive ACMM evaluation
([repogen#37–#55](https://github.com/frostyard/repogen/issues/37)) grades
repositories by checking that fixed paths exist — test suites, templates,
style configs, rubrics, metrics, agent-safety settings. Each criterion lists
acceptable paths and states "the content can follow your project's
conventions." frostyard/core solved the identical issue set with
[core ADR-0029](https://github.com/frostyard/core/blob/main/docs/adr/0029-acmm-conformance-via-canonical-aliases.md):
committed relative symlinks to canonical content wherever a canonical
equivalent exists, genuinely new artifacts only where none does. This repo
already held canonical equivalents for part of the list (`CLAUDE.md` and
`.github/copilot-instructions.md` as divergent instruction surfaces,
`test/integration_test.go` as a real e2e suite, the four-category `docs/`
tree per core ADR-0025). Duplicating content into ACMM's paths would
guarantee drift — exactly what core ADR-0002 rejected.

## Decision

The two divergent instruction files merge into one canonical **`AGENTS.md`**
(core ADR-0002/0018 pattern, both already binding via
[org-adrs.md](../org-adrs.md)). ACMM's required paths are satisfied by
**committed relative symlinks to canonical content** wherever a canonical
equivalent exists, and by genuinely new artifacts only where none does.

The alias table (edit the targets, never the aliases):

| Alias | Target | Criterion |
| --- | --- | --- |
| `CLAUDE.md` | `AGENTS.md` | (agent surface, core ADR-0002) |
| `GEMINI.md` | `AGENTS.md` | (agent surface, core ADR-0002) |
| `CONTRIBUTING.md` | `AGENTS.md` | contributing guide (#40) |
| `.cursorrules` | `AGENTS.md` | cursor rules (#44) |
| `.github/copilot-instructions.md` | `../AGENTS.md` | (agent surface, core ADR-0002) |
| `.claude/skills` | `../.agents/skills` | simple skills (#47) |
| `docs/metrics.md` | `specs/pr-acceptance-metric.md` | PR acceptance metric (#49) |
| `docs/review-rubric.md` | `specs/pr-review-rubric.md` | PR review rubric (#50) |
| `docs/quality.md` | `design/quality-loop.md` | quality dashboard (#51) |

Rules:

- **Directory criteria always get real git trees** (`test/e2e/`,
  `.github/ISSUE_TEMPLATE/`, `.github/prompts/`, `.memory/`) — an evaluator
  reading the git tree via API sees a symlink as a blob, not a tree.
- **Aliases are not docs**: they get no `docs/README.md` index entries and
  carry no cross-link obligations; the canonical target does.
- Genuinely new artifacts, each doing real work: the merged `AGENTS.md`
  (#43); `test/e2e/README.md` documenting the existing integration suite as
  the e2e entry point (#37); `.github/pull_request_template.md` (#38) and
  `.github/ISSUE_TEMPLATE/` (#39); `.golangci.yml` pinning the linter set
  `make lint` runs (#41); `.coverage-thresholds.json` enforced by
  `scripts/check-docs.mjs` in the new `docs-gate` CI job — docs-index
  coverage, link integrity, symlink resolution (#42); `.github/prompts/`
  runbooks (#45); `.editorconfig` (#46); the `.memory/` inbox with core
  ADR-0018's append-only five-field schema (#48);
  [specs/pr-acceptance-metric.md](../specs/pr-acceptance-metric.md) (#49),
  [specs/pr-review-rubric.md](../specs/pr-review-rubric.md) (#50), and
  [design/quality-loop.md](../design/quality-loop.md) (#51);
  `.claude/settings.json` denying merge-own-PR, approve-own-work,
  release-publishing, and pushes to `main` at the tool layer
  (#52, #53, #55); `.claude/session-summary.md` (#54).

## Consequences

- One canonical body of content per criterion; conformance paths cannot
  drift from it.
- GitHub's web renderer shows a symlinked `.md` as its target path rather
  than its content; checkouts on Windows need `core.symlinks=true` or WSL.
- The alias table above is the registry; adding or removing an alias means
  amending it here (a new ADR if the mechanism itself changes).
- `scripts/check-docs.mjs` fails CI on any broken alias, making the lattice
  self-guarding.
- Contingency: if the ACMM evaluator rejects a symlink for one of the file
  criteria (#40, #44, #49, #50, #51), that alias is replaced by a real stub
  file pointing at the canonical doc — a one-commit change that does not
  reverse this decision.

## Alternatives considered

- **Real duplicate files at the ACMM paths:** guaranteed drift; rejected for
  the same reason core ADR-0002 rejected per-tool instruction copies —
  this repo had already accumulated exactly that drift between `CLAUDE.md`
  and `.github/copilot-instructions.md`.
- **Content-free stub files:** a second class of "doc" that the index and
  cross-link rules would nominally govern; symlinks are aliases, not docs.
- **Ignore the issues:** the repo stays flagged at ACMM L0 and cannot enter
  agentic-fleet management.

## References

- Shapes: [design/quality-loop.md](../design/quality-loop.md),
  [specs/pr-review-rubric.md](../specs/pr-review-rubric.md),
  [specs/pr-acceptance-metric.md](../specs/pr-acceptance-metric.md)
- Pattern source:
  [core ADR-0029](https://github.com/frostyard/core/blob/main/docs/adr/0029-acmm-conformance-via-canonical-aliases.md),
  building on core ADR-0002/0018/0019 (see [org-adrs.md](../org-adrs.md))
