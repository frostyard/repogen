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
# It makes one HEAD request per skipped identity and no listing call: the key is
# a request parameter, so no response decoding can confuse two keys.

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

skipped_count=0
missing_count=0
while IFS= read -r line; do
  line="${line%$'\r'}"
  [[ -n "$line" ]] || continue
  skipped_count=$((skipped_count + 1))
  key="${PREFIX:+$PREFIX/}$line"
  # A non-zero exit means the object is missing or unreadable. Every other key
  # is still checked, so one run reports every broken identity. stdin is
  # /dev/null so the command cannot consume the report this loop reads.
  if ! aws s3api head-object \
    --bucket "$BUCKET" \
    --key "$key" \
    --endpoint-url "$R2_ENDPOINT" </dev/null >/dev/null; then
    echo "::error::$key is listed in the signature-verified SHA256SUMS but its payload object is missing or unreadable; a rebuild cannot republish that identity; bump the version or revision, or repair the bucket"
    missing_count=$((missing_count + 1))
  fi
done < "$SKIPPED_REPORT"

if (( missing_count > 0 )); then
  echo "::error::$missing_count of $skipped_count skipped identities have no readable payload object in s3://$BUCKET/${PREFIX:+$PREFIX/}"
  exit 1
fi

echo "$skipped_count skipped identities have their payloads"
exit 0
