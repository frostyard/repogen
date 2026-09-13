# Spec: R10 release acceptance

This contract governs the local evidence and external observations required
before a Repogen release can satisfy R10. It composes the R1-R9 implementation
without granting permission to merge, tag, publish, change credentials, or
mutate a production repository.

## Interface

R10 has three distinct artifacts:

| Artifact | Required identity | Evidence |
| --- | --- | --- |
| Candidate | One clean Repogen commit containing the reviewed R1-R9 lineage | Exact commit and tree, full repository gate, signed two-suite fixture |
| Merge | The exact independently reviewed candidate on the repository's default branch | Human-recorded merge commit whose tree equals the candidate tree |
| Release | One semantic-version tag resolving to the merged commit | Human-recorded release URL, workflow run, assets, checksums, and installed identities |

The release asset set MUST contain exactly:

```text
repogen-linux-amd64
repogen-linux-arm64
SHA256SUMS
```

`SHA256SUMS` MUST contain exactly one lowercase SHA-256 entry for each binary
and no entry for itself. Each architecture MUST be installed through
[`scripts/install-release.sh`](../../scripts/install-release.sh) on a matching
architecture. `repogen version --short` MUST report the tag version without
the leading `v` and the exact 40-character tag commit.

## Rules

- The candidate MUST descend from the exact independently reviewed R2-R9
  inputs. Reimplementing or silently dropping a predecessor is not accepted.
- The full local gate is:

  ```bash
  make build
  make fmt
  git diff --exit-code
  make lint
  go test -v -short -race -coverprofile=coverage.txt ./...
  make test-packages-docker
  make test-integration
  node scripts/check-docs.mjs
  ```

- `TestR10SignedTwoSuitePublisherAcceptance` MUST publish signed `trixie` and
  `forky` fixture suites through the production transaction, verify each with
  real `gpgv` and apt, reuse one identical shared-pool object without
  overwrite, retain exact suite identity, omit `Valid-Until`, require
  by-hash, and preserve the frozen `stable` fixture.
- The complete test suite remains the R1-R9 matrix: strict validation and
  restore, immutable shared-pool collision handling, signed ordered
  publication, deterministic no-op generation, atomic and durable local
  commit, retained intake/recovery, and OSVersion-aware serialized sysext
  reconciliation.
- `.github/workflows/release.yml` MUST remain the only tag-triggered release
  publisher. It MUST run commit-pinned actions and exact GoReleaser `v2.18.1`
  against the tag commit.
- A successful local snapshot, merge, tag, or workflow is not release
  acceptance. The published assets, checksums, embedded identities, and
  production installer path MUST all match this contract.
- Downstreams MUST remain on their prior verified version until the separate
  published-release evidence passes. They MUST NOT consume a candidate or
  merge commit as if it were a release.
- A visible incorrect tag or release MUST NOT be moved or replaced. Preserve
  it and correct forward with a new reviewed version.
- Closing frostyard/repogen#34 is useful bookkeeping but is neither a
  substitute for the exact reviewed merge nor proof of the separately
  published release.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| Raw Linux binaries | GoReleaser builds the reviewed tag commit for amd64 and arm64 |
| `SHA256SUMS` | GoReleaser hashes the two raw binaries |
| Consumer installation | Fixed GitHub release origin plus exact tag, commit, architecture, and verified checksum |
| R10 evidence | Candidate, merge, and release records remain separate and preserve exact identities |

## References

- Rationale:
  [core ADR-0023](https://github.com/frostyard/core/blob/main/docs/adr/0023-verified-pinned-downloads.md)
  and
  [core ADR-0048](https://github.com/frostyard/core/blob/main/docs/adr/0048-publish-debian-packages-to-explicit-codenames.md)
- Context: [CI/CD and GitHub Action](../design/ci-cd.md)
- Built in:
  [Frostyard production publisher boundary](../plans/0001-frostyard-production-publisher.md)
