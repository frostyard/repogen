package sysext

// compareVersions orders two sysext version strings the way systemd-sysupdate
// orders them: it is a port of systemd's strverscmp_improved()
// (src/fundamental/string-util-fundamental.c), the comparison the UAPI
// Version Format Specification defines. It returns a negative number when a is
// older than b, a positive number when a is newer, and 0 when both are
// equivalent (for example "1.02" and "1.2").
//
// Retention (Generator.SysextKeepVersions) must keep the versions a client
// would pick as newest, so it uses the client's order, not a string order: as
// strings "9" sorts after "10".
//
// An empty string has no shortcut: it runs through the loop like upstream, so
// "" is newer than "~1" (a pre-release) and older than "1".
func compareVersions(a, b string) int {
	for {
		// Drop leading characters that are not valid version characters
		// (for example '+' or '_'): they only separate segments.
		for a != "" && !isValidVersionChar(a[0]) {
			a = a[1:]
		}
		for b != "" && !isValidVersionChar(b[0]) {
			b = b[1:]
		}

		// '~' marks a pre-release: the side with '~' is older.
		if hasPrefixByte(a, '~') || hasPrefixByte(b, '~') {
			if r := compareBools(!hasPrefixByte(a, '~'), !hasPrefixByte(b, '~')); r != 0 {
				return r
			}
			a, b = a[1:], b[1:]
		}

		// At the end of either string, the longer one is newer.
		if a == "" || b == "" {
			return compareStrings(a, b)
		}

		// '-' separates the version from the release: the side with '-' is
		// older (123-9 is older than 123.1-1).
		if a[0] == '-' || b[0] == '-' {
			if r := compareBools(a[0] != '-', b[0] != '-'); r != 0 {
				return r
			}
			a, b = a[1:], b[1:]
		}

		// '^' marks a patched release.
		if hasPrefixByte(a, '^') || hasPrefixByte(b, '^') {
			if r := compareBools(!hasPrefixByte(a, '^'), !hasPrefixByte(b, '^')); r != 0 {
				return r
			}
			a, b = a[1:], b[1:]
		}

		// '.' marks a point release.
		if hasPrefixByte(a, '.') || hasPrefixByte(b, '.') {
			if r := compareBools(!hasPrefixByte(a, '.'), !hasPrefixByte(b, '.')); r != 0 {
				return r
			}
			a, b = a[1:], b[1:]
		}

		var aEnd, bEnd int
		if (a != "" && isDigit(a[0])) || (b != "" && isDigit(b[0])) {
			// Numeric segments: a numeric segment is newer than an empty one.
			aEnd = spanOf(a, isDigit)
			bEnd = spanOf(b, isDigit)
			if r := compareBools(aEnd != 0, bEnd != 0); r != 0 {
				return r
			}
			aNum := trimLeadingZeros(a[:aEnd])
			bNum := trimLeadingZeros(b[:bEnd])
			// A longer number (without leading zeros) is larger; numbers of
			// one length compare as strings.
			if r := compareInts(len(aNum), len(bNum)); r != 0 {
				return r
			}
			if r := compareStrings(aNum, bNum); r != 0 {
				return r
			}
		} else {
			// Alphabetic segments: compare the common prefix, then the
			// longer segment is newer.
			aEnd = spanOf(a, isAlpha)
			bEnd = spanOf(b, isAlpha)
			common := min(aEnd, bEnd)
			if r := compareStrings(a[:common], b[:common]); r != 0 {
				return r
			}
			if r := compareInts(aEnd, bEnd); r != 0 {
				return r
			}
		}

		a, b = a[aEnd:], b[bEnd:]
	}
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

func isValidVersionChar(c byte) bool {
	return isDigit(c) || isAlpha(c) || c == '~' || c == '-' || c == '^' || c == '.'
}

func hasPrefixByte(s string, c byte) bool { return s != "" && s[0] == c }

func spanOf(s string, accept func(byte) bool) int {
	n := 0
	for n < len(s) && accept(s[n]) {
		n++
	}
	return n
}

func trimLeadingZeros(s string) string {
	for s != "" && s[0] == '0' {
		s = s[1:]
	}
	return s
}

func compareInts(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func compareBools(a, b bool) int {
	return compareInts(boolToInt(a), boolToInt(b))
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
