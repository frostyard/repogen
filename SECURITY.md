# Security policy

repogen builds and signs package repositories. The signing keys and the
published metadata are sensitive. A repository that repogen writes is only as
trustworthy as the key that signs it and the metadata it publishes.

## Supported versions

Only the latest `v0.x` release gets fixes. Today that is `v0.7.2`
([releases](https://github.com/frostyard/repogen/releases)).

`main` between releases gets no fixes. Older releases get no fixes.

The tags `v1.0.0` to `v1.0.4` come from the upstream fork `ralt/repogen`.
They are not repogen releases.

## Report a vulnerability

Do not open a public issue.

Use GitHub private vulnerability reporting at
https://github.com/frostyard/repogen/security (select "Report a
vulnerability", or open
https://github.com/frostyard/repogen/security/advisories/new).

repogen is a GitHub fork of `ralt/repogen`. Report the problem here, not
upstream.

If GitHub reporting is not available to you, send email to
[bketelsen@gmail.com](mailto:bketelsen@gmail.com).

## What happens next

1. The maintainer confirms receipt.
2. The maintainer fixes the problem on `main`.
3. The maintainer releases a patch.
4. The maintainer publishes a security advisory.

## Scope

The most sensitive areas of this repository:

- signing: `internal/signer/` and `internal/generator/deb/release*.go`.
- sysext manifests and their signatures: `internal/generator/sysext/`.
- the release installer: `scripts/install-release.sh`.
- the `publish-to-r2` action and its upload scripts. These run with R2
  credentials and the repository signing key.
