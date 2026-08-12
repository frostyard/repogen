# Org-wide decisions (frostyard/core ADRs)

Conventions this repository follows that are decided at the org level are
recorded as ADRs in
[frostyard/core](https://github.com/frostyard/core/tree/main/docs/adr).
The ones that bind repogen:

- [ADR-0007 — The Frostyard sysext filename pattern and derived versions](https://github.com/frostyard/core/blob/main/docs/adr/0007-frostyard-sysext-filename-pattern.md) — the 4-field NAME_VERSION_OSVERSION_ARCH parser is the org grammar's validator
- [ADR-0008 — Sysext distribution layout and update contract](https://github.com/frostyard/core/blob/main/docs/adr/0008-sysext-distribution-and-update-contract.md) — ext/<name>/SHA256SUMS(.gpg), ext/index, generated .transfer policy — repogen defines this format
- [ADR-0009 — repository.frostyard.org is the single artifact origin](https://github.com/frostyard/core/blob/main/docs/adr/0009-single-artifact-origin-repository-frostyard-org.md) — the layouts repogen writes are served from the frozen namespaces
- [ADR-0010 — Publish packages through the shared repogen action](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md) — .github/actions/publish-to-r2 is the org publish pipeline; incremental never-delete semantics
- [ADR-0014 — One GPG repository key, baked into images](https://github.com/frostyard/core/blob/main/docs/adr/0014-single-gpg-trust-root.md) — REPOGEN_GPG_KEY signs all metadata and SHA256SUMS.gpg
- [ADR-0018 — Org-wide agent instruction and knowledge surfaces](https://github.com/frostyard/core/blob/main/docs/adr/0018-org-wide-agent-instruction-and-knowledge-surfaces.md) — yeti/ AI-docs tier
- [ADR-0021 — SHA-pinned actions and least-privilege CI workflows](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md) — consumers must SHA-pin the publish action; applies to this repo's workflows too
- [ADR-0022 — make ci is the canonical gate; TestI* is reserved](https://github.com/frostyard/core/blob/main/docs/adr/0022-make-ci-gate-and-test-naming-filter.md) — the Makefile-as-interface gate convention

When changing behavior covered by one of these, update or supersede the ADR
in frostyard/core first, then change this repo in the same effort.
