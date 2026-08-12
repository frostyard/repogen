# Session summary

Ephemeral session state — agents replace the block below at session end
(session state lives in `.claude/`). Durable learnings go to
[.memory/](../.memory/README.md), never here; ongoing work is tracked in
GitHub issues (`gh --repo frostyard/repogen`).

## Current state

- ACMM conformance landed (2026-08-12): closes repogen#37–#55 via
  [ADR-0012](../docs/adr/0012-acmm-conformance-via-canonical-aliases.md)'s
  alias lattice plus new templates, prompts, and the docs-integrity gate.

## Last landed

- Repo-local architecture decisions recorded as ADRs 0001–0011 (#30).

## Next

- repogen#34 (release-pipeline consolidation onto GoReleaser per core
  ADR-0012) — on `hold`, separate effort.
