#!/usr/bin/env bash
set -euo pipefail

normalize_arch() {
  case "$1" in
    amd64|x86_64) printf '%s\n' amd64 ;;
    arm64|aarch64) printf '%s\n' arm64 ;;
    *) echo "unsupported release architecture: $1" >&2; return 1 ;;
  esac
}

validate_tag() {
  [[ "$1" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$ ]] || {
    echo "repogen version must be an exact v-prefixed release tag" >&2
    return 1
  }
}

asset_name() {
  validate_tag "$1"
  local arch
  arch=$(normalize_arch "$2")
  printf 'repogen-linux-%s\n' "$arch"
}

if [[ "${1:-}" == "--asset-name" ]]; then
  [[ "$#" -eq 3 ]] || {
    echo "usage: $0 --asset-name <vMAJOR.MINOR.PATCH> <architecture>" >&2
    exit 2
  }
  asset_name "$2" "$3"
  exit 0
fi

[[ "$#" -eq 4 ]] || {
  echo "usage: $0 <vMAJOR.MINOR.PATCH> <40-char-commit> <architecture> <destination>" >&2
  exit 2
}

tag="$1"
expected_commit="$2"
arch=$(normalize_arch "$3")
destination="$4"
validate_tag "$tag"
[[ "$expected_commit" =~ ^[0-9a-f]{40}$ ]] || {
  echo "repogen commit must be an exact 40-character lowercase Git commit" >&2
  exit 1
}

asset=$(asset_name "$tag" "$arch")
checksum_asset=SHA256SUMS
base_url="${REPOGEN_RELEASE_BASE_URL:-https://github.com/frostyard/repogen/releases/download}"
if [[ "$base_url" != https://* && "${REPOGEN_ALLOW_FILE_FIXTURE:-}" != "1" ]]; then
  echo "release base URL must use HTTPS" >&2
  exit 1
fi

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
curl_args=(--fail --silent --show-error --location)
curl "${curl_args[@]}" --output "$tmp_dir/$asset" "$base_url/$tag/$asset"
curl "${curl_args[@]}" --output "$tmp_dir/$checksum_asset" "$base_url/$tag/$checksum_asset"

checksum_pattern="^[0-9a-f]{64}  ${asset}$"
match_count=$(grep -Ec "$checksum_pattern" "$tmp_dir/$checksum_asset" || true)
[[ "$match_count" == "1" ]] || {
  echo "checksum file must contain exactly one entry for $asset" >&2
  exit 1
}
expected_checksum=$(grep -E "$checksum_pattern" "$tmp_dir/$checksum_asset" | cut -d' ' -f1)
(
  cd "$tmp_dir"
  printf '%s  %s\n' "$expected_checksum" "$asset" | sha256sum --check --status -
) || {
  echo "SHA-256 verification failed for $asset" >&2
  exit 1
}

chmod 0755 "$tmp_dir/$asset"
identity=$("$tmp_dir/$asset" version --short)
read -r actual_version actual_commit trailing <<<"$identity"
expected_version="${tag#v}"
[[ -z "${trailing:-}" && "$actual_version" == "$expected_version" && "$actual_commit" == "$expected_commit" ]] || {
  echo "embedded identity mismatch for $asset" >&2
  exit 1
}

install -m 0755 "$tmp_dir/$asset" "$destination"
printf 'installed %s %s from %s\n' "$actual_version" "$actual_commit" "$asset"
