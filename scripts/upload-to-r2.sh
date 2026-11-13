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
# Sysext write guard (ADR-0019, frostyard/repogen#102): when METADATA_ETAGS is
# set (the publish-to-r2 action sets it for package-type sysext only), phase 3
# does not sync ext/<name>/SHA256SUMS and ext/<name>/SHA256SUMS.gpg. Phase 3b
# writes them instead, one `aws s3api put-object` each, with `--if-match` the
# ETag scripts/sync-sysext-metadata.sh recorded at the restore, or with
# `--if-none-match '*'` when the object did not exist then. A manifest whose
# bytes did not change is not written. SHA256SUMS goes before its signature.
# A 412 (another publisher wrote the object since the restore) stops the run
# with `::error::` before anything else is written; the payloads phase 1 already
# uploaded are not listed by any manifest, so they are harmless. Without
# METADATA_ETAGS every package type uploads exactly as before.
#
# Inputs (environment):
#   S3_PATH            required, e.g. s3://bucket/prefix
#   R2_ENDPOINT        required, e.g. https://<account>.r2.cloudflarestorage.com
#   REPO_DIR           optional, default ./repo
#   METADATA_ETAGS     optional, the ETag state file of
#                      scripts/sync-sysext-metadata.sh; turns on the sysext
#                      write guard (phase 3b)
#   UPLOAD_ATTEMPTS    optional, default 4
#   UPLOAD_RETRY_DELAY optional, default 15 seconds; the wait before attempt
#                      N+1 is delay * N (with 0 there is no wait)
#   AWS_RETRY_MODE     default standard (exported when unset)
#   AWS_MAX_ATTEMPTS   default 10 (exported when unset)

set -euo pipefail

S3_PATH="${S3_PATH:-}"
R2_ENDPOINT="${R2_ENDPOINT:-}"
REPO_DIR="${REPO_DIR:-./repo}"
METADATA_ETAGS="${METADATA_ETAGS:-}"
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

# The sysext write guard (phase 3b) writes these files itself, so phase 3
# leaves them out. `*` also matches `/`, so a deeper path matches too; the
# guard check below refuses such a path before any upload.
guarded_patterns=(
  'ext/*/SHA256SUMS'
  'ext/*/SHA256SUMS.gpg'
)
if [[ -n "$METADATA_ETAGS" ]]; then
  for pattern in "${guarded_patterns[@]}"; do
    metadata_filters+=(--exclude "$pattern")
  done
fi

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
#   - the one generated sysext transfer file, `ext/<name>/<name>.transfer`, is
#     exempt, because `<name>` may itself contain `.pkg.tar.` or `.bottle.tar`
#     (`ext/foo.pkg.tar.x/foo.pkg.tar.x.transfer`). No other `.transfer` file
#     is exempt: a bottle named `foo--1.0.arm64_sonoma.bottle.tar.gz.transfer`
#     is still refused, because no payload pattern matches it.
#
# A sysext has no such problem: the scanner detects it only by the end-anchored
# suffixes `.raw`, `.raw.zst`, `.raw.xz` and `.raw.gz`, which are exactly the
# four patterns phase 1 uses, so no sysext payload can be missed here.
echo "Phase 0 (classify): checking every file name against the payload patterns"
unclassified=()
while IFS= read -r -d '' file; do
  rel="${file#"$REPO_DIR"/}"
  base="${rel##*/}"

  if [[ $rel == ext/*/*.transfer ]]; then
    dir="${rel%/*}"
    name="${dir#ext/}"
    if [[ $name != */* && $base == "$name.transfer" ]]; then
      continue
    fi
  fi

  case "$base" in
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

