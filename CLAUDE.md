# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Documentation

**update documentation** After any change to source code, update relevant documentation in CLAUDE.md, README.md and the yeti/ folder. A task is not complete without reviewing and updating relevant documentation.

**Sysext signing:** when a GPG signer is configured, the sysext generator emits a detached binary `SHA256SUMS.gpg` beside each manifest and sets generated transfers to `Verify=true`. Unsigned generation remains supported and emits `Verify=false`.

**yeti/ directory** The `yeti/` directory contains documentation written for AI consumption and context enhancement, not primarily for humans. Jobs like `doc-maintainer` and `issue-worker` instruct the AI to read `yeti/OVERVIEW.md` and related files for codebase context before performing tasks. Write content in this directory to be maximally useful to an AI agent understanding the codebase — detailed architecture, patterns, and decision rationale rather than user-facing guides.

## Org-wide decisions

Org-level conventions this repo follows are recorded as ADRs in
frostyard/core — see [docs/org-adrs.md](docs/org-adrs.md) for the list that
binds this repo. Change the ADR (in core) before changing behavior it covers.
