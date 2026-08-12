<!-- The org squash-merges: branch off main, never stack on another PR's
branch. Reviews apply docs/specs/pr-review-rubric.md. -->

## Summary

<!-- What changes and why, in a few sentences. Link the issue(s) this
closes. -->

## Checks

<!-- The build gate from AGENTS.md — run after every code change. -->

- [ ] `make build` — code compiles
- [ ] `make fmt` — code is formatted
- [ ] `make lint` — linter passes (`.golangci.yml`)
- [ ] `make test-unit` — unit tests pass
- [ ] Generator/scanner/signer changes: `make test-integration` green

## Docs housekeeping

<!-- Delete rows that don't apply (no docs touched). -->

- [ ] New docs started from their category's `TEMPLATE.md`
- [ ] Every new doc indexed in `docs/README.md`
- [ ] Cross-links added in both directions (ADR ↔ design ↔ spec ↔ plan)
- [ ] New significant decision recorded as an ADR *first*, in this PR
- [ ] Conformance aliases (ADR-0012) untouched — canonical targets edited
      instead

## Verification

<!-- Paste evidence the gates ran locally. -->

- [ ] `node scripts/check-docs.mjs` green
- [ ] Output-format changes: `docs/specs/generators.md` updated alongside
      the code
