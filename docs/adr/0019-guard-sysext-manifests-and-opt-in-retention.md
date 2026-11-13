# 0019 — Guard sysext manifests with conditional writes; prune only on request

- **Status:** Proposed
- **Date:** 2026-11-13

## Context

[Issue #102](https://github.com/frostyard/repogen/issues/102): snosi publishes
the sysexts of 2 Debian suites into one bucket
([core Plan 0009 Phase 6](https://github.com/frostyard/core/blob/main/docs/plans/0009-debian-publication-through-apt-publisher.md)):
trixie files carry the OS version `13` (`incus_7.3_13_x86-64.raw`), forky
files carry `14`. Both land in the same `ext/<name>/` directory with one
`SHA256SUMS` and one `SHA256SUMS.gpg`.

- Each `publish-to-r2` run restores the published metadata, merges its new
  entries (`internal/generator/sysext/generator.go`, `generateForExtension`)
  and writes `SHA256SUMS` back. The write had no condition. Two runs that
  overlap (2 CI runners, so a local `flock` cannot help) both read version A
  of the manifest; the run that writes last replaces the entries of the other
  run (a lost update). The payloads of the lost entries stay in the bucket,
  but no client finds them.
- Nothing prunes `ext/`: the upload never deletes, so `ext/<name>/` and its
  `SHA256SUMS` grow with every published version, for each suite.
- Cloudflare R2 evaluates `If-Match` and `If-None-Match` on `PutObject`
  atomically against the committed object and answers `412
  PreconditionFailed` on a mismatch (R2 S3 API, "Conditional operations").
  AWS CLI v2 passes them as `aws s3api put-object --if-match` and
  `--if-none-match` (from v2.22); an older CLI refuses them as unknown options.
- A client (systemd-sysupdate, updex) reads `SHA256SUMS`, checks
  `SHA256SUMS.gpg`, and fetches the newest version it lists. The rule of
  [ADR-0017](0017-upload-payloads-before-metadata.md) stays: live metadata must
  never list a payload that is not in the bucket.

## Decision

### 1. Conditional writes for sysext manifests and signatures

- **Restore with ETags.** For `package-type: sysext` the action restores
  `ext/` through `scripts/sync-sysext-metadata.sh`. It syncs every non-payload
  file except the guarded ones (`ext/<name>/SHA256SUMS` and
  `ext/<name>/SHA256SUMS.gpg`), lists the guarded keys with
  `aws s3api list-objects-v2`, and downloads each with
  `aws s3api get-object`, which returns the ETag of exactly the bytes it wrote.
  It records `path, ETag, SHA-256 of the bytes` in a state file under
  `$RUNNER_TEMP`. An ETag read after a download (a separate `HEAD`) is never
  used: it can belong to a newer object and would hide the race.
- **Write with conditions.** `scripts/upload-to-r2.sh` with `METADATA_ETAGS`
  set leaves the guarded files out of phase 3 and writes them in a new phase
  3b, after the rest of the metadata and before the indexes, with one
  `aws s3api put-object` each: `--if-match <restored ETag>`, or
  `--if-none-match '*'` when the object did not exist at the restore. Each
  `SHA256SUMS` goes before its `SHA256SUMS.gpg`; changed extensions go before
  new ones. A manifest whose bytes did not change is not written (repogen signs
  every manifest again, so its fresh signature is not uploaded either), so
  publishers that touch different extensions do not collide.
- **On 412.** The run stops with `::error::`: another publisher changed
  `ext/<name>/SHA256SUMS` since the restore, no metadata of this run replaced
  it, the payloads this run uploaded are not listed and so are harmless, and
  the job must be rerun. Nothing after it is written: no other manifest, no
  index. A 412 after an attempt whose response was lost is recognized by
  downloading the object and comparing it with the local bytes.
- **Old CLI.** An `aws` without the flags exits with "Unknown options"; the
  script reports that AWS CLI v2.22 or newer is needed and writes nothing
  without its condition. There is no unconditional fallback.
- **Scope.** Only sysext is guarded. For apk, pacman and homebrew the action
  does not set `METADATA_ETAGS`, and `upload-to-r2.sh` uploads exactly as
  before. ADR-0017's order is unchanged: payloads, verify, metadata (now with
  phase 3b), indexes.

### 2. Publishers must still serialize

Sysext publishers of one bucket must still run one at a time (snosi: 1 job,
or 1 `concurrency` group for all suites). The conditional write turns an
unserialized race into a failed run, not into lost entries. It does not make
the publication atomic. The windows that remain:

- `SHA256SUMS` and `SHA256SUMS.gpg` are 2 PUTs. Between them a client can read
  a new manifest with the old signature; it fails closed (updex and repogen
  both refuse a manifest whose signature does not verify) and succeeds on the
  next read. A publisher writes a signature only after its own manifest write
  won, so a 412 on `SHA256SUMS.gpg` means that an unguarded writer (an older
  action pin, a manual upload) changed it, or that another publisher wrote a
  byte-identical `SHA256SUMS` and its own signature (then the bucket holds that
  publisher's consistent pair). After an unguarded writer the pair can
  mismatch until one writer writes a consistent pair; a rerun after that writer
  finished repairs it.
- `ext/index` is not guarded. If 2 unserialized runs each add a new extension,
  the index of one can lose the name of the other, and a run that adds a new
  extension and then stops on a 412 leaves a live manifest that `ext/index`
  does not list. The next run fails closed (`verifyIndex`) until the index is
  repaired. Serialization prevents both.

### 3. Opt-in retention, off by default

- `repogen generate --sysext-keep-versions N` (default `0`, keep every
  version) keeps, for each identity group (name, OS version, arch), only the
  newest `N` versions in `SHA256SUMS`, and `--pruned-report FILE` writes the
  published payload path (`ext/NAME/FILE`) of each dropped entry. The action
  input is `sysext-keep-versions` (default `'0'`). **By default nothing is
  pruned**, and behavior is identical to before this ADR.
- **Version order** is systemd-sysupdate's: a port of systemd's
  `strverscmp_improved()` (the UAPI Version Format Specification), so the kept
  versions are the ones a client would pick as newest. Plain string order is
  not used (`9` would sort after `10`).
- **Groups.** Each OS version and each arch is a group of its own, so trixie
  files never push out forky files, and the reverse. `N >= 1`, so the newest
  version of each group always stays.
- **Generator.** Retention runs on the merged set (published entries plus
  incoming ones), before staging. A pruned incoming file is not copied; a
  pruned payload that is present in a local tree is removed in the same atomic
  switch as its manifest entry.
- **Delete after the metadata.** The action deletes the pruned payload objects
  in a step after the upload and the cache purge, which a failed step before it
  skips. `scripts/delete-pruned-payloads.sh` also downloads the live
  `ext/<name>/SHA256SUMS` and deletes a payload only when that live manifest
  (and the local one) does not list it. So a client never sees a listed file
  that is missing, and nothing is deleted when the metadata write failed.

## Consequences

- An unserialized second publisher now gets a red run instead of silently
  dropping the entries of the first. Rerunning it publishes its entries.
- A first sysext publish, or one after an upgrade of the action pin, needs AWS
  CLI v2.22 or newer on the runner. GitHub-hosted runners have it.
- Each sysext run costs 1 `ListObjectsV2` call and 1 `GetObject` per guarded
  file at the restore, and 1 `PutObject` per changed guarded file instead of a
  sync upload.
- Unchanged manifests keep their published signature: a run no longer
  re-uploads every `SHA256SUMS.gpg`.
- **Interplay with ADR-0018.** A run whose identity skip publishes nothing
  generates nothing, so it prunes nothing and its pruned report is empty;
  retention applies at the next run that publishes. A skipped identity that
  retention prunes in the same run is still checked by
  `scripts/check-published-payloads.sh` before the upload (it is still in the
  bucket then). A pruned identity is no longer in `SHA256SUMS`, so the skip
  does not see it: a rebuild of a pruned, older version is merged, pruned again
  by the same rule, and not uploaded.
- **CDN window.** A client that read a cached `SHA256SUMS` from before the
  prune can fetch a deleted older version and get 404. The newest version of
  each group is never pruned, so a client that picks the newest version is not
  affected; enable `purge-cache` to shorten the window.
- A pruned payload object that the delete step cannot delete is reported with
  `::error::` (red run, after the metadata is live); no later run deletes it,
  because it is no longer listed.

## Alternatives considered

- **Only document serialization:** rejected as the only measure. A
  misconfigured workflow would still lose entries silently; the conditional
  write costs little and makes the mistake loud.
- **Retry the merge on 412 inside the action:** rejected for now. It needs a
  second restore and a second repogen run inside the upload step, after the
  payloads went up; a rerun of the job does the same with less code.
- **Guard `ext/index` too:** deferred. A 412 there leaves the same broken state
  as a lost update (a live manifest the index does not list); only
  serialization or an index repair in the generator fixes it.
- **Prune by default:** rejected. Deleting published objects must be an
  explicit choice of the publisher, and pinned clients may need old versions.
- **Debian version order:** rejected. Clients choose by systemd's order;
  retention must agree with them.

## References

- Shapes: [design/ci-cd.md](../design/ci-cd.md),
  [specs/generators.md](../specs/generators.md),
  [guides/publish-to-r2.md](../guides/publish-to-r2.md),
  [guides/usage.md](../guides/usage.md)
- Builds on: [ADR-0011](0011-incremental-state-from-published-metadata.md)
  (published metadata is the incremental state),
  [ADR-0017](0017-upload-payloads-before-metadata.md) (payloads before
  metadata before indexes),
  [ADR-0018](0018-opt-in-identity-level-skip-for-published-sysexts.md)
  (identity-level skip)
- Issue: [frostyard/repogen#102](https://github.com/frostyard/repogen/issues/102)
