# 0010 — dnf `.repo` file naming, ID, and flag policy

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

When `--base-url` is set, the RPM generator publishes a ready-to-install
dnf/yum `.repo` file at the repository root, so consumers configure the repo
with one `curl -o /etc/yum.repos.d/…`. That file's name, section ID, baseurl,
and flags are consumer-visible contract: renaming the file orphans installed
copies, changing the section ID resets dnf's per-repo state, and the flags
differ in convention between Fedora, CentOS, and RHEL.

## Decision

`generateRepoFile` and its helpers in
[internal/generator/rpm/generator.go](../../internal/generator/rpm/generator.go)
fix the policy:

- **Filename** (`getRepoFileName`): `--repo-name` (sanitized) >
  `--distro` variant > sanitized `--origin`, emitted as `{name}.repo` at the
  output root.
- **Repo ID** (section header): always the sanitized `--origin`
  (`sanitizeRepoID` lowercases; keeps `[a-z0-9]`; maps space/`_`/`.` to `-`;
  drops everything else). Display `name=` is `--label`, falling back to
  `--origin`.
- **baseurl**: `--base-url` with `$releasever/$basearch` **always** appended
  (after ensuring a trailing slash) — the file only works against the
  `{version}/{arch}/` layout of ADR-0004.
- **Flags by signing and distro variant**: `enabled=1` always;
  `gpgcheck=1` plus a `gpgkey=` line (from `--gpg-key-url`, never
  auto-generated) when a signer is configured, else `gpgcheck=0`;
  `repo_gpgcheck=1` only for the `fedora` variant when signed;
  `metadata_expire=86400` for `rhel` and `centos`.

## Consequences

- One published `.repo` file serves every release/arch of a distro variant;
  installation is a single file download.
- Filename and repo ID are frozen once consumers install the file — changing
  the priority ladder, the sanitization rules, or `--origin` for a published
  repo is a breaking change requiring a superseding ADR.
- `gpgkey=` comes exclusively from `--gpg-key-url`; a signed repo published
  without that flag emits `gpgcheck=1` with an empty key URL, which dnf will
  reject. Callers of the publish pipeline must supply both together.
- The distro-conditional flags encode today's conventions (Fedora does
  repo-level GPG checking; RHEL/CentOS get a 24h metadata TTL); new variants
  must extend the switch deliberately.

## Alternatives considered

- **One `.repo` file per version/arch:** redundant given `$releasever` /
  `$basearch` substitution, and multiplies the frozen-filename problem.
- **Auto-deriving `gpgkey=` from `--base-url`:** implicit coupling between
  key location and repo layout; an explicit URL keeps the key relocatable
  (org key placement is core ADR-0014's concern).
- **Uniform flags across variants:** simpler, but would either impose
  `repo_gpgcheck` on distros whose stock dnf configs don't expect it or drop
  it where Fedora convention enables it.

## References

- Shapes: [specs/generators.md — Yum/RPM](../specs/generators.md#yumrpm-internalgeneratorrpm),
  [design/overview.md — Configuration](../design/overview.md#configuration)
- Implementation: [internal/generator/rpm/generator.go](../../internal/generator/rpm/generator.go)
  (`generateRepoFile`, `getRepoFileName`, `sanitizeRepoID`)
- Builds on: [ADR-0004 — RPM `{version}/{arch}/` layout](0004-rpm-version-arch-layout-and-version-ladder.md),
  [core ADR-0014 — one GPG repository key](https://github.com/frostyard/core/blob/main/docs/adr/0014-single-gpg-trust-root.md)
