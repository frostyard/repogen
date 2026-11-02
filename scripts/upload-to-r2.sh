#!/usr/bin/env bash
# Upload a generated repogen tree to an S3-compatible bucket (Cloudflare R2)
# in an order that keeps live metadata honest.
#
# Why (frostyard/repogen#84): a single `aws s3 sync ./repo s3://...` uploads
# payloads and metadata in parallel. On 2026-11-02 one payload upload failed
# with R2 `InvalidPart` on `CompleteMultipartUpload`, but `ext/chatgpt/SHA256SUMS`
# had already been uploaded and lists the payload that never arrived. Consumers
# then fetched a manifest that references a missing file.
#
# The rule this script enforces: live metadata must never reference a payload
# that is not in the bucket. So the upload runs in four phases, each an
# `aws s3 sync` with include/exclude filters over the paths that
# `internal/scanner/detector.go` detects:
#
#   Phase 0 (classify) refuses to start when a base name has one of the two
#     detector shapes that are not end-anchored (`.pkg.tar.` and
#     `.bottle.tar`) but no payload pattern matches the path, because such a
#     file would otherwise go up with the metadata while a consumer expects a
#     payload.
#   Phase 1 (payloads) uploads only package payloads (`.raw*`, `.apk`,
#     `.pkg.tar*` with their `.sig` files, Homebrew bottles).
#   Phase 2 (verify payloads) re-runs the phase 1 filters as `--dryrun
#     --size-only` and fails if anything is still missing, so a silently
#     skipped payload cannot be followed by metadata that references it.
#   Phase 3 (metadata) uploads everything that is not a payload and not a
#     directory index (`SHA256SUMS`, signatures, `.transfer`, APKINDEX,
#     pacman databases, formulae).
#   Phase 4 (indexes) uploads only the directory indexes, last, because they
#     enumerate the payloads and the metadata.
#
# Phases 1, 3 and 4 each retry up to UPLOAD_ATTEMPTS times. `aws s3 sync` is
# idempotent, so a retry uploads only what is still missing.
#
# Inputs (environment):
#   S3_PATH            required, e.g. s3://bucket/prefix
#   R2_ENDPOINT        required, e.g. https://<account>.r2.cloudflarestorage.com
#   REPO_DIR           optional, default ./repo
#   UPLOAD_ATTEMPTS    optional, default 4
#   UPLOAD_RETRY_DELAY optional, default 15 seconds; the wait before attempt
#                      N+1 is delay * N (with 0 there is no wait)
#   AWS_RETRY_MODE     default standard (exported when unset)
#   AWS_MAX_ATTEMPTS   default 10 (exported when unset)

set -euo pipefail

S3_PATH="${S3_PATH:-}"
R2_ENDPOINT="${R2_ENDPOINT:-}"
REPO_DIR="${REPO_DIR:-./repo}"
UPLOAD_ATTEMPTS="${UPLOAD_ATTEMPTS:-4}"
UPLOAD_RETRY_DELAY="${UPLOAD_RETRY_DELAY:-15}"

export AWS_RETRY_MODE="${AWS_RETRY_MODE:-standard}"
export AWS_MAX_ATTEMPTS="${AWS_MAX_ATTEMPTS:-10}"

if [[ -z "$S3_PATH" ]]; then
  echo "::error::S3_PATH is required (for example s3://bucket/prefix)"
  exit 1
fi
if [[ -z "$R2_ENDPOINT" ]]; then
  echo "::error::R2_ENDPOINT is required (for example https://<account>.r2.cloudflarestorage.com)"
  exit 1
fi
if [[ ! -d "$REPO_DIR" ]]; then
  echo "::error::REPO_DIR does not exist: $REPO_DIR"
  exit 1
fi
if [[ ! "$UPLOAD_ATTEMPTS" =~ ^[0-9]+$ ]] || (( UPLOAD_ATTEMPTS < 1 )); then
  echo "::error::UPLOAD_ATTEMPTS must be a positive integer, got '$UPLOAD_ATTEMPTS'"
  exit 1
