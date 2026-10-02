# 0015 — Producers submit to the durable intake from CI with a submit-only command

- **Status:** Accepted
- **Date:** 2026-10-02

## Context

`repogen reconcile-production` processes one retained receipt from the durable
intake ([specs/production-r2-reconciliation.md](../specs/production-r2-reconciliation.md)).
Plan 0001 T0 defines how a request reaches that intake: an authenticated
submit endpoint, or a documented and reviewed equivalent, writes the
artifacts, provenance, request, submission pointer and sequenced receipt, and
producers never hold intake credentials. No hosted submit endpoint exists,
and the authenticated endpoint of R8 is not built. Without a submit path the
reconciler has no receipt to process.

The only R2 credentials available are the existing ones that producers'
release jobs already hold. They are not scoped by the provider to the intake
buckets.

## Decision

`repogen submit-intake` and the composite Action
`.github/actions/submit-intake` are the reviewed T0 equivalent until R8's
endpoint exists. They run in the producer's protected CI job from an exact
repogen release, with the existing R2 credentials written only to a private
mode-`0600` file.

The command is submit-only in code:

- every write is `CreateIfAbsent` (`If-None-Match: *`) under exactly five key
  shapes: `blobs/sha256/`, `manifests/provenance/v1/sha256/`,
  `manifests/request/v1/sha256/`, `submissions/v1/sha256/` and
  `receipts/v1/debian/trixie/`;
- it has no replace, delete, copy or publication operation. The publication
  bucket is fixed in code, and a configuration whose intake or coordination
  bucket is that bucket, or whose intake and coordination buckets are the
  same, is refused;
- a digest-pinned configuration fixes the account, buckets, kind `debian`,
  target `trixie` and the one allowed producer; `stable` is refused everywhere;
- every input is validated, and every artifact snapshotted and hashed, before
  the first write; artifacts are uploaded only from the verified snapshots.

The producer identity is the configuration's `producer`. The command requires
it to equal the request's `producer`, and the Action requires it to equal
`GITHUB_REPOSITORY`.

## Consequences

- Producers can place a request in the intake today, so a canary can reach
  `reconcile-production` without a hosted service.
- This deviates from T0's rule that producers never hold intake credentials.
  The existing R2 credentials are used and they are not provider-scoped. By
  the maintainer's decision, the submit-only and no-publication properties are
  enforced only in repogen code.
- Residual risk: anyone who obtains the token, or runs other code with it, can
  delete or replace intake and publication objects outside repogen. Scoping a
  token at the provider to the intake and coordination buckets remains an
  option for later; it is not a prerequisite of this path.
- Producer identity is self-asserted by the configuration and the CI
  environment, not a federated identity checked by a separate service.
- Replays are idempotent: the same request bytes with the same submission key
  return the existing receipt. A reused key with different bytes, or an
  existing object with different bytes, fails without writing a receipt.

## Alternatives considered

- **Build the authenticated endpoint first (R8):** right long-term, but it
  needs hosting and identity infrastructure that does not exist, and would
  block every canary until then.
- **Upload intake objects ad hoc from the producer workflow:** no scope
  checks, no canonical records, and no idempotency; it would bypass the
  record shape the reconciler verifies.

## References

- Shapes: [specs/intake-submit.md](../specs/intake-submit.md),
  [specs/production-r2-reconciliation.md](../specs/production-r2-reconciliation.md),
  [design/ci-cd.md](../design/ci-cd.md)
- Builds on: [ADR-0013](0013-separate-generic-generation-from-production-publishing.md),
  [plan 0001](../plans/0001-frostyard-production-publisher.md) T0
