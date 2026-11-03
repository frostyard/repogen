# 0018 — Opt-in identity-level skip for already published sysexts

- **Status:** Proposed
- **Date:** 2026-11-03

## Context

Incremental generation reconstructs its state from the published manifests
([ADR-0011](0011-incremental-state-from-published-metadata.md)). For sysexts
that state is a strict, signature-verified read: `utils.DetectDigestConflicts`
in `internal/cli/generate.go` fails the whole run when an incoming sysext has a
logical identity that is already published but whose filename or SHA-256
differs. That rule is deliberate. It is also unusable for a rebuild:

- mkosi output is not byte-reproducible. Two builds of the same extension
  version and revision produce different bytes, so the SHA-256 always differs.
- A snosi nightly build therefore rebuilds every sysext and passes all of them
  to `publish-to-r2`. Every already published identity is a digest conflict,
  and the run fails.
- snosi works around this with `shared/sysext/publish/drop-published.sh`, a
  shell script that reads `SHA256SUMS` **without verifying its signature** and
  deletes the matching input files before repogen sees them. A tampered or
  truncated manifest silently changes what gets published, and the workaround
  lives outside the tool that owns the contract.
- Removing bytes from the input also hides a real fault: snosi run
  [41384581449](https://github.com/frostyard/snosi/actions/runs/41384581449)
  (the ADR-0017 incident) published a manifest that names a payload object
  which is not in the bucket. A "skip because it is already published" step
  that never checks the bucket keeps that state forever, because a rebuild
  cannot republish the identity.

## Decision

`repogen generate` gets an opt-in flag, and the `publish-to-r2` action gets a
matching input. Both are off by default; without them nothing changes.

- **CLI.** `--skip-published-identities` (sysext only, requires
  `--incremental` and `--gpg-key`) skips each incoming sysext whose identity
  `name:version:OSVersion:arch` is already listed in the
  signature-verified `SHA256SUMS`, whatever its bytes and whatever its
  compression suffix. `--skipped-report FILE` writes the published payload
  path (`ext/NAME/FILE`) of each skipped sysext, one per line, sorted; an
  empty file when nothing was skipped. `--skipped-report` without
  `--skip-published-identities`, `--skip-published-identities` without
  `--incremental`, and `--skip-published-identities` without `--gpg-key`, are
  configuration errors. A run whose scan finds any
  non-sysext package is refused when the flag is on, and a sysext that the
  parser rejects fails the run (in default mode it is only a warning), so a
  broken build cannot look like "nothing to publish".
- **Read.** The published identities come from
  `sysext.Generator.PublishedPackages`, which returns nothing when
  `<output>/ext` does not exist (first publish) and otherwise runs the same
  strict read the generator uses for merging: every manifest parsed strictly,
  every present payload matched against its digest, every `SHA256SUMS.gpg`
  verified with the configured signer, every transfer file and the index
  checked. **It fails closed**: there is no lenient fallback, and any error
  fails the run.
- **No-op.** When every identity was skipped, the run writes the report, logs
  that nothing is published, and returns success without writing output and
  without generating an HTML index. The tree is not touched.
- **Action.** The `publish-to-r2` input `skip-published-identities` (default
  `'false'`) passes the flag and a `--skipped-report` file under
  `$RUNNER_TEMP`. A new `published` output is `'true'` when the action
  uploaded to R2 and `'false'` when the skip left nothing to publish; the
  upload step and the cache purge step are gated on it. `packages-added`
  counts the payloads under `./repo` in that mode, so the step refuses a
  `./repo` that already holds a payload from an earlier step in the same job
  instead of counting it as added. A new step,
  `Check skipped identities have their payloads`, runs
  `scripts/check-published-payloads.sh` before the upload. It reads the report
  and sends one `head-object` request per skipped identity, because such an
  identity can never be repaired by a rebuild. A key that is missing or
  unreadable fails the step; the key is a request parameter, so no response
  decoding can confuse two keys.
- **Signing key required.** Published identities come only from
  signature-verified manifests, so the CLI refuses
  `--skip-published-identities` without `--gpg-key`, and
  `sysext.Generator.PublishedPackages` refuses to run without a signer. The
  action refuses the input when `gpg-private-key` is empty. A repository that
  was published unsigned therefore cannot use the mode: its manifests have no
  signature to verify.
- **Every candidate counts.** A regular input file whose base name ends in
  `.raw`, `.raw.zst`, `.raw.xz` or `.raw.gz` must reach the scan as a sysext.
  The scanner logs and drops a file whose detection fails (a zero-byte image
  gives EOF), so the mode walks the input directory and refuses the run when
  such a candidate was dropped. Otherwise a rebuilt extension could be skipped
  silently.
- **Empty input stays an error.** The action still fails when `packages-dir`
  holds no package at all; only a full skip of *built* sysexts is a clean
  no-op.

## Consequences

- **Known gap.** A content change without a version or `SYSEXT_REVISION` bump
  keeps its identity, so it is skipped and **is not republished**: the old
  bytes stay live. This is the price of an identity-level skip. The operator
  must bump the version or the revision. The gap is stated in the flag help,
  in the action input description, and in `README.md`.
- The signed-manifest read now gates publication, so a wrong or missing
  signature on a published manifest stops the nightly build instead of being
  ignored. That is the same fail-closed behavior the merge already has. An
  unsigned repository cannot use the mode at all: there is no signature to
  verify.
- sysext only. Other formats need no new mode: for them `--skip-duplicates`
  already skips by identity (`utils.DetectConflicts`), and only sysext has the
  digest-conflict check.
- The skipped-payload check costs one `HEAD` request per skipped identity, and
  no request when the report is empty. A bucket that returns an error for a
  present object is reported as missing or unreadable; the run stays red.
- Adoption order: Brian tags `v0.7.2` after this merges; then a separate snosi
  PR, owned by its maintainer, sets `skip-published-identities: true` on the
  `publish-to-r2` step and deletes `shared/sysext/publish/drop-published.sh`.

## Alternatives considered

- **Keep the snosi script.** Rejected: it reads `SHA256SUMS` without verifying
  the signature, it removes evidence from the input, and it duplicates the
  identity rule outside repogen, so the two can drift.
- **Make `--skip-duplicates` identity-level.** Rejected: that flag is on in
  nightly sysext runs and its contract is fail-closed on changed bytes
  ([ADR-0011](0011-incremental-state-from-published-metadata.md)). Changing it
  would silently drop rebuilds for every existing caller.
- **Replace the changed bytes of a published identity.** Rejected: it rewrites
  published content under clients that already verified or cached it, and it
  breaks the digest contract the manifests exist to provide.

## References

- Shapes: [specs/generators.md](../specs/generators.md),
  [design/ci-cd.md](../design/ci-cd.md),
  [design/overview.md](../design/overview.md)
- Builds on: [ADR-0011](0011-incremental-state-from-published-metadata.md)
  (published metadata is the incremental state),
  [ADR-0017](0017-upload-payloads-before-metadata.md) (a live manifest must
  never reference a missing payload)
- Issue: [frostyard/repogen#84](https://github.com/frostyard/repogen/issues/84)