fi
if [[ ! "$UPLOAD_RETRY_DELAY" =~ ^[0-9]+$ ]]; then
  echo "::error::UPLOAD_RETRY_DELAY must be a whole number of seconds, got '$UPLOAD_RETRY_DELAY'"
  exit 1
fi

# Payload patterns. An `aws s3 sync` filter is matched against the path
# relative to the source, and `*` also matches `/`. Every pattern here is
# end-anchored on purpose: a trailing `*` such as `*.pkg.tar.*` or
# `*.bottle.tar*` would also match a file inside a directory whose name merely
# looks like a payload (for example `ext/foo.pkg.tar.x/SHA256SUMS`), and phase 1
# would then upload that manifest beside its payload. Phase 0 below catches a
# payload-like file name that these patterns do not cover.
# Debian payloads are absent on purpose: the publish-to-r2 action refuses `deb`
# (ADR-0015).
payload_patterns=(
  '*.raw'
  '*.raw.zst'
  '*.raw.xz'
  '*.raw.gz'
  '*.apk'
  '*.pkg.tar'
  '*.pkg.tar.zst'
  '*.pkg.tar.xz'
  '*.pkg.tar.gz'
  '*.pkg.tar.sig'
  '*.pkg.tar.zst.sig'
  '*.pkg.tar.xz.sig'
  '*.pkg.tar.gz.sig'
  '*.bottle.tar'
  '*.bottle.tar.gz'
)

# Directory index patterns, uploaded last.
index_patterns=(
  'index'
  '*/index'
  'index.html'
  '*/index.html'
)

payload_only_filters=(--exclude '*')
for pattern in "${payload_patterns[@]}"; do
  payload_only_filters+=(--include "$pattern")
done

metadata_filters=()
for pattern in "${payload_patterns[@]}"; do
  metadata_filters+=(--exclude "$pattern")
done
for pattern in "${index_patterns[@]}"; do
  metadata_filters+=(--exclude "$pattern")
done

index_only_filters=(--exclude '*')
for pattern in "${index_patterns[@]}"; do
  index_only_filters+=(--include "$pattern")
done

# run_phase NUMBER NAME COMMAND... runs COMMAND in a retry loop. Any non-zero
# exit is a failure; the run is retried until UPLOAD_ATTEMPTS is exhausted.
run_phase() {
  local number="$1"
  local name="$2"
  shift 2

  local attempt wait
  for ((attempt = 1; attempt <= UPLOAD_ATTEMPTS; attempt++)); do
    echo "Phase $number ($name): attempt $attempt of $UPLOAD_ATTEMPTS"
    if "$@"; then
      return 0
    fi
    if ((attempt < UPLOAD_ATTEMPTS)); then
      wait=$((UPLOAD_RETRY_DELAY * attempt))
      echo "::warning::Phase $number ($name) failed on attempt $attempt of $UPLOAD_ATTEMPTS; retrying in ${wait}s"
      sleep "$wait"
    fi
  done

  return 1
}

echo "Uploading $REPO_DIR to $S3_PATH in payload -> metadata -> index order"

