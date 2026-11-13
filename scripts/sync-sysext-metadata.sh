#!/usr/bin/env bash
# Restore the published sysext metadata from an S3-compatible bucket
# (Cloudflare R2) into REPO_DIR, and record the ETag of each manifest and
# signature it restored, so the upload can write them back conditionally.
#
# Why (ADR-0019, frostyard/repogen#102): several publishers (for example the
# trixie and forky builds of snosi) write the same ext/<name>/SHA256SUMS. Each
# run restores the manifest, merges its new entries and writes it back. Without
# a condition, a run that writes later silently drops the entries of a run that
# wrote in between (a lost update). R2 evaluates If-Match and If-None-Match on
# PutObject atomically, so scripts/upload-to-r2.sh writes each guarded file
# only when the object is still the version this script restored.
#
# The ETag must belong to exactly the bytes the generator merges. So the
# guarded files are not restored by `aws s3 sync`: this script lists them and
# downloads each with `aws s3api get-object`, which returns the ETag of the
# bytes it wrote. An ETag read after a download (a separate HEAD) could belong
# to a newer object and would hide exactly the race this guards against.
#
# Guarded files: ext/<name>/SHA256SUMS and ext/<name>/SHA256SUMS.gpg. Every
# other non-payload file under ext/ (the transfer files, ext/index, the HTML
# indexes) is restored with `aws s3 sync`, as before.
#
# Inputs (environment):
#   S3_PATH          required, e.g. s3://bucket/prefix
#   R2_ENDPOINT      required, e.g. https://<account>.r2.cloudflarestorage.com
#   METADATA_ETAGS   required, the state file this script writes: one line per
#                    guarded object, `ext/<name>/<file><TAB><etag><TAB><sha256>`
#                    (the SHA-256 of the restored bytes); a guarded object
#                    that is not listed did not exist at the restore
#   REPO_DIR         optional, default ./repo

set -euo pipefail

S3_PATH="${S3_PATH:-}"
R2_ENDPOINT="${R2_ENDPOINT:-}"
METADATA_ETAGS="${METADATA_ETAGS:-}"
REPO_DIR="${REPO_DIR:-./repo}"

if [[ -z "$S3_PATH" || "$S3_PATH" != s3://?* ]]; then
  echo "::error::S3_PATH is required and must start with s3:// (for example s3://bucket/prefix)"
  exit 1
fi
if [[ -z "$R2_ENDPOINT" ]]; then
  echo "::error::R2_ENDPOINT is required (for example https://<account>.r2.cloudflarestorage.com)"
  exit 1
fi
if [[ -z "$METADATA_ETAGS" ]]; then
  echo "::error::METADATA_ETAGS is required (the ETag state file for scripts/upload-to-r2.sh)"
  exit 1
fi

s3_rest="${S3_PATH#s3://}"
s3_rest="${s3_rest%/}"
bucket="${s3_rest%%/*}"
key_prefix=""
if [[ "$s3_rest" == */* ]]; then
  key_prefix="${s3_rest#*/}/"
fi

mkdir -p "$REPO_DIR/ext"

# The state file is written beside its final path and renamed at the end, so a
# failed restore never leaves a partial file that a later upload could trust.
# An earlier state file is removed first for the same reason.
rm -f "$METADATA_ETAGS"
state_tmp="$(mktemp "${METADATA_ETAGS}.XXXXXX")"
trap 'rm -f "$state_tmp"' EXIT

echo "Restoring sysext metadata from ${S3_PATH}/ext (payloads excluded)"
aws s3 sync "${S3_PATH}/ext" "$REPO_DIR/ext" \
  --endpoint-url "$R2_ENDPOINT" \
  --exclude '*.raw' \
  --exclude '*.raw.zst' \
  --exclude '*.raw.xz' \
  --exclude '*.raw.gz' \
  --exclude '*/SHA256SUMS' \
  --exclude '*/SHA256SUMS.gpg'

echo "Listing the guarded manifests and signatures under ${key_prefix}ext/"
listing="$(aws s3api list-objects-v2 \
  --bucket "$bucket" \
  --prefix "${key_prefix}ext/" \
  --endpoint-url "$R2_ENDPOINT" \
  --query 'Contents[].[Key]' \
  --output text)"

guarded=()
while IFS= read -r key; do
  key="${key%$'\r'}"
  [[ -n "$key" && "$key" != "None" ]] || continue
  rel="${key#"$key_prefix"}"
  case "$rel" in
    ext/*/SHA256SUMS | ext/*/SHA256SUMS.gpg) ;;
    *) continue ;;
  esac
  name="${rel#ext/}"
  name="${name%/*}"
  # Only ext/<name>/ holds a manifest; a deeper key is not repogen's.
  [[ "$name" != */* && "$name" != "." && "$name" != ".." ]] || continue
  guarded+=("$rel")
done <<<"$listing"

restored=0
for rel in ${guarded[@]+"${guarded[@]}"}; do
  key="${key_prefix}${rel}"
  mkdir -p "$(dirname "$REPO_DIR/$rel")"
  # get-object prints the ETag of the bytes it wrote to the file.
  if ! etag="$(aws s3api get-object \
    --bucket "$bucket" \
    --key "$key" \
    --endpoint-url "$R2_ENDPOINT" \
    --query ETag \
    --output text \
    "$REPO_DIR/$rel" </dev/null)"; then
    echo "::error::could not download $key with its ETag; rerun the job"
    exit 1
  fi
  etag="${etag%$'\r'}"
  if [[ -z "$etag" || "$etag" == "None" || "$etag" == *$'\t'* || "$etag" == *$'\n'* ]]; then
    echo "::error::get-object returned no usable ETag for $key; refusing to publish without a write guard"
    exit 1
  fi
  sha="$(sha256sum "$REPO_DIR/$rel")"
  sha="${sha%% *}"
  printf '%s\t%s\t%s\n' "$rel" "$etag" "$sha" >>"$state_tmp"
  restored=$((restored + 1))
done

mv "$state_tmp" "$METADATA_ETAGS"
trap - EXIT

echo "Restored $restored guarded sysext metadata file(s) with their ETags"