# Sysext write guard: read the restore's ETags and plan phase 3b before any
# upload, so a tree the guard cannot write safely fails before phase 1.
guarded_existing=()
guarded_new=()
declare -A recorded_etag=()
declare -A recorded_sha=()
if [[ -n "$METADATA_ETAGS" ]]; then
  echo "Sysext write guard: planning conditional writes from $METADATA_ETAGS"
  if [[ ! -f "$METADATA_ETAGS" ]]; then
    echo "::error::METADATA_ETAGS does not exist: $METADATA_ETAGS (scripts/sync-sysext-metadata.sh writes it at the restore); nothing was uploaded"
    exit 1
  fi
  if [[ "$S3_PATH" != s3://?* ]]; then
    echo "::error::S3_PATH must start with s3:// for the sysext write guard, got '$S3_PATH'"
    exit 1
  fi
  s3_rest="${S3_PATH#s3://}"
  s3_rest="${s3_rest%/}"
  guard_bucket="${s3_rest%%/*}"
  guard_key_prefix=""
  if [[ "$s3_rest" == */* ]]; then
    guard_key_prefix="${s3_rest#*/}/"
  fi

  while IFS=$'\t' read -r rel etag sha extra; do
    [[ -n "$rel" ]] || continue
    if [[ -n "${extra:-}" || -z "${etag:-}" || ! "${sha:-}" =~ ^[0-9a-f]{64}$ ]]; then
      echo "::error::METADATA_ETAGS has a malformed line for '$rel'; nothing was uploaded"
      exit 1
    fi
    case "$rel" in
      ext/*/SHA256SUMS | ext/*/SHA256SUMS.gpg) ;;
      *)
        echo "::error::METADATA_ETAGS names '$rel', which is not a guarded sysext file; nothing was uploaded"
        exit 1
        ;;
    esac
    recorded_etag["$rel"]="$etag"
    recorded_sha["$rel"]="$sha"
  done <"$METADATA_ETAGS"

  guard_problems=0
  manifests=()
  while IFS= read -r -d '' file; do
    rel="${file#"$REPO_DIR"/}"
    name="${rel#ext/}"
    name="${name%/*}"
    if [[ "$name" == */* ]]; then
      echo "::error::$rel is not at ext/<name>/, where the sysext write guard expects every manifest and signature"
      guard_problems=$((guard_problems + 1))
      continue
    fi
    if [[ "$rel" == */SHA256SUMS ]]; then
      manifests+=("$rel")
    elif [[ ! -f "$REPO_DIR/${rel%.gpg}" ]]; then
      echo "::error::$rel has no SHA256SUMS beside it"
      guard_problems=$((guard_problems + 1))
    fi
  done < <(if [[ -d "$REPO_DIR/ext" ]]; then
    find "$REPO_DIR/ext" -type f \( -name SHA256SUMS -o -name SHA256SUMS.gpg \) -print0 | LC_ALL=C sort -z
  fi)

  for manifest in ${manifests[@]+"${manifests[@]}"}; do
    signature="$manifest.gpg"
    local_sha="$(sha256sum "$REPO_DIR/$manifest")"
    local_sha="${local_sha%% *}"
    has_signature=0
    [[ -f "$REPO_DIR/$signature" ]] && has_signature=1
    if [[ -n "${recorded_etag[$signature]:-}" ]] && ((!has_signature)); then
      echo "::error::$signature is published, but this run has no signature for the new $manifest; writing the manifest alone would leave a stale signature"
      guard_problems=$((guard_problems + 1))
      continue
    fi
    if [[ "${recorded_sha[$manifest]:-}" == "$local_sha" ]] &&
      { ((!has_signature)) || [[ -n "${recorded_etag[$signature]:-}" ]]; }; then
      # Unchanged manifest: the published pair stays, and the fresh signature
      # of the same bytes is not uploaded.
      continue
    fi
    if [[ -n "${recorded_etag[$manifest]:-}" ]]; then
      guarded_existing+=("$manifest")
    else
      guarded_new+=("$manifest")
    fi
  done

  if ((guard_problems > 0)); then
    echo "::error::Sysext write guard refused the tree; nothing was uploaded"
    exit 1
  fi
  echo "Sysext write guard: ${#guarded_existing[@]} changed and ${#guarded_new[@]} new manifest(s) to write conditionally"
fi

# remote_matches KEY FILE succeeds when the object KEY holds exactly the bytes
# of FILE.
remote_matches() {
  local key="$1" file="$2" copy
  copy="$(mktemp)"
  if aws s3api get-object \
    --bucket "$guard_bucket" \
    --key "$key" \
    --endpoint-url "$R2_ENDPOINT" \
    "$copy" </dev/null >/dev/null 2>&1 && cmp -s "$copy" "$file"; then
    rm -f "$copy"
    return 0
  fi
  rm -f "$copy"
  return 1
}

# put_guarded REL writes REPO_DIR/REL to the bucket only when the object is
# still the version the restore recorded (or still absent). It returns 0 on
# success, 2 when the condition failed (another publisher wrote the object),
# and 1 on any other failure. A failed attempt that is not a 412 is retried;
# a 412 after an attempt whose response was lost is recognized by comparing
# the object with the local bytes.
put_guarded() {
  local rel="$1"
  local key="${guard_key_prefix}${rel}"
  local etag="${recorded_etag[$rel]:-}"
  local condition=(--if-none-match '*')
  if [[ -n "$etag" ]]; then
    condition=(--if-match "$etag")
  fi

  local attempt wait error_output
  for ((attempt = 1; attempt <= UPLOAD_ATTEMPTS; attempt++)); do
    echo "Phase 3b (guarded metadata): $rel with ${condition[*]}, attempt $attempt of $UPLOAD_ATTEMPTS"
    if error_output="$(aws s3api put-object \
      --bucket "$guard_bucket" \
      --key "$key" \
      --body "$REPO_DIR/$rel" \
      --endpoint-url "$R2_ENDPOINT" \
      "${condition[@]}" </dev/null 2>&1 >/dev/null)"; then
      return 0
    fi
    if grep -q 'Unknown options' <<<"$error_output"; then
      echo "::error::this aws CLI does not support ${condition[0]} on put-object (AWS CLI v2.22 or newer is required); refusing to write $rel without its condition"
      echo "$error_output"
      return 1
    fi
    if grep -Eq 'PreconditionFailed|\(412\)' <<<"$error_output"; then
      if remote_matches "$key" "$REPO_DIR/$rel"; then
        echo "$rel already holds the bytes of this run (an earlier attempt wrote them)"
        return 0
      fi
      return 2
    fi
    if ((attempt < UPLOAD_ATTEMPTS)); then
      wait=$((UPLOAD_RETRY_DELAY * attempt))
      echo "::warning::Phase 3b (guarded metadata) could not write $rel on attempt $attempt of $UPLOAD_ATTEMPTS; retrying in ${wait}s"
      echo "$error_output"
      sleep "$wait"
    fi
  done
  echo "::error::Phase 3b (guarded metadata) could not write $rel after $UPLOAD_ATTEMPTS attempts"
  echo "$error_output"
  return 1
}

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

# Phase 3b: the guarded sysext manifests and signatures, existing extensions
# first, new ones last, each SHA256SUMS before its SHA256SUMS.gpg.
for manifest in ${guarded_existing[@]+"${guarded_existing[@]}"} ${guarded_new[@]+"${guarded_new[@]}"}; do
  status=0
  put_guarded "$manifest" || status=$?
  if ((status == 2)); then
    echo "::error::another publisher changed ${guard_key_prefix}${manifest} since this run restored it; no metadata of this run replaced it. The payloads this run uploaded are not listed by any manifest and are harmless. Rerun the job; sysext publishers of one bucket must be serialized (ADR-0019)"
    exit 1
  elif ((status != 0)); then
    echo "::error::Phase 3b (guarded metadata) failed on $manifest; no later manifest and no index was uploaded"
    exit 1
  fi
  signature="$manifest.gpg"
  [[ -f "$REPO_DIR/$signature" ]] || continue
  status=0
  put_guarded "$signature" || status=$?
  if ((status == 2)); then
    echo "::error::${guard_key_prefix}${manifest} was written, but another writer changed ${guard_key_prefix}${signature} since this run restored it, so this run did not replace it. The manifest and its signature can mismatch until one consistent pair is written; repogen and updex both refuse a mismatched pair. Serialize the sysext publishers of this bucket, then rerun the job (ADR-0019)"
    exit 1
  elif ((status != 0)); then
    echo "::error::Phase 3b (guarded metadata) wrote $manifest but failed on $signature; the pair can mismatch until a rerun writes both"
    exit 1
  fi
done

# Phase 4: directory indexes last.
if ! run_phase 4 "indexes" \
  aws s3 sync "$REPO_DIR" "$S3_PATH" \
  --endpoint-url "$R2_ENDPOINT" \
  "${index_only_filters[@]}"; then
  echo "::error::Phase 4 (indexes) failed after $UPLOAD_ATTEMPTS attempts"
  exit 1
fi

echo "Upload complete: payloads, then metadata, then indexes are in $S3_PATH"
