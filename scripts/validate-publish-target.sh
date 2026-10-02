#!/bin/sh
# Validate the publish target of the publish-to-r2 Action.
#
# Reads PACKAGE_TYPE, CODENAME, SUITE and PACKAGES_DIR from the environment. The signed
# legacy "stable" suite is frozen (frostyard/core ADR-0048, ADR-0054), so a
# codename or suite of "stable" (any case, surrounding whitespace ignored) is
# refused for every package type. Debian publication has no default codename:
# it must name an immutable codename such as "trixie". A codename or suite
# must be a plain name ([a-z0-9][a-z0-9.+~-]*), so path-equivalent spellings
# such as "stable/" or "./stable" cannot reach dists/stable. Because repogen
# generates every package type it detects, Debian packages (by .deb extension
# or ar/debian magic, symlinks included, as repogen's scanner follows them)
# in PACKAGES_DIR are refused whatever the declared type.
set -eu

fail() {
	echo "::error::$1"
	exit 1
}

check_name() {
	case "$2" in
	'') ;;
	*[!a-z0-9.+~-]* | [!a-z0-9]*)
		fail "$1 '$2' is not a plain name; use an immutable codename such as trixie" ;;
	esac
}

trim_lower() {
	printf '%s' "$1" | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]'
}

package_type=$(trim_lower "${PACKAGE_TYPE:-}")
codename=$(trim_lower "${CODENAME:-}")
suite=$(trim_lower "${SUITE:-}")

check_name codename "$codename"
check_name suite "$suite"

if [ "$codename" = "stable" ]; then
	echo "::error::codename 'stable' is refused: the signed stable suite is frozen (ADR-0048, ADR-0054); publish to an immutable codename such as trixie"
	exit 1
fi
if [ "$suite" = "stable" ]; then
	echo "::error::suite 'stable' is refused: the signed stable suite is frozen (ADR-0048, ADR-0054); publish to an immutable codename such as trixie"
	exit 1
fi
if [ "$package_type" = "deb" ] && [ -z "$codename" ]; then
	echo "::error::codename is required for package-type deb and has no default; name an immutable codename such as trixie (ADR-0048, ADR-0054)"
	exit 1
fi

packages_dir=${PACKAGES_DIR:-}
if [ -n "$packages_dir" ] && [ -d "$packages_dir" ]; then
	deb_magic=$(printf '!<arch>\ndebian')
	found=$(find "$packages_dir" \( -type f -o -type l \) -exec sh -c '
		magic=$1; shift
		for f do
			case "$f" in *.deb) printf "%s\n" "$f"; continue ;; esac
			[ "$(head -c 14 "$f" 2>/dev/null)" = "$magic" ] && printf "%s\n" "$f"
		done
		exit 0' sh "$deb_magic" {} +)
	if [ -n "$found" ]; then
		fail "Debian packages found in packages-dir; this legacy action never publishes Debian metadata (repogen would generate dists/ for them): $(printf '%s' "$found" | head -n 5 | tr '\n' ' ')"
	fi
fi

echo "✓ publish target: codename='${codename:-<none>}' suite='${suite:-<codename>}'"
