# 0001 — Unsigned Debian repositories still emit InRelease

- **Status:** Accepted
- **Date:** 2026-08-12

## Context

Modern apt (notably Debian Trixie and later) fetches `InRelease` first and
treats a repository without one as suspect even when the sources.list entry
carries `[trusted=yes]`. Repogen supports running without any signing key —
`--gpg-key` is optional — and an unsigned repository that simply omits
`InRelease` breaks on those clients. The OpenPGP cleartext format normally
makes `InRelease` a *signed* wrapper around the `Release` content, so there is
no standard-conformant unsigned `InRelease`.

## Decision

When no GPG signer is configured, the Debian generator writes an `InRelease`
file containing the raw, unsigned `Release` content — no cleartext armor, no
signature — alongside the normal `Release` file
(`generateRelease` in [internal/generator/deb/generator.go](../../internal/generator/deb/generator.go)).
When a signer is configured, `InRelease` is a proper cleartext signature and
`Release.gpg` a detached one; the unsigned copy is emitted only in the
no-signer branch.

This is deliberately non-conformant: an `InRelease` without an OpenPGP
cleartext wrapper is not valid per the Debian repository format, but apt with
`[trusted=yes]` accepts it, and its absence is worse than its presence.

## Consequences

- Unsigned repogen repositories work with modern apt using `[trusted=yes]`,
  without requiring consumers to tolerate a missing `InRelease`.
- The file is intentionally malformed by the letter of the spec; tools that
  strictly validate OpenPGP cleartext framing will reject it. That is accepted
  — such tools would reject an unsigned repo anyway.
- `Release` and `InRelease` must stay byte-identical in the unsigned case;
  any future change to Release generation must keep writing both.
- Signed repositories are unaffected (they get conformant `InRelease` +
  `Release.gpg`).

## Alternatives considered

- **Omit `InRelease` when unsigned:** the previous behavior; broke apt on
  Debian Trixie even with `[trusted=yes]`.
- **Self-sign with a throwaway generated key:** would produce a conformant
  file but a meaningless trust anchor, and would force consumers to import a
  junk key or still use `[trusted=yes]`; adds a gpg dependency to the
  unsigned path for no security gain.

## References

- Shapes: [specs/generators.md — Debian/APT](../specs/generators.md#debianapt-internalgeneratordeb),
  [design/overview.md — Signing Strategy](../design/overview.md#signing-strategy)
- Implementation: [internal/generator/deb/generator.go](../../internal/generator/deb/generator.go) (`generateRelease`)
- Related: [ADR-0009 — GPG signing split](0009-gpg-cli-and-go-crypto-signing-split.md)
  covers the signed branch of the same function
