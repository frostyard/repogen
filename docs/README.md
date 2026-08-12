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

*(none yet — repo-local decisions get the next number here;
org-wide ones go to frostyard/core and are listed in
[org-adrs.md](org-adrs.md))*

### Design

- [Repogen Overview](design/overview.md) — purpose, architecture, data flow,
  key patterns, configuration; the entry point for understanding the codebase
- [CI/CD and GitHub Action](design/ci-cd.md) — test/release workflows, the
  `publish-to-r2` composite action, Makefile targets, test fixtures

### Specs

- [Generator Details](specs/generators.md) — per-format output directory
  layout, metadata file formats, parser behavior, and signing for all six
  generators

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
