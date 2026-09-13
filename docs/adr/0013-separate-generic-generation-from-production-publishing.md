# 0013 - Separate generic generation from production publishing

- **Status:** Proposed
- **Date:** 2026-09-12

## Context

Repogen is a general-purpose repository generator. Its current `generate`
command supports six package formats, arbitrary Debian codenames and Release
identity, unsigned output, and a forgiving incremental mode. Those behaviors
are useful for local generation and non-Frostyard callers.

Frostyard production APT publication has stricter accepted requirements from
[core ADR-0048](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md):
explicit immutable codenames, fixed Release identity, a frozen `stable`
namespace, mandatory signing, fail-closed restoration, one protected writer,
immutable shared-pool handling, manifests, and verified publication.

Making every generic invocation follow the production rules would break
existing local, unsigned, non-Debian, and sysext uses. Treating a collection
of ordinary flags as the production boundary would leave unsafe defaults and
fallbacks reachable by a credential-bearing caller.

## Decision

Keep generic repository generation independent of Frostyard production
publishing.

Add a structurally distinct production path that:

- accepts Debian input only;
- requires an explicit immutable codename and matching suite;
- rejects `stable`, non-`main` components, and caller-defined Origin/Label;
- distinguishes explicit initialize from strict signed reconcile;
- stages a complete target-scoped transaction before publication;
- treats shared-pool paths as immutable and verifies bytes by SHA-256;
- requires signing, by-hash indexes, manifests, ordered visibility, and
  remote read-back; and
- never grants Debian authority over sysext paths.

The implementation may use a dedicated command or an explicit production
mode. In either case, production validation must happen before mutation and
must not be inferred from credentials, environment names, or a combination of
generic flags.

Generic generation keeps its current package-format and unsigned-use
capabilities. Its output is not production-safe merely because its values
happen to match the production identity.

## Consequences

- Existing generic and sysext callers can migrate independently.
- Production tests can prove fail-before-write behavior without weakening
  generic compatibility.
- The production publisher owns restoration, object-store transactions, and
  read-back; these concerns do not leak into every generator.
- Some validation and configuration concepts will exist at both generic and
  production layers. The production layer must reject ambiguity rather than
  inherit permissive defaults.
- Documentation and CLI help must state which path is generic and which path
  carries the Frostyard production contract.
- This ADR does not implement the production path or authorize credentials,
  publication, migration, release, or any remote mutation.

## Alternatives considered

- **Make `generate` production-strict for every caller:** rejected because it
  breaks supported unsigned, non-Debian, local, and sysext generation.
- **Keep one permissive command and rely on workflow discipline:** rejected
  because unsafe defaults and restore fallbacks remain available inside the
  credential boundary.
- **Fork Repogen into a Frostyard-only publisher:** rejected because the
  generic generators remain useful and a production orchestration boundary
  is smaller than maintaining a divergent tool.
- **Regenerate every suite in one transaction:** rejected because one
  codename per transaction limits failure and serialization scope.

## References

- Shapes:
  [Repogen overview](../design/overview.md),
  [generator contract](../specs/generators.md), and
  [Plan 0001](../plans/0001-frostyard-production-publisher.md)
- Builds on:
  [ADR-0002](0002-shared-debian-pool-layout.md),
  [ADR-0003](0003-single-main-component.md), and
  [ADR-0011](0011-incremental-state-from-published-metadata.md)
- Governing organization decision:
  [core ADR-0048](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md)
