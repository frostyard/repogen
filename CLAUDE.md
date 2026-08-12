# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Documentation

**update documentation** After any change to source code, update relevant documentation in CLAUDE.md, README.md and the `docs/` tree. A task is not complete without reviewing and updating relevant documentation.

**Sysext signing:** when a GPG signer is configured, the sysext generator emits a detached binary `SHA256SUMS.gpg` beside each manifest and sets generated transfers to `Verify=true`. Unsigned generation remains supported and emits `Verify=false`.

**docs/ tree** (formerly `yeti/`) All documentation lives in `docs/`, in the four-category shape defined by [frostyard/core ADR-0025](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md): `docs/adr/` (why), `docs/design/` (how it fits together), `docs/specs/` (exact contracts), `docs/plans/` (order of work) — see [docs/README.md](docs/README.md) for the table, index, and conventions. Read [docs/design/overview.md](docs/design/overview.md) for codebase context before performing tasks. Write these docs to be maximally useful to an AI agent understanding the codebase — detailed architecture, patterns, and decision rationale rather than user-facing guides. Repo-local decisions get an ADR in `docs/adr/` (next free number, from the TEMPLATE); org-wide decisions go to frostyard/core and are listed in [docs/org-adrs.md](docs/org-adrs.md).

## Repo gotchas

- This repository is a GitHub fork of `ralt/repogen`, so every `gh` command must pass `--repo frostyard/repogen` explicitly or it may target the upstream parent.

## Org-wide decisions

Org-level conventions this repo follows are recorded as ADRs in
frostyard/core — see [docs/org-adrs.md](docs/org-adrs.md) for the list that
binds this repo. Change the ADR (in core) before changing behavior it covers.
