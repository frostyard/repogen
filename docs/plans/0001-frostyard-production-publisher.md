# Plan: Frostyard production publisher boundary

This plan defines the repository-local implementation contract for the
Frostyard APT publisher. R1 records the contract, R2 implements its
write-free input preflight, and R3 implements explicit absent-target
initialize plus strict signed prior-state reconciliation. Later phases remain
unimplemented. The plan
implements
[core ADR-0048](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md)
and the Repogen R1-R10 sequence in
[core Plan 0007](https://github.com/frostyard/core/blob/main/docs/plans/0007-support-suites-in-repogen.md).
No production generation, signing, publication, remote mutation, merge,
release, or production operation is implemented or authorized by the
R2-R3 validation path. R3's "restore" is read-only verification and in-memory
parsing of an already local prior tree.

## Current behavior and compatibility boundary

At the pinned source baseline
`ea8cd1f0b2fe4d2370a83dff1feacfa7a4aef9ae`, Repogen provides one generic
`generate` command. It scans every recognized package format under the input
directory, defaults Debian generation to `stable`, permits unsigned output,
and treats an incremental metadata parse error as a reason to continue with
only the new packages. The shared action downloads a mutable `latest` binary
by default, restores broad format-specific paths, runs generic generation,
and uploads with `aws s3 sync`.

Those behaviors remain available for local and non-production generation.
They are not a safe Frostyard production publication contract. The work in
this plan adds an opt-in production boundary rather than changing generic
generation into a Frostyard-specific tool.

The public state rechecked on 2026-09-12 still had only `dists/stable`.
`dists/trixie/Release` and `dists/forky/Release` returned 404. Stable had:

- Release SHA-256
  `067d873d8a51bb2e70b51da4cd488599d5133a47dac289f6ea52924aef6249ab`;
- InRelease SHA-256
  `68e35e909b8cbcd5f2d62362f4bee86366a56562e2ae361018564af207cef1c6`;
- `Origin: Repogen Repository`, `Label: Frostyard Repository`,
  `Suite: stable`, `Codename: stable`, `Components: main`, and
  `Architectures: all amd64`; and
- no `Acquire-By-Hash` or `Valid-Until` field.

These observations are evidence, not permission to write production state.

## Production contract

The production publisher is a separate, fail-closed caller of Repogen's
generic Debian generator. It owns production validation, restoration,
staging, signing, publication, and read-back. Generic callers do not gain
production authority by supplying the same values.

This separation is proposed in
[ADR-0013](../adr/0013-separate-generic-generation-from-production-publishing.md).

| Concern | Generic generation | Frostyard production publisher |
| --- | --- | --- |
| Formats | Any supported format, including mixed input | Debian `.deb` input only |
| Target | Caller-selected; current default is `stable` | Explicit immutable codename; `stable` rejected |
| Suite identity | Caller-selected/defaulted | `Suite` exactly equals `Codename` |
| Release identity | Caller-selected/defaulted | Fixed Origin/Label; component `main`; exact architecture allowlist |
| Signing | Optional | Required with the accepted Frostyard trust root |
| Existing state | Optional incremental parse with fallback | Signature- and checksum-verified strict reconcile |
| New state | Implicitly possible | Explicit initialize against an absent target only |
| Output | Direct local generation | Run-specific staging followed by scoped publication |
| Upload | Outside the generator | Explicit object operations; no whole-tree sync |
| Completion | Local generation returned success | Remote read-back and result manifest agree |

The initial fixed identity is:

| Field | Required value or rule |
| --- | --- |
| `Origin` | `Repogen Repository` |
| `Label` | `Frostyard Repository` |
| `Suite` | Exact requested immutable codename |
| `Codename` | Exact requested immutable codename |
| `Components` | `main` only |
| `Architectures` | Exact request allowlist; initial canary is `all amd64` |
| `Acquire-By-Hash` | `yes` before first visible new-suite generation |
| `Valid-Until` | Omitted initially |

Changing Origin/Label, allowing `stable`, adding a component, or adding
`Valid-Until` is not a routine flag change. It requires the corresponding
consumer-tested policy decision. A future `Valid-Until` decision must define
the expiry interval, refresh/resign cadence, monitoring, and outage behavior.

The production API names are deliberately not fixed by R1. R2 may use a
dedicated command or an explicit production mode, but the boundary must be
structural and testable rather than inferred from credentials or a collection
of ordinary `generate` flags.

## Initialize and reconcile

Every transaction names exactly one codename and exactly one operation:

- **Initialize** requires the target `dists/<codename>` prefix to be
  authoritatively absent and the request to declare no prior Release digest.
  A 404 observed during an ordinary reconcile never changes the operation to
  initialize.
- **Reconcile** requires a prior Release digest. It restores only the target
  suite, verifies the accepted signature, fixed identity, every advertised
  checksum and architecture index, and requires the observed Release digest
  to equal the request.

Timeouts, 403/5xx responses, partial metadata, missing expected metadata,
wrong-key or invalid signatures, checksum errors, identity drift, and any
architecture parse failure are fatal before a write. The current whole-run
and per-architecture fallback behavior is not used by the production path.

## Shared immutable pool

The accepted [shared `pool/main` layout](../adr/0002-shared-debian-pool-layout.md)
remains unchanged. Production safety comes from immutable object handling,
not a codename-scoped pool migration.

The publisher builds retained authority from signature- and
checksum-verified Packages indexes. For each requested pool path:

1. If retained metadata references the path, the incoming SHA-256 must match
   the stanza and the remote bytes must have been stream-hashed successfully.
2. If the path is not in the verified map, create it conditionally without
   overwrite.
3. If the conditional create loses a race, stream-hash the existing bytes.
   The exact same digest may be reused; a different digest or unreadable
   object aborts the transaction.

ETag is never treated as SHA-256. `--skip-duplicates` can no-op only when
package identity, path, and bytes all agree. Different Trixie and Forky bytes
must have suite-distinct Debian versions and filenames.

## Debian and sysext separation

The production APT writer processes only Debian packages and writes only the
approved `pool/main` paths and one `dists/<codename>` target. It never scans
or publishes sysext input as part of an APT transaction.

The existing generic sysext generator and the current Snosi layout remain
available while R9 supplies a separately reviewed sysext reconciliation path.
R1-R5 do not change `ext/`, sysext filename parsing, transfer files, or
`SHA256SUMS`. This preserves current callers without giving the Debian writer
authority over sysext objects.

## Manifests and staging

An immutable intake manifest binds:

- producer repository, source commit/tag, workflow run, and artifact ID;
- package name, Debian version, architecture, filename, size, and SHA-256;
- operation, requested codename/suite, component, architectures, and fixed
  Origin/Label;
- action commit, Repogen version, exact asset name, and binary SHA-256; and
- expected prior Release SHA-256, or the explicit initialize assertion.

The result manifest adds the signing-key fingerprint, verified pool-object
digests, and resulting Release/InRelease digests. A request is not complete
until remote read-back matches that result.

Generation occurs in a clean, run-specific staging directory. R5 publishes
only explicitly enumerated objects for the target transaction, in this order:

1. new immutable pool objects;
2. immutable `by-hash/SHA256/<digest>` indexes;
3. canonical Packages and Packages.gz;
4. Release and Release.gpg;
5. InRelease last as the visibility point.

HTML indexes, other codenames, `stable`, and sysext paths are outside that
transaction. Broad `aws s3 sync` is not an acceptable production mechanism.

## Rollback and retention

Before the first visible InRelease, removal of an unpublished failed prefix is
a separate approved operation. After visibility, keep the immutable suite and
correct forward. Never rewrite `stable`, Trixie, or Forky as rollback.

Consumer rollback selects a previously verified explicit APT source, image,
or channel. It does not imply a package downgrade, and package pin/downgrade
behavior must be tested separately. No plan phase automatically deletes
stanzas, pool objects, by-hash objects, manifests, or cache entries.

## R1-R5 canary boundary

The first Trixie `gchlog` canary is gated by this exact subset:

| Slice | Required outcome | Stop condition |
| --- | --- | --- |
| R1 | This reviewed contract preserves generic behavior and fixes the production boundary | Documentation claims implementation or weakens accepted ADRs |
| R2 | Production target/input/identity validation fails before writes | Implicit target, mixed format, path ambiguity, or identity drift |
| R3 | Signed strict restore and explicit absent-prefix initialize | Any read/verification/parse fallback |
| R4 | Verified digest map and conditional no-overwrite shared-pool handling | ETag trust, unreadable object, collision, or overwrite |
| R5 | Signed by-hash staged transaction, manifest, InRelease-last publication, and read-back | Mutable tool, broad sync, unsigned output, incomplete visibility, or stable drift |

R1-R5 permit at most one separately authorized, manually serialized,
retained canary request. After that first visible transaction, new-suite
writes freeze until R6-R10 add deterministic no-op behavior, full local
atomicity/failure injection, durable intake and recovery, per-codename
serialization, sysext reconciliation, and the hardened release.

## Release artifact blocker

[frostyard/repogen#34](https://github.com/frostyard/repogen/issues/34) remained
open when this plan was rechecked on 2026-09-12. Tag pushes currently trigger
two incompatible release pipelines: hand-built
`repogen-{os}-{arch}` binaries with `SHA256SUMS`, and GoReleaser archives
with different names and `checksums.txt`. The action assumes the hand-built
name, verifies only HTTP success, and cannot verify a real embedded
version/commit identity.

R1 records but does not resolve that defect. R5 must select one authoritative
asset name, checksum file, embedded version/commit output, and action download
path, then test digest-verified installation. No canary or downstream release
acceptance can proceed while the contract is ambiguous. An exception must
establish the same exact, digest-verified contract and receive separate human
acceptance; it cannot waive
[core ADR-0023](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md).

## Phase 1 - Record the boundary (R1)

- [x] Record the generic/production compatibility boundary and exact proposed
  production identity.
- [x] Define initialize/reconcile, shared-pool, Debian/sysext, manifest,
  staging, visibility, rollback, and R1-R5 canary contracts.
- [x] Record current implementation gaps and the unresolved release artifact
  contract without claiming either is fixed.
- **Done when:** this plan and core Plan 0007 agree, repository documentation
  gates pass, and an independent reviewer accepts the exact candidates.

## Phase 2 - Validate before writes (R2)

- [x] Add production-only target, identity, path, architecture, component, format,
  control-character, and Debian-only input validation.
- [x] Preserve the generic command and its existing non-production use.
- **Done when:** table-driven negative tests prove every invalid or ambiguous
  request fails before output or remote state changes.

## Phase 3 - Restore and protect immutable state (R3-R4)

- [x] Add strict signed restore and explicit initialization without writes.
- Add verified shared-pool digest authority and conditional creation.
- **Done when:** valid initialize/reconcile pass while missing, corrupt,
  tampered, partial, collision, unreadable, and race cases fail without
  changing prior bytes.

## Phase 4 - Publish one bounded canary (R5)

- Add clean staging, mandatory signing/by-hash, compact manifests,
  expected-prior checks, target-scoped ordered writes, and remote read-back.
- Resolve the authoritative Repogen release artifact contract.
- **Done when:** failure injection exposes no incomplete generation and the
  separately authorized `gchlog` canary passes real `gpgv` and apt install
  while `stable` remains byte-identical.

## Later / ideas

R6-R10 remain mandatory before closure expansion: deterministic/no-op
generation, complete local atomicity, durable recovery, sysext reconciliation,
and a separately human-published digest-verified Repogen release. Their order
and acceptance matrix remain authoritative in core Plan 0007.

## Open questions

- **Release artifact contract:** resolve frostyard/repogen#34 by R5 or obtain
  an exact reviewed and human-accepted exception with the same digest and
  embedded-identity guarantees.
- **Production API spelling:** choose a dedicated command or explicit mode in
  R2; either must make the production boundary structural and fail closed.

## References

- Governing policy:
  [core ADR-0048](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md)
- Cross-repository sequence:
  [core Plan 0007](https://github.com/frostyard/core/blob/main/docs/plans/0007-support-suites-in-repogen.md)
- Current architecture:
  [Repogen overview](../design/overview.md),
  [CI/CD and GitHub Action](../design/ci-cd.md), and
  [generator contract](../specs/generators.md)
- Repository decisions:
  [ADR-0002](../adr/0002-shared-debian-pool-layout.md),
  [ADR-0003](../adr/0003-single-main-component.md), and
  [ADR-0011](../adr/0011-incremental-state-from-published-metadata.md)
- Proposed repository boundary:
  [ADR-0013](../adr/0013-separate-generic-generation-from-production-publishing.md)
