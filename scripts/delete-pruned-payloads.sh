#!/usr/bin/env bash
# Delete the sysext payload objects that retention dropped from SHA256SUMS
# (`repogen generate --sysext-keep-versions N --pruned-report FILE`, ADR-0019).
#
# The rule (ADR-0017): live metadata must never list a payload that is not in
# the bucket. So the publish-to-r2 action runs this script only after the
# upload (payloads, then the guarded metadata, then the indexes) and the cache
# purge succeeded, and this script checks it again for each object: it
# downloads the live ext/<name>/SHA256SUMS and deletes a pruned payload only
# when that live manifest does not list it. When the metadata write failed (a
# 412, an error), the live manifest still lists the pruned files, and nothing
# is deleted.
#
# Inputs (environment):
#   PRUNED_REPORT  required, the --pruned-report file: one ext/<name>/<file>
#                  line per pruned payload
#   S3_PATH        required, e.g. s3://bucket/prefix
#   R2_ENDPOINT    required, e.g. https://<account>.r2.cloudflarestorage.com
#   REPO_DIR       optional, default ./repo; its ext/<name>/SHA256SUMS must not
#                  list a pruned file either
#
# A payload this script cannot delete, or refuses to delete, is reported with
# `::error::` and the script exits non-zero after it tried every line. Such an
# object is not listed by the live manifest (or is still listed and so must
# stay); it costs storage, nothing else.

set -euo pipefail

PRUNED_REPORT="${PRUNED_REPORT:-}"
S3_PATH="${S3_PATH:-}"
R2_ENDPOINT="${R2_ENDPOINT:-}"
REPO_DIR="${REPO_DIR:-./repo}"

if [[ -z "$PRUNED_REPORT" ]]; then
  echo "::error::PRUNED_REPORT is required (the --pruned-report file)"
  exit 1
fi
if [[ -z "$S3_PATH" || "$S3_PATH" != s3://?* ]]; then
  echo "::error::S3_PATH is required and must start with s3:// (for example s3://bucket/prefix)"
  exit 1
fi
if [[ -z "$R2_ENDPOINT" ]]; then
  echo "::error::R2_ENDPOINT is required (for example https://<account>.r2.cloudflarestorage.com)"
  exit 1
fi
if [[ ! -f "$PRUNED_REPORT" ]]; then
  echo "::error::pruned report does not exist: $PRUNED_REPORT"
  exit 1
fi
if [[ ! -s "$PRUNED_REPORT" ]]; then
  echo "no pruned sysext payloads"
  exit 0
fi

s3_rest="${S3_PATH#s3://}"
s3_rest="${s3_rest%/}"
bucket="${s3_rest%%/*}"
key_prefix=""
if [[ "$s3_rest" == */* ]]; then
  key_prefix="${s3_rest#*/}/"
fi

payload_re='^ext/([^/[:space:]]+)/([^/[:space:]]+\.raw(\.zst|\.xz|\.gz)?)$'

# Validate every line before any delete.
names=()
lines=()
declare -A seen_name=()
while IFS= read -r line; do
  line="${line%$'\r'}"
  [[ -n "$line" ]] || continue
  if [[ ! "$line" =~ $payload_re ]] || [[ "${BASH_REMATCH[1]}" == "." || "${BASH_REMATCH[1]}" == ".." ]]; then
    echo "::error::pruned report line '$line' is not ext/<name>/<sysext payload>; nothing was deleted"
    exit 1
  fi
  lines+=("$line")
  if [[ -z "${seen_name[${BASH_REMATCH[1]}]:-}" ]]; then
    seen_name["${BASH_REMATCH[1]}"]=1
    names+=("${BASH_REMATCH[1]}")
  fi
done <"$PRUNED_REPORT"

# lists MANIFEST FILE succeeds when the SHA256SUMS file MANIFEST lists FILE.
lists() {
  PRUNED_FILE="$2" awk '$2 == ENVIRON["PRUNED_FILE"] { found = 1 } END { exit !found }' "$1"
}

live="$(mktemp)"
trap 'rm -f "$live"' EXIT

failed=0
deleted=0
for name in "${names[@]}"; do
  manifest_key="${key_prefix}ext/$name/SHA256SUMS"
  local_manifest="$REPO_DIR/ext/$name/SHA256SUMS"
  manifest_ok=1
  if ! aws s3api get-object \
    --bucket "$bucket" \
    --key "$manifest_key" \
    --endpoint-url "$R2_ENDPOINT" \
    "$live" </dev/null >/dev/null; then
    echo "::error::cannot read the live $manifest_key; no pruned payload of $name was deleted"
    manifest_ok=0
  elif [[ ! -f "$local_manifest" ]]; then
    echo "::error::$local_manifest does not exist; no pruned payload of $name was deleted"
    manifest_ok=0
  fi

  for line in "${lines[@]}"; do
    [[ "$line" == "ext/$name/"* ]] || continue
    file="${line##*/}"
    key="${key_prefix}${line}"
    if ((!manifest_ok)); then
      failed=$((failed + 1))
      continue
    fi
    if lists "$live" "$file" || lists "$local_manifest" "$file"; then
      echo "::error::$key is still listed by ext/$name/SHA256SUMS; it was not deleted"
      failed=$((failed + 1))
      continue
    fi
    if ! aws s3api delete-object \
      --bucket "$bucket" \
      --key "$key" \
      --endpoint-url "$R2_ENDPOINT" </dev/null >/dev/null; then
      echo "::error::could not delete $key; it is not listed by any manifest, so it is harmless, but nothing will delete it later"
      failed=$((failed + 1))
      continue
    fi
    echo "deleted pruned payload $key"
    deleted=$((deleted + 1))
  done
done

if ((failed > 0)); then
  echo "::error::$failed pruned sysext payload object(s) were not deleted from s3://$bucket/${key_prefix}"
  exit 1
fi
echo "deleted $deleted pruned sysext payload object(s)"
