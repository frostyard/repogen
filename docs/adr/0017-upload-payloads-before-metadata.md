# 0017 — Upload payloads before metadata before indexes

- **Status:** Proposed
- **Date:** 2026-11-02

## Context

[Issue #84](https://github.com/frostyard/repogen/issues/84): the
`publish-to-r2` action uploaded the whole `./repo` tree with one
`aws s3 sync ./repo "$S3_PATH"`. `aws s3 sync` uploads objects in parallel and
in no fixed order, so metadata and payloads race.

On 2026-11-02 (snosi run
[41384581449](https://github.com/frostyard/snosi/actions/runs/41384581449),
action pin `c3c9f63`, v0.7.0) one payload upload failed with an R2
`InvalidPart` error on `CompleteMultipartUpload`. The metadata that lists that
payload was already live: `ext/chatgpt/SHA256SUMS` names
`chatgpt_26.1027.51404+r1_13_x86-64.raw.zst`, and that object returns 404. It
was the newest version, so `systemd-sysupdate`/updex clients picked it and
failed. The error looks transient — the previous 29 main runs had none — but
the damage is durable: v0.7.0 digest reconciliation refuses a rebuild of the
same identity with other bytes, so a rerun cannot repair the dangling entry.

Consumers read a manifest and then fetch the payloads it lists. A missing
payload is a broken client. A missing manifest is not: the client keeps the
old, consistent prefix. So the invariant is: **live metadata must never
reference a payload that is not in the bucket.**

## Decision

The action uploads in three upload phases and one verify pass, in this order, through a new script
`scripts/upload-to-r2.sh` (the action keeps only bucket/prefix plumbing):

1. **Payloads** — `aws s3 sync` with `--exclude '*'` followed by one
   `--include` per payload pattern: `*.raw`, `*.raw.zst`, `*.raw.xz`,
   `*.raw.gz`, `*.apk`, `*.pkg.tar`, `*.pkg.tar.*` (this also covers pacman
   `.sig` files) and `*.bottle.tar*`. These are the shapes
   `internal/scanner/detector.go` detects; Debian is absent because the action
   refuses `deb` ([ADR-0015](0015-remove-the-superseded-production-path.md)).
2. **Verify payloads** — the same filters with `--dryrun --size-only`. Any
   listed `upload:` line means a payload is missing or has another size (the
   pass does not compare bytes; phase 1 already overwrote any older object of
   the same name, because the local copy is newer); the
   script prints `::error::` with the files and stops before uploading
   metadata. The verify step also catches a payload the sync silently skipped.
3. **Metadata** — `aws s3 sync` excluding every payload and index pattern, so
   manifests, signatures, `.transfer` files, APKINDEX files, pacman databases
   and formulae go up.
4. **Indexes** — `aws s3 sync` with `--exclude '*'` and `--include` for
   `index`, `*/index`, `index.html`, `*/index.html`, because a directory index
   enumerates both payloads and metadata.

Phases 1, 3 and 4 each retry up to `UPLOAD_ATTEMPTS` (default 4) with a
`UPLOAD_RETRY_DELAY * N` second wait (default 15, so 15/30/45). `aws s3 sync`
is idempotent, so a retry sends only what is still missing. The script also
defaults `AWS_RETRY_MODE=standard` and `AWS_MAX_ATTEMPTS=10` when the caller
sets neither. Filters are bash arrays with every pattern quoted, so the shell
never expands them.

## Consequences

- No consumer input changes: `publish-to-r2` takes the same inputs and produces
  the same bucket layout. A consumer gets the new order by moving its action
  pin and `repogen-version`/`repogen-commit` together to the release that
  contains this change.
- A failed payload phase now leaves the previously published metadata live and
  consistent. No metadata is uploaded in that run.
- Each run costs 3 to 4 extra `ListObjectsV2` calls (one per phase plus the
  verify pass). Payload PUTs are unchanged; the phases only split them.
- **Residual, not fixed:** inside phase 3, `SHA256SUMS` and `SHA256SUMS.gpg`
  still change in two separate PUTs, so a client can read a new manifest with
  the old signature (or the reverse) in that window. That is a short
  signature mismatch on an existing payload, never a dangling reference.
  Fixing it needs object-level atomicity that static R2 storage does not
  offer.
- **Residual, unchanged:** the caller must still serialize complete
  restore/generate/upload cycles, because `ext/index` is shared between
  publishers.
- A phase that fails after all attempts prints `::error::` and exits non-zero,
  so the run stays red and the cache purge step is skipped, as before.

## Alternatives considered

- **Keep one `aws s3 sync` and add retries only:** rejected. Retries do not
  change the order, so metadata can still be live before its payload. The
  failure would just be rarer.
- **Upload each file with `aws s3 cp`:** rejected. It is far slower (one
  process and one request per object), it needs its own up-to-date check, and
  it loses the sync filter and skip behavior the restore path relies on.

## References

- Shapes: [design/ci-cd.md](../design/ci-cd.md),
  [specs/generators.md](../specs/generators.md)
- Builds on: [ADR-0011](0011-incremental-state-from-published-metadata.md)
  (published metadata is the incremental state, so it must match the bucket),
  [ADR-0013](0013-separate-generic-generation-from-production-publishing.md)
  (generation and publishing stay separate)
- Issue: [frostyard/repogen#84](https://github.com/frostyard/repogen/issues/84)
