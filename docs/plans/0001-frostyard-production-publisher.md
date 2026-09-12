# Plan: Frostyard production publisher boundary

This plan defines the repository-local implementation contract for the
Frostyard APT publisher. R1 records the contract, R2 implements its
write-free input preflight, and R3 implements explicit absent-target
initialize plus strict signed prior-state reconciliation. R4 provides the
provider-neutral immutable pool primitive, and R6 provides deterministic
local generation and signed no-op detection. R5 provides the signed,
target-scoped publication transaction and R7 provides atomic local
generation commit. R8 provides crash-durable local commits plus retained
intake and Debian writer recovery. The provider adapter/canary and R9-R10
remain unimplemented. The plan
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
| Output | Direct local generation | Run-specific staging followed by atomic local commit or scoped publication |
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

## T0 - Durable intake transport

This section is the repository-local profile of the
[core T0 contract](https://github.com/frostyard/core/blob/main/docs/plans/0007-support-suites-in-repogen.md#t0-durable-intake-transport-contract).
It specifies later implementation; no submit service, storage, identity,
workflow, permission, environment, token, secret, or production writer is
configured by this candidate.

### Boundary and durable namespace

Producers submit through an authenticated, submit-only endpoint. They never
receive credentials for the intake store, production package repository,
signer, or cache. The endpoint validates the authenticated producer against
an allowlist, streams and verifies artifact bytes, and conditionally creates
immutable intake objects. Direct producer access to an object-store bucket is
not conforming because a broad bucket credential could replace or delete
another request even when keys are digest-addressed.

The endpoint derives the authenticated principal rather than trusting a
request field. It binds the principal to the producer repository and, when
available, source ref, commit, workflow, and run claims; otherwise it performs
trusted provider read-back before acceptance. A self-asserted source ID is
provenance only. The receipt records the authorization-policy digest, not the
credential. A broader later policy does not retroactively authorize the
request, while a current explicit deny or revocation stops processing before
a write.

The durable backend must support conditional create, read-after-write
read-back, prefix enumeration, and retained records. Workflow dispatch is an
optional wake-up hint only; it is not a queue entry or completion signal.

All JSON uses UTF-8
[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785) canonical form without
duplicate keys or non-integer numeric fields. Objects use lowercase SHA-256
of canonical JSON or exact artifact bytes:

| Record | Key |
| --- | --- |
| Artifact | `blobs/sha256/<first-two>/<digest>` |
| Provenance | `manifests/provenance/v1/sha256/<digest>.json` |
| Request | `manifests/request/v1/sha256/<request-digest>.json` |
| Receipt | `receipts/v1/<kind>/<target>/<sequence>-<request-digest>.json` |
| Prior state | `manifests/prior/v1/sha256/<digest>.json` |
| Attempt | `attempts/v1/<request-digest>/<attempt-id>.json` |
| Result | `manifests/result/v1/sha256/<digest>.json` and a conditionally created `results/v1/<request-digest>.json` pointer |

The intake authority allocates a monotonic receipt sequence for each
`(kind, target)`. It acknowledges acceptance only after every referenced
blob, manifest, and receipt reads back with the expected digest. Replaying
identical canonical request bytes returns the existing receipt. Reusing a
producer submission key for different bytes fails. Cursors are rebuildable
caches; receipts remain the enumerable source of accepted work.

### Request and manifest graph

Every request contains `schema`, `kind`, `operation`, `target`, `producer`,
`provenance_digest`, ordered `artifact_digests`, and `expected_prior`.
`operation` is exactly `initialize` or `reconcile`. Initialize requires a null
expected prior; reconcile requires the exact prior Release or sysext-index
SHA-256.

The provenance manifest records the producer repository and commit, source
ref or tag, workflow/run and artifact identifiers, builder/environment pins,
exact action and Repogen version/commit/asset/digest, and every artifact's
filename, media type, size, SHA-256, package name, version, and architecture.
It may link producer-local test evidence but contains no credential value,
personal data, or unredacted command carrying a secret.

For `kind: debian`, the request also fixes codename and matching suite, the
accepted Origin and Label, component `main`, exact architecture allowlist,
and Valid-Until policy. It can reference Debian artifacts only. For
`kind: sysext`, the request fixes extension name, version, OSVersion,
architecture, and checksum-index identity. It cannot name `dists/` or
`pool/`. The two kinds use separate policy, writer capability, target
serialization, and result records.

Before a write, the writer records a prior manifest. Reconcile binds the
verified signature, Release identity, every index digest, and the
expected-prior match. Initialize binds an authoritative absent-target
observation. A 403, timeout, 5xx, partial response, or unverifiable 404 is not
absence.

Each attempt is append-only. Retryable failure does not close or delete the
request. A result is created only after remote read-back verifies the public
commit point and every advertised object. It links the request, provenance,
prior, attempt, exact action and Repogen binary, signing fingerprint, written
object digests, and resulting Release/InRelease or sysext-index digests.

### Replay and recovery

Scheduled and manual reconciliation list receipts, not workflow history:

1. Process one target by receipt sequence without cancelling earlier
   accepted work. Cross-target concurrency waits for shared-pool collision
   tests.
2. Re-hash every referenced object, verify the receipt's authorization-policy
   digest, and apply any current deny or revocation before staging.
3. If a result exists and public read-back still matches, return the same
   result as an idempotent no-op.
4. Without a result, compare public state with the expected prior and prior
   manifest. If the intended complete generation is already visible, verify
   it and record the result. If the prior generation remains visible, retry
   from clean staging. Any third state is drift and stops that target.
5. Conditionally create the result pointer only after read-back. Workflow
   success, dispatch delivery, staged upload, or process exit cannot close a
   request.

The single R1-R5 canary may use one reviewed request placed into this record
shape by the protected writer under a separately approved human operation.
It does not require a general producer credential or submit endpoint. R8
implements the endpoint, enumeration, scheduled/manual reconciliation, and
replay before closure expansion.

### Capability and approval boundary

The producer can call only the submit endpoint for an allowlisted identity,
kind, and target. The intake endpoint can only verify and conditionally create
intake records. The Debian writer can read accepted Debian intake, append
writer records, and perform target-scoped conditional writes to approved
`pool/main` objects and one `dists/<codename>` transaction; it cannot write
`stable`, another codename, `ext/`, or use broad sync. The sysext writer can
reconcile only its approved `ext/<name>` target and has no APT authority.
Verifier/monitor capability is read-only.

Signing material and passphrases exist only in the protected writer execution
and never enter manifests, logs, artifacts, or producer workspaces.
Short-lived federated producer identity is preferred, but the exact provider
mechanism remains a separately approved configuration choice. The selected
backend must be tested at its real credential boundary before capability
separation is described as enforced; workflow YAML or advisory permissions
alone are not proof.

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

R1-R5 plus T0 permit at most one separately authorized, manually serialized,
retained canary request using the T0 manifest graph. After that first visible
transaction, new-suite writes freeze until R6-R10 add deterministic no-op
behavior, full local atomicity/failure injection, the T0 submit and recovery
service, per-codename serialization, sysext reconciliation, and the hardened
release.

## Release artifact blocker

[frostyard/repogen#34](https://github.com/frostyard/repogen/issues/34) remained
open when this plan was rechecked on 2026-09-12. The accepted baseline has two
incompatible release pipelines: hand-built
`repogen-{os}-{arch}` binaries with `SHA256SUMS`, and GoReleaser archives
with different names and `checksums.txt`. The action assumes the hand-built
name, verifies only HTTP success, and cannot verify a real embedded
version/commit identity.

The R5 candidate replaces the hand-built workflow with one pinned GoReleaser
path and removes the parallel workflow. Its authoritative consumer
contract is an exact `vMAJOR.MINOR.PATCH` tag, exact 40-character source
commit, `repogen-linux-{amd64,arm64}`, and `SHA256SUMS`. The binary reports
its embedded identity through `repogen version --short`; the installer
uses the fixed GitHub release origin, downloads both assets, verifies exactly
one matching SHA-256 entry, and rejects any identity mismatch. The checksum is
unsigned and same-origin with the binary; there is no signature or attestation,
so authenticity depends on GitHub and repository release controls. Darwin
assets from the removed hand-built workflow are intentionally outside the new
Linux-only contract. This local candidate does not close issue 34, publish a
release, or prove an external release. D5 still requires the exact reviewed
merge and separately human-published, digest-verified release under
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

## T0 - Specify durable intake

- [x] Define the submit-only producer boundary and prohibit direct producer
  intake-store or production-repository credentials.
- [x] Define digest-addressed artifacts, provenance/request/prior/result
  manifests, target-sequenced receipts, and append-only attempts.
- [x] Define enumeration, idempotent replay, partial-attempt recovery, and
  public-read-back completion.
- [x] Keep Debian and sysext schemas, writers, paths, and credentials
  separate.
- **Done when:** core and Repogen plans agree, documentation gates pass, and
  independent review accepts the exact candidate. This does not mean the
  transport is implemented or configured.

## Phase 2 - Validate before writes (R2)

- [x] Add production-only target, identity, path, architecture, component, format,
  control-character, and Debian-only input validation.
- [x] Preserve the generic command and its existing non-production use.
- **Done when:** table-driven negative tests prove every invalid or ambiguous
  request fails before output or remote state changes.

## Phase 3 - Restore and protect immutable state (R3-R4)

- [x] Add strict signed restore and explicit initialization without writes.
- [x] Add the provider-neutral verified shared-pool digest authority and
  conditional-create primitive, with fake-S3 indexed/unindexed reuse,
  collision, unreadable, race, opaque-ETag, and cross-suite tests.
- **Done when:** valid initialize/reconcile pass while missing, corrupt,
  tampered, partial, collision, unreadable, and race cases fail without
  changing prior bytes.

## Phase 4 - Publish one bounded canary (R5)

- [x] Add clean staging, mandatory signing/by-hash, compact manifests,
  expected-prior checks, target-scoped ordered writes, and remote read-back.
- [x] Resolve the authoritative Repogen release artifact contract in the
  local candidate; external issue closure and release remain separate.
- [ ] Merge/configure a real provider adapter and execute the separately
  authorized retained `gchlog` canary.
- **Done when:** failure injection exposes no incomplete generation and the
  separately authorized `gchlog` canary passes real `gpgv` and apt install
  while `stable` remains byte-identical.

## Phase 5 - Deterministic generation and no-op detection (R6)

- [x] Canonically order Debian package stanzas, arbitrary fields,
  architectures, components, Release checksum entries, and sysext checksum
  entries.
- [x] Preflight Debian pool destinations so same-byte reuse is deterministic
  and conflicting bytes at one path fail before output is created.
- [x] Use deterministic gzip headers and expose a controlled Debian
  publication clock.
- [x] Preserve byte-identical Release, InRelease, and Release.gpg files
  without signing calls when the canonical metadata is unchanged and both
  prior signatures verify against the configured signer's public key.
- **Done when:** shuffled inputs produce byte-identical Packages,
  Packages.gz, Release, InRelease, and sysext checksum bytes; controlled Date
  tests pass; a changed package set receives a new timestamp and signatures;
  and an unchanged signed generation receives neither signing call.

## Phase 6 - Commit complete local generations atomically (R7)

- [x] Compose signed production staging with a complete sibling repository
  generation that retains unrelated suites and digest-identical shared-pool
  objects.
- [x] Verify exact reconcile prior bytes, staged object bytes, pool
  collisions, the final generation, and a second snapshot of the prior tree
  before commit.
- [x] Synchronize the candidate and switch it into place with one Linux
  `renameat2` no-replace or exchange operation; never use a remove/rename or
  two-rename rollback sequence.
- [x] Inject failure before every observed initialize and reconcile package,
  index, Release, signing, copy, install, verification, synchronization, and
  commit step.
- [x] Reject unsigned production staging while retaining generic unsigned
  generation unchanged.
- **Done when:** each injected failure leaves the old output tree
  unchanged; initialize and reconcile expose one complete signed generation;
  unrelated stable fixture bytes and modes remain unchanged; and unsupported
  platforms fail rather than weaken atomicity.
- **R8 boundary:** R7 alone provides one atomically visible namespace switch
  without parent-directory durability. Phase 7 closes that boundary with the
  journal, parent synchronization, and exact-state recovery below.

## Phase 7 - Recover durable writer work (R8)

- [x] Persist and synchronize an exact prior/candidate journal around the
  local atomic switch, synchronize the parent after commit, and recover
  interrupted pre-switch and post-switch states without rollback.
- [x] Implement a retained local intake store with conditional create,
  read-after-write, prefix enumeration, process-safe target locks, immutable
  requests, monotonic receipts, append-only attempts, and result pointers.
- [x] Enumerate receipts for scheduled/manual recovery, process each codename
  in sequence without cancellation, allow independent codenames to progress,
  and recheck current policy and every referenced digest.
- [x] Resume a partial Debian publication only from exact prior/candidate
  object states, preserve conditional shared-pool behavior, and create
  completion records only after complete public read-back.
- **Done when:** crash-boundary, coalesced-wakeup, same-target ordering,
  cross-target concurrency, partial-attempt replay, permission/5xx/timeout,
  checksum, ambiguous-state, read-back, GPG, and APT fixture tests pass under
  the race detector.
- **Boundary:** this is local library and fixture evidence. No endpoint,
  provider adapter, credential, workflow, schedule, publication, or
  production mutation is configured or authorized.

## Later / ideas

R9-R10 remain mandatory before closure expansion: sysext reconciliation and a
separately human-published digest-verified Repogen release. Their order and
acceptance matrix remain authoritative in core Plan 0007. R8 does not wire the
read-only production validator to a provider, configure credentials, or
confer publication authority.

## Open questions

- **Release publication:** issue 34 remains externally open; D5 must prove
  the reviewed merged workflow and separately published release satisfy this
  candidate contract. Candidate review must retain or independently reproduce
  exact GoReleaser config/snapshot output, asset names, checksums, and embedded
  identities; the repository tests validate the contract structure but do not
  execute GoReleaser.
- **Production adapter/API:** select and separately approve the real provider
  adapter and external command/service wiring. The current library boundary
  and fixtures do not carry credentials or publication authority.

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
