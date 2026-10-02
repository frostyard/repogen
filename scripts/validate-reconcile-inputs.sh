#!/bin/sh
# Validate the inputs of the reconcile-production Action before anything is
# installed or any secret is written to disk.
#
# Reads CONFIG_FILE, CONFIG_SHA256, POLICY_FILE, POLICY_SHA256, REQUEST_SHA256,
# PROVENANCE_SHA256, R2_ACCOUNT_ID, R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY,
# SIGNING_KEY, REPOGEN_VERSION and REPOGEN_COMMIT from the environment. Pins
# must already be lowercase 64-hex SHA-256 values: nothing is trimmed or
# normalised. Config and policy must be regular non-symlink files whose bytes
# match their pins, both must target exactly "trixie" (the signed legacy
# "stable" suite is frozen, frostyard/core ADR-0048, ADR-0054), both must bind
# REQUEST_SHA256, and the config account and endpoint must match the
# R2_ACCOUNT_ID secret. The repogen binary is named by an exact release tag and
# full commit. Secret values are never printed.
set -eu

fail() {
	echo "::error::$1"
	exit 1
}

value=
for name in CONFIG_FILE CONFIG_SHA256 POLICY_FILE POLICY_SHA256 \
	REQUEST_SHA256 PROVENANCE_SHA256 R2_ACCOUNT_ID R2_ACCESS_KEY_ID \
	R2_SECRET_ACCESS_KEY SIGNING_KEY REPOGEN_VERSION REPOGEN_COMMIT; do
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
check_sha256 request-sha256 "$REQUEST_SHA256"
check_sha256 provenance-sha256 "$PROVENANCE_SHA256"

printf '%s' "$REPOGEN_VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$' ||
	fail "repogen-version must be an exact release tag such as v0.6.0"
[ "$(printf '%s' "$REPOGEN_VERSION" | wc -l)" -eq 0 ] ||
	fail "repogen-version must be an exact release tag such as v0.6.0"
case "$REPOGEN_COMMIT" in
*[!0-9a-f]*) fail "repogen-commit must be a full lowercase 40-hex commit" ;;
esac
[ "${#REPOGEN_COMMIT}" -eq 40 ] || fail "repogen-commit must be a full lowercase 40-hex commit"

check_file() {
	if [ -L "$2" ] || [ ! -f "$2" ]; then
		fail "$1 must be a regular file, not a symlink"
	fi
	actual=$(sha256sum "$2" | cut -d' ' -f1)
	[ "$actual" = "$3" ] || fail "$1 digest does not match its explicit pin"
}

check_file config "$CONFIG_FILE" "$CONFIG_SHA256"
check_file policy "$POLICY_FILE" "$POLICY_SHA256"

field() {
	jq -er --arg key "$2" '.[$key] | strings' "$1" 2>/dev/null ||
		fail "$3 has no string field $2"
}

check_target() {
	normalised=$(printf '%s' "$2" | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')
	if [ "$normalised" = "stable" ]; then
		fail "$1 target 'stable' is refused: the signed stable suite is frozen (ADR-0048, ADR-0054)"
	fi
	[ "$2" = "trixie" ] || fail "$1 target must be exactly trixie"
}

config_target=$(field "$CONFIG_FILE" target config)
policy_target=$(field "$POLICY_FILE" target policy)
check_target config "$config_target"
check_target policy "$policy_target"

[ "$(field "$CONFIG_FILE" request_sha256 config)" = "$REQUEST_SHA256" ] ||
	fail "config request_sha256 does not match request-sha256"
[ "$(field "$POLICY_FILE" request_sha256 policy)" = "$REQUEST_SHA256" ] ||
	fail "policy request_sha256 does not match request-sha256"

# The binary embeds the tag without its "v" and the full release commit;
# reconcile-production compares them literally with the policy.
[ "$(field "$POLICY_FILE" repogen_version policy)" = "${REPOGEN_VERSION#v}" ] ||
	fail "policy repogen_version must be the repogen-version tag without its v prefix"
[ "$(field "$POLICY_FILE" action_commit policy)" = "$REPOGEN_COMMIT" ] ||
	fail "policy action_commit does not match repogen-commit"

[ "$(field "$CONFIG_FILE" account_id config)" = "$R2_ACCOUNT_ID" ] ||
	fail "config account_id does not match the r2-account-id input"
[ "$(field "$CONFIG_FILE" endpoint config)" = "https://$R2_ACCOUNT_ID.r2.cloudflarestorage.com" ] ||
	fail "config endpoint is not the R2 endpoint of the r2-account-id input"

echo "✓ reconcile inputs: target=trixie repogen=$REPOGEN_VERSION config=$CONFIG_SHA256 policy=$POLICY_SHA256"
