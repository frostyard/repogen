# 0015 — Remove the superseded production path

- **Status:** Proposed
- **Date:** 2026-10-24

## Context

[ADR-0013](0013-separate-generic-generation-from-production-publishing.md)
proposed a Frostyard production path for Debian publication inside Repogen.
Core [ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md)
superseded it: `frostyard/apt-publisher` is the only Frostyard Debian writer.
[Plan 0001](../plans/0001-frostyard-production-publisher.md) is superseded too.

The code of that path stayed in the tree and stayed tested, but nothing calls
it. No workflow in apt-publisher, snosi or omarchy-apps runs
`validate-production`, `reconcile-production` or the
`reconcile-production` action. snosi uses only `publish-to-r2`. ADR-0013 said
that removing the code is a separate decision. This is that decision.

## Decision

Delete the production path. These parts go:

- the `validate-production` and `reconcile-production` commands
  (`internal/cli/production.go`, `internal/cli/reconcile_production.go`);
- `internal/production/`, `internal/intake/` and `internal/objectstore/`
  (with the AWS SDK modules);
- the Debian production restore, transaction, contract and recovery writer,
  and the atomic local production generation (`production_*.go`,
  `recovery_writer.go`, `local_generation*.go` in `internal/generator/deb/`);
- the immutable shared-pool object-store primitive `SharedPool`
  (`internal/generator/deb/pool.go`), which only the production
  transaction used;
- the `.github/actions/reconcile-production` action, its input validator
  `scripts/validate-reconcile-inputs.sh`, and the `production/` templates;
- the production contract spec and the tests of the removed action.

One part stays because `generate` needs it: the check of prior `Release`,
`InRelease` and `Release.gpg` signatures. It moves to
`internal/generator/deb/release_signature.go` as `parseTrustedKeyRing` and
`verifyReleaseSignatures`, with the same logic and its own tests.

`generate`, the `publish-to-r2` action, and the sysext reconciliation do not
change.

## Consequences

- Breaking change: the `validate-production` and `reconcile-production`
  commands shipped in v0.5.2 and go away. No known consumer uses them.
- Less code, fewer dependencies (the AWS SDK leaves `go.mod`), and a smaller
  docs and test surface.
- The record stays in git history, in ADR-0013 and in Plan 0001.
- The R10 release rules that needed the removed code go away; the release
  asset, checksum and installer rules stay.

## Alternatives considered

- **Keep the code, tested but unused:** rejected. It costs maintenance and
  dependencies and has no consumer.
- **Move it to apt-publisher:** rejected. apt-publisher publishes with aptly
  (its ADR-0001), so it has no use for this transaction code.

## References

- Builds on: [ADR-0013](0013-separate-generic-generation-from-production-publishing.md),
  [Plan 0001](../plans/0001-frostyard-production-publisher.md),
  core [ADR-0055](https://github.com/frostyard/core/blob/main/docs/adr/0055-publish-debian-packages-through-the-apt-publisher.md)
- Shapes: [design/overview.md](../design/overview.md),
  [specs/generators.md](../specs/generators.md)
