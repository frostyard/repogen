# Review a pull request

Review the given frostyard/repogen PR against the repo rubric. You are
reviewing, not merging: never approve-and-merge in one act, and never merge
a PR you authored (mechanically backed by `.claude/settings.json`,
[ADR-0012](../../docs/adr/0012-acmm-conformance-via-canonical-aliases.md)).
Every `gh` command must pass `--repo frostyard/repogen` (this repo is a
fork; see AGENTS.md "Repo gotchas").

1. Read [AGENTS.md](../../AGENTS.md) — the code conventions and
   documentation rules the diff must satisfy.
2. Apply every row of the
   [PR review rubric](../../docs/specs/pr-review-rubric.md)
   (`docs/review-rubric.md` resolves to the same file). Check each row
   independently; cite file and line for every failure.
3. Run the gates the rubric names:
   - `make build && make fmt && make lint && make test-unit`
   - `node scripts/check-docs.mjs`
   - if `internal/generator/**`, `internal/scanner/**`, or
     `internal/signer/**` changed: `make test-integration`
4. If the diff changes generator output formats, verify
   [docs/specs/generators.md](../../docs/specs/generators.md) changed
   alongside the code, and that the repo-local ADRs still hold (checksums
   recomputed from copied bytes, ADR-0007; signing routed per ADR-0009).
5. Report findings as review comments ordered by severity; state plainly
   when a row passes. A PR with any failing rubric row gets "request
   changes", not silence.
