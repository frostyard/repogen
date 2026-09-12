# Release Process

Repogen has one release pipeline and one machine-consumed asset contract.
Publishing a tag or GitHub release is an external operation and requires the
repository's normal human authorization and review.

## Prerequisites

- A clean, reviewed commit on the release branch.
- Go and the repository-required lint/test tools.
- Permission to create and push the exact release tag.
- Confirmation that the intended semantic version and source commit are the
  ones being released.

Run the repository gates before creating a tag:

```bash
make build
make fmt
git diff --exit-code
make lint
go test -v -short -race ./...
```

## Authoritative contract

`.github/workflows/release.yml` is the only tag-triggered release workflow.
It uses commit-pinned GitHub Actions and exact GoReleaser `v2.18.1`.
`.goreleaser.yml` publishes:

| Asset | Purpose |
|---|---|
| `repogen-linux-amd64` | Raw Linux amd64 executable |
| `repogen-linux-arm64` | Raw Linux arm64 executable |
| `SHA256SUMS` | SHA-256 digest list for both executables |

The binaries embed:

- the semantic version without the leading `v`; and
- the full 40-character commit used by GoReleaser.

Inspect that identity with:

```bash
repogen version --short
# <version> <40-character-commit>
```

The former hand-built package/repository release workflow is not a second
publisher. Native `.deb`, `.rpm`, `.apk`, bottles, and generated repository
archives are not part of this release contract.

## Creating a release

Use an annotated semantic-version tag:

```bash
git tag -a v1.2.3 -m "Release version 1.2.3"
git push origin v1.2.3
```

The tag must match
`vMAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]`. The workflow builds only from the
checked-out tag commit, embeds its full commit identity, creates
`SHA256SUMS`, and publishes the three assets above.

Do not run a second release workflow or manually upload a differently named
binary under the same tag.

## Verifying a release

Record the tag's full commit and install one exact architecture through the
same verifier used by repository consumers:

```bash
git rev-list -n 1 v1.2.3
./scripts/install-release.sh \
  v1.2.3 \
  <40-character-tag-commit> \
  "$(uname -m)" \
  ./repogen-release
./repogen-release version --short
```

The installer:

1. rejects `latest`, branches, and malformed versions;
2. maps only amd64/x86_64 and arm64/aarch64 to the stable raw asset names;
3. downloads the selected binary and `SHA256SUMS` from the exact tag;
4. requires exactly one lowercase SHA-256 entry for that asset;
5. verifies the downloaded bytes before making them executable;
6. verifies the embedded version and full commit; and
7. installs only after every check passes.

Also compare the displayed GitHub release assets with the table above. A
missing checksum, duplicate checksum entry, unexpected archive-only output,
or embedded identity mismatch is a failed release.

## Failure and correction

- A failed workflow publishes no acceptable release evidence.
- Do not move, replace, or silently reuse a visible tag.
- If an artifact is already visible and incorrect, preserve evidence and
  correct forward with a new reviewed version.
- Do not treat a successful workflow alone as proof that a consumer download
  was digest- and identity-verified.

The production Debian transaction is separate from this release pipeline.
Publishing Repogen does not authorize package ingestion, repository writes,
credential changes, cache mutation, or a Frostyard canary.
