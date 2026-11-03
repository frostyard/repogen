#!/usr/bin/env bash
# Check that every sysext identity the run skipped as "already published" still
# has its payload object in the bucket.
#
# Why (ADR-0018): `repogen generate --skip-published-identities` skips an
# incoming sysext whose identity (name, version, OSVersion, arch) the
# signature-verified SHA256SUMS already lists, whatever its bytes. The skip
# works from metadata, so it cannot tell on its own whether the payload object
# that metadata names is still in the bucket. If it is gone, a rebuild cannot
# republish that identity (repogen will not upload those bytes again), and the
# live manifest would point at nothing. This check runs before any upload and
# turns that into a loud failure.
#
# Inputs (environment):
#   SKIPPED_REPORT     required, the --skipped-report file (one
#                      ext/NAME/FILE line per skipped identity)
#   BUCKET             required, the target bucket
#   PREFIX             optional, the key prefix (repo-prefix)
#   R2_ENDPOINT        required, e.g. https://<account>.r2.cloudflarestorage.com
#
# It makes exactly one listing call and no request per object.

set -euo pipefail

SKIPPED_REPORT="${SKIPPED_REPORT:-}"
BUCKET="${BUCKET:-}"
PREFIX="${PREFIX:-}"
R2_ENDPOINT="${R2_ENDPOINT:-}"

if [[ -z "$SKIPPED_REPORT" ]]; then
  echo "::error::SKIPPED_REPORT is required (the --skipped-report file)"
  exit 1
fi
if [[ -z "$BUCKET" ]]; then
  echo "::error::BUCKET is required"
  exit 1
fi
if [[ -z "$R2_ENDPOINT" ]]; then
  echo "::error::R2_ENDPOINT is required (for example https://<account>.r2.cloudflarestorage.com)"
  exit 1
fi

if [[ ! -f "$SKIPPED_REPORT" ]]; then
  echo "::error::skipped report does not exist: $SKIPPED_REPORT"
  exit 1
fi

if [[ ! -s "$SKIPPED_REPORT" ]]; then
  echo "no skipped identities"
  exit 0
fi

# Payload keys live under <prefix>ext/.
KEY_PREFIX="${PREFIX:+$PREFIX/}ext/"

# One listing call for the whole report. `--output text` prints one key per
# line; an empty result prints `None`.
listing=$(aws s3api list-objects-v2 \
  --bucket "$BUCKET" \
  --prefix "$KEY_PREFIX" \
  --endpoint-url "$R2_ENDPOINT" \
  --query 'Contents[].[Key]' \
  --output text) || {
  echo "::error::could not list s3://$BUCKET/$KEY_PREFIX with $R2_ENDPOINT"
  exit 1
}

# The AWS CLI decodes keys with unquote_plus, so a `+` in an object key comes
# back as a space. Compare both sides with every `+` read as a space; payload
# names never contain spaces.
listing_with_spaces="${listing//+/ }"
declare -A present=()
if [[ "$listing_with_spaces" != "None" ]]; then
  while IFS= read -r key; do
    key="${key%$'\r'}"
    [[ -n "$key" ]] || continue
    present["$key"]=1
  done <<< "$listing_with_spaces"
fi

skipped_count=0
missing_count=0
while IFS= read -r line; do
  line="${line%$'\r'}"
  [[ -n "$line" ]] || continue
  skipped_count=$((skipped_count + 1))
  key="${PREFIX:+$PREFIX/}$line"
  if [[ -z "${present[${key//+/ }]:-}" ]]; then
    echo "::error::$key is listed in the signature-verified SHA256SUMS but its payload object is missing; a rebuild cannot republish that identity; bump the version or revision, or repair the bucket"
    missing_count=$((missing_count + 1))
  fi
done < "$SKIPPED_REPORT"

if (( missing_count > 0 )); then
  echo "::error::$missing_count of $skipped_count skipped identities have no payload object in s3://$BUCKET/$KEY_PREFIX"
  exit 1
fi

echo "$skipped_count skipped identities have their payloads"
exit 0
