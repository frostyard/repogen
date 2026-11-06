<!-- The org squash-merges: branch off main, never stack on another PR's
branch. Reviews apply docs/specs/pr-review-rubric.md. -->

## Summary

<!-- What changes and why, in a few sentences. Link the issue(s) this
closes. -->

## Checks

<!-- The build gate from AGENTS.md — run after every code change. -->

- [ ] `make check` — formats, then runs the verify gate: tidy, vet, gofmt, lint at the mise pin, docs integrity, unit tests
- [ ] `make ci` — adds govulncheck, race tests, the Docker-built packages and the integration suite, the release builds — or a green CI run
- [ ] Generator/scanner/signer changes: `make test-integration` green

## Risk tier

<!-- Select the highest tier that applies (core ADR-0019) and give the
rationale. A pull request with the label `risk:high` (it touches a path in
.github/risk-paths.json) is Tier 4: name each matched path and why the
change is safe. -->

- [ ] Tier 1: Low
- [ ] Tier 2: Moderate
- [ ] Tier 3: High
- [ ] Tier 4: Critical

**Rationale:**

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

- [ ] `node scripts/check-docs.mjs` green (also inside `make check`)
- [ ] Output-format changes: `docs/specs/generators.md` updated alongside
      the code
