# Org-wide decisions (frostyard/core ADRs)

Conventions this repository follows that are decided at the org level are
recorded as ADRs in
[frostyard/core](https://github.com/frostyard/core/tree/main/docs/adr).
The ones that bind repogen:

- [ADR-0007 — The Frostyard sysext filename pattern and derived versions](https://github.com/frostyard/core/blob/main/docs/adr/0007-frostyard-sysext-filename-pattern.md) — the 4-field NAME_VERSION_OSVERSION_ARCH parser is the org grammar's validator
- [ADR-0008 — Sysext distribution layout and update contract](https://github.com/frostyard/core/blob/main/docs/adr/0008-sysext-distribution-and-update-contract.md) — ext/<name>/SHA256SUMS(.gpg), ext/index, generated .transfer policy — repogen defines this format
- [ADR-0009 — repository.frostyard.org is the single artifact origin](https://github.com/frostyard/core/blob/main/docs/adr/0009-single-artifact-origin-repository-frostyard-org.md) — the layouts repogen writes are served from the frozen namespaces
- [ADR-0010 — Publish packages through the shared repogen action](https://github.com/frostyard/core/blob/main/docs/adr/0010-publish-packages-via-repogen-to-r2.md) — historical shared-action decision, superseded by ADR-0048; current code still reflects its incremental never-delete model
- [ADR-0012 — svu versioning and rolling dev prerelease](https://github.com/frostyard/core/blob/main/docs/adr/0012-svu-versioning-and-rolling-dev-prerelease.md) — the version comes from svu on Conventional Commits: this repo's `.svu.yml` plus `make bump` (this repo's own ADR-0012 is a different decision: ACMM conformance)
- [ADR-0014 — One GPG repository key, baked into images](https://github.com/frostyard/core/blob/main/docs/adr/0014-single-gpg-trust-root.md) — REPOGEN_GPG_KEY signs all metadata and SHA256SUMS.gpg
- [ADR-0018 — Org-wide agent instruction and knowledge surfaces](https://github.com/frostyard/core/blob/main/docs/adr/0018-org-wide-agent-instruction-and-knowledge-surfaces.md) — agent instruction surfaces; its yeti/ AI-docs tier is superseded by ADR-0025's docs/ shape
- [ADR-0019 — Governance as code and risk tiers](https://github.com/frostyard/core/blob/main/docs/adr/0019-governance-as-code-and-risk-tiers.md) — the `## Risk tier` section of `.github/pull_request_template.md`
- [ADR-0021 — SHA-pinned actions and least-privilege CI workflows](https://github.com/frostyard/core/blob/main/docs/adr/0021-sha-pinned-actions-and-least-privilege-ci.md) — consumers must SHA-pin the publish action; applies to this repo's workflows too
- [ADR-0022 — make ci is the canonical gate; TestI* is reserved](https://github.com/frostyard/core/blob/main/docs/adr/0022-make-ci-gate-and-test-naming-filter.md) — the Makefile-as-interface gate convention
- [ADR-0023 — External downloads are version-pinned and checksum-verified](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md) — the `publish-to-r2` action must bind an exact Repogen asset and verify its SHA-256 before execution
- [ADR-0025 — One docs/ tree per repository, in core's four-category shape](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md) — this repo's docs/ tree (adr/, design/, specs/, plans/; formerly yeti/) — see [README.md](README.md)
- [ADR-0040 — Publish the repository settings contract](https://github.com/frostyard/core/blob/main/docs/adr/0040-publish-the-repository-settings-contract.md) — the settings the repository states plainly: `SECURITY.md` and GitHub private vulnerability reporting
- [ADR-0043 — Pin repository tools in mise and name the verify gate](https://github.com/frostyard/core/blob/main/docs/adr/0043-pin-repository-tools-in-mise-and-name-the-verify-gate.md) — `mise.toml` and `mise.lock` hold every tool pin; `make verify` is the non-mutating gate
- [ADR-0044 — Expose the make gate triad in every repository](https://github.com/frostyard/core/blob/main/docs/adr/0044-expose-the-make-gate-triad-in-every-repository.md) — `make verify`, `make check` and `make ci`
- [ADR-0048 — Publish Debian packages to explicit codenames](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md) — historical: superseded on 2026-10-02 by ADR-0055. It made Repogen the sole Debian writer; the explicit-codename and frozen-`stable` rules continue in ADR-0055
- [ADR-0053 — Name the svu config .svu.yml](https://github.com/frostyard/core/blob/main/docs/adr/0053-name-the-svu-config-svu-yml.md) — the file is `.svu.yml`
- [ADR-0055 — Publish Debian packages through the apt-publisher single writer](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md) — `frostyard/apt-publisher` is the only Debian writer; Repogen no longer publishes Debian packages and keeps sysexts; `publish-to-r2` refuses `deb`
- [ADR-0068 — Label pull requests that touch high-risk paths](https://github.com/frostyard/core/blob/main/docs/adr/0068-label-pull-requests-that-touch-high-risk-paths.md) — `.github/risk-paths.json` holds the globs; `.github/workflows/risk-paths.yml` calls core's `pr-triage.yml`, which adds `level:L1` to `level:L4` and `risk:high` at L1 and L2
- [ADR-0070 — Build tool docs sites with MaterialX on GitHub Pages](https://github.com/frostyard/core/blob/main/docs/adr/0070-build-tool-docs-sites-with-materialx-on-github-pages.md) — a docs site is a follow-up for this repo
- [ADR-0071 — Hold every code repository to one baseline](https://github.com/frostyard/core/blob/main/docs/adr/0071-hold-every-code-repository-to-one-baseline.md) — the repository baseline this repo meets; `internal/baseline` checks it
- [ADR-0072 — Write every README to one standard](https://github.com/frostyard/core/blob/main/docs/adr/0072-write-every-readme-to-one-standard.md) — the root README is a `tool` README of at most 1000 words
- [ADR-0073 — Let CI run the make ci targets as separate jobs](https://github.com/frostyard/core/blob/main/docs/adr/0073-let-ci-run-the-make-ci-targets-as-separate-jobs.md) — CI may split `make ci` into jobs; this repo runs 1 job
- [ADR-0075 — Group Dependabot updates and sync skills once a day](https://github.com/frostyard/core/blob/main/docs/adr/0075-batch-routine-pull-requests.md) — each entry of [`.github/dependabot.yml`](../.github/dependabot.yml) has 1 `patterns: ["*"]` group, so Dependabot opens at most 1 version-update PR for each ecosystem and run; security updates stay 1 PR each

When changing behavior covered by one of these, update or supersede the ADR
in frostyard/core first, then change this repo in the same effort.