# Phase 0: classify. The scanner detects a payload by magic bytes plus one of
# two name shapes that are not end-anchored: any base name that contains
# `.pkg.tar.`, and any base name that contains `.bottle.tar`. The payload
# patterns cover only the end-anchored suffixes, so a file with one of those two
# shapes that no pattern matches would be uploaded in phase 3, beside metadata
# that may list it as a payload. Refuse the whole run instead, before any
# upload.
#
# Two exclusions keep valid metadata out of the guard:
#   - only the base name is inspected, so a directory that merely looks like a
#     payload (`ext/foo.pkg.tar.x/`) does not trip it;
#   - `*.transfer` is skipped, because the sysext generator writes
#     `ext/<name>/<name>.transfer` and `<name>` may itself contain `.pkg.tar.`
#     or `.bottle.tar` (`foo.pkg.tar.x.transfer`, `foo.bottle.tar.transfer`).
#
# A sysext has no such problem: the scanner detects it only by the end-anchored
# suffixes `.raw`, `.raw.zst`, `.raw.xz` and `.raw.gz`, which are exactly the
# four patterns phase 1 uses, so no sysext payload can be missed here.
echo "Phase 0 (classify): checking every file name against the payload patterns"
unclassified=()
while IFS= read -r -d '' file; do
  rel="${file#"$REPO_DIR"/}"
  base="${rel##*/}"
  case "$base" in
    *.transfer) continue ;;
    *.pkg.tar.* | *.bottle.tar*) ;;
    *) continue ;;
  esac

  matched=0
  for pattern in "${payload_patterns[@]}"; do
    # The pattern stays unquoted on purpose: this is a glob match with the
    # same semantics as an `aws s3 sync` filter.
    # shellcheck disable=SC2053
    if [[ $rel == $pattern ]]; then
      matched=1
      break
    fi
  done
  ((matched)) || unclassified+=("$rel")
done < <(find "$REPO_DIR" -type f -print0 | LC_ALL=C sort -z)

if (( ${#unclassified[@]} > 0 )); then
  for rel in "${unclassified[@]}"; do
    echo "::error::$rel has a payload-like name that no payload pattern matches; refusing to upload it with metadata"
  done
  echo "::error::Phase 0 (classify) failed; nothing was uploaded"
  exit 1
fi

# Phase 1: payloads only.
if ! run_phase 1 "payloads" \
  aws s3 sync "$REPO_DIR" "$S3_PATH" \
  --endpoint-url "$R2_ENDPOINT" \
  "${payload_only_filters[@]}"; then
  echo "::error::Phase 1 (payloads) failed after $UPLOAD_ATTEMPTS attempts; no metadata was uploaded, so the published metadata stays consistent with the bucket"
  exit 1
fi

# Phase 2: prove that every payload is now in the bucket. --size-only lists a
# payload that is missing or has another size, and does not depend on the
# runner clock (the default check compares modification times); it does not
# compare bytes. --dryrun lists what a sync would still upload and copies
# nothing.
echo "Phase 2 (verify payloads): checking that every payload is in $S3_PATH"
verify_output=""
if ! verify_output="$(aws s3 sync "$REPO_DIR" "$S3_PATH" \
  --endpoint-url "$R2_ENDPOINT" \
  --dryrun --size-only \
  "${payload_only_filters[@]}" 2>&1)"; then
  echo "::error::Phase 2 (verify payloads) could not compare $REPO_DIR with $S3_PATH; no metadata was uploaded"
  echo "$verify_output"
  exit 1
fi
missing_payloads="$(grep -F 'upload:' <<<"$verify_output" || true)"
if [[ -n "$missing_payloads" ]]; then
  echo "::error::Phase 2 (verify payloads) found payload files that are not in $S3_PATH, so no metadata was uploaded:"
  echo "$missing_payloads"
  exit 1
fi

# Phase 3: metadata and everything else that is not a payload or an index.
if ! run_phase 3 "metadata" \
  aws s3 sync "$REPO_DIR" "$S3_PATH" \
  --endpoint-url "$R2_ENDPOINT" \
  "${metadata_filters[@]}"; then
  echo "::error::Phase 3 (metadata) failed after $UPLOAD_ATTEMPTS attempts"
  exit 1
fi

# Phase 4: directory indexes last.
if ! run_phase 4 "indexes" \
  aws s3 sync "$REPO_DIR" "$S3_PATH" \
  --endpoint-url "$R2_ENDPOINT" \
  "${index_only_filters[@]}"; then
  echo "::error::Phase 4 (indexes) failed after $UPLOAD_ATTEMPTS attempts"
  exit 1
fi

echo "Upload complete: payloads, then metadata, then indexes are in $S3_PATH"
