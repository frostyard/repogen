# Org-wide decisions (frostyard/core ADRs)

Conventions this repository follows that are decided at the org level are
recorded as ADRs in
[frostyard/core](https://github.com/frostyard/core/tree/main/docs/adr).
The ones that bind repogen:

- [ADR-0007 — The Frostyard sysext filename pattern and derived versions](https://github.com/frostyard/core/blob/main/docs/adr/0007-frostyard-sysext-filename-pattern.md) — the 4-field NAME_VERSION_OSVERSION_ARCH parser is the org grammar's validator
- [ADR-0008 — Sysext distribution layout and update contract](https://github.com/frostyard/core/blob/main/docs/adr/0008-sysext-distribution-and-update-contract.md) — ext/<name>/SHA256SUMS(.gpg), ext/index, generated .transfer policy — repogen defines this format
- [ADR-0009 — repository.frostyard.org is the single artifact origin](https://github.com/frostyard/core/blob/main/docs/adr/0009-single-artifact-origin-repository-frostyard-org.md) — the layouts repogen writes are served from the frozen namespaces
- [ADR-0010 — Publish packages through the shared repogen action](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md) — historical shared-action decision, superseded by ADR-0048; current code still reflects its incremental never-delete model
- [ADR-0014 — One GPG repository key, baked into images](https://github.com/frostyard/core/blob/main/docs/adr/0014-single-gpg-trust-root.md) — REPOGEN_GPG_KEY signs all metadata and SHA256SUMS.gpg
- [ADR-0018 — Org-wide agent instruction and knowledge surfaces](https://github.com/frostyard/core/blob/main/docs/adr/0018-org-wide-agent-instruction-and-knowledge-surfaces.md) — agent instruction surfaces; its yeti/ AI-docs tier is superseded by ADR-0025's docs/ shape
- [ADR-0021 — SHA-pinned actions and least-privilege CI workflows](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md) — consumers must SHA-pin the publish action; applies to this repo's workflows too
- [ADR-0022 — make ci is the canonical gate; TestI* is reserved](https://github.com/frostyard/core/blob/main/docs/adr/0022-make-ci-gate-and-test-naming-filter.md) — the Makefile-as-interface gate convention
- [ADR-0023 — External downloads are version-pinned and checksum-verified](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md) — the production action must bind an exact Repogen asset and verify its SHA-256 before execution
- [ADR-0025 — One docs/ tree per repository, in core's four-category shape](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md) — this repo's docs/ tree (adr/, design/, specs/, plans/; formerly yeti/) — see [README.md](README.md)
- [ADR-0048 — Publish Debian packages to explicit codenames](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md) — freeze stable, reject it as a write target, use explicit immutable codenames, and make Repogen the sole fail-closed production metadata writer

When changing behavior covered by one of these, update or supersede the ADR
in frostyard/core first, then change this repo in the same effort.
