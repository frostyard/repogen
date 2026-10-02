#!/bin/sh
# Validate the inputs of the submit-intake Action before anything is
# installed or any secret is written to disk.
#
# Reads CONFIG_FILE, CONFIG_SHA256, POLICY_SHA256, REQUEST_FILE,
# PROVENANCE_FILE, ARTIFACTS_DIR, SUBMISSION_KEY, R2_ACCOUNT_ID,
# R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY, REPOGEN_VERSION, REPOGEN_COMMIT and
# GITHUB_REPOSITORY from the environment. The config must be a regular
# non-symlink file matching its pin, target exactly "trixie" (the signed
# legacy "stable" suite is frozen, frostyard/core ADR-0048, ADR-0054), name
# GITHUB_REPOSITORY as its producer, match the R2_ACCOUNT_ID secret, and keep
# the intake and coordination buckets apart from the publication bucket.
# Secret values are never printed.
set -eu

fail() {
	echo "::error::$1"
	exit 1
}

value=
for name in CONFIG_FILE CONFIG_SHA256 POLICY_SHA256 REQUEST_FILE \
	PROVENANCE_FILE ARTIFACTS_DIR SUBMISSION_KEY R2_ACCOUNT_ID \
	R2_ACCESS_KEY_ID R2_SECRET_ACCESS_KEY REPOGEN_VERSION REPOGEN_COMMIT \
	GITHUB_REPOSITORY; do
	eval "value=\${$name:-}"
	if [ -z "$(printf '%s' "$value" | tr -d '[:space:]')" ]; then
		fail "$name is required"
	fi
done

check_sha256() {
	case "$2" in
	*[!0-9a-f]*) fail "$1 is not a lowercase SHA-256 hex digest" ;;
	esac
	[ "${#2}" -eq 64 ] || fail "$1 is not a lowercase SHA-256 hex digest"
}

check_sha256 config-sha256 "$CONFIG_SHA256"
check_sha256 policy-sha256 "$POLICY_SHA256"

printf '%s' "$REPOGEN_VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
	fail "repogen-version must be an exact release tag such as v0.6.0"
[ "$(printf '%s' "$REPOGEN_VERSION" | wc -l)" -eq 0 ] ||
	fail "repogen-version must be an exact release tag such as v0.6.0"
case "$REPOGEN_COMMIT" in
*[!0-9a-f]*) fail "repogen-commit must be a full lowercase 40-hex commit" ;;
esac
[ "${#REPOGEN_COMMIT}" -eq 40 ] || fail "repogen-commit must be a full lowercase 40-hex commit"

case "$SUBMISSION_KEY" in
*[!A-Za-z0-9._-]* | . | ..) fail "submission-key may contain only letters, digits, '.', '_' and '-'" ;;
esac

regular() {
	if [ -L "$2" ] || [ ! -f "$2" ]; then
		fail "$1 must be a regular file, not a symlink"
	fi
}

regular config "$CONFIG_FILE"
regular request "$REQUEST_FILE"
regular provenance "$PROVENANCE_FILE"
if [ -L "$ARTIFACTS_DIR" ] || [ ! -d "$ARTIFACTS_DIR" ]; then
	fail "artifacts-dir must be a directory, not a symlink"
fi
actual=$(sha256sum "$CONFIG_FILE" | cut -d' ' -f1)
[ "$actual" = "$CONFIG_SHA256" ] || fail "config digest does not match its explicit pin"

field() {
	jq -er --arg key "$2" '.[$key] | strings' "$1" 2>/dev/null ||
		fail "$3 has no string field $2"
}

target=$(field "$CONFIG_FILE" target config)
normalised=$(printf '%s' "$target" | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')
if [ "$normalised" = "stable" ]; then
	fail "config target 'stable' is refused: the signed stable suite is frozen (ADR-0048, ADR-0054)"
fi
[ "$target" = "trixie" ] || fail "config target must be exactly trixie"

[ "$(field "$CONFIG_FILE" producer config)" = "$GITHUB_REPOSITORY" ] ||
	fail "config producer does not match GITHUB_REPOSITORY"
[ "$(field "$CONFIG_FILE" account_id config)" = "$R2_ACCOUNT_ID" ] ||
	fail "config account_id does not match the r2-account-id input"
[ "$(field "$CONFIG_FILE" endpoint config)" = "https://$R2_ACCOUNT_ID.r2.cloudflarestorage.com" ] ||
	fail "config endpoint is not the R2 endpoint of the r2-account-id input"

# The publication bucket is fixed here, not trusted from the config, so a
# decoy publication_bucket cannot point intake writes at the real one.
publication=frostyardrepo
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }
[ "$(field "$CONFIG_FILE" publication_bucket config)" = "$publication" ] ||
	fail "config publication_bucket must be $publication"
intake_bucket=$(field "$CONFIG_FILE" intake_bucket config)
coordination_bucket=$(field "$CONFIG_FILE" coordination_bucket config)
[ "$(lower "$intake_bucket")" != "$publication" ] ||
	fail "config intake_bucket must differ from the publication bucket"
[ "$(lower "$coordination_bucket")" != "$publication" ] ||
	fail "config coordination_bucket must differ from the publication bucket"
[ "$(lower "$intake_bucket")" != "$(lower "$coordination_bucket")" ] ||
	fail "config intake_bucket and coordination_bucket must differ"

echo "✓ submit inputs: target=trixie producer=$GITHUB_REPOSITORY repogen=$REPOGEN_VERSION config=$CONFIG_SHA256"
