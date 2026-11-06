# The publish-to-r2 GitHub Action

Repogen provides a legacy reusable GitHub Action for publishing non-Debian
package formats to repositories hosted on Cloudflare R2 storage.

> **Production status:** The current action is the generic legacy publisher.
> It requires an exact Repogen tag and commit but still uses broad
> synchronization for supported non-Debian formats. Debian is rejected
> structurally; the action cannot initialize or reconcile Frostyard
> Trixie/Forky. Frostyard Debian publication moved to `frostyard/apt-publisher`
> (core ADR-0055). This action stays for sysexts and other non-Debian
> formats.

## Quick Start

```yaml
- name: Publish to repository
  uses: frostyard/repogen/.github/actions/publish-to-r2@<reviewed-action-commit>
  with:
    r2-account-id: ${{ secrets.R2_ACCOUNT_ID }}
    r2-access-key-id: ${{ secrets.R2_ACCESS_KEY_ID }}
    r2-secret-access-key: ${{ secrets.R2_SECRET_ACCESS_KEY }}
    r2-bucket: my-repo-bucket
    packages-dir: ./dist
    package-type: sysext
    base-url: https://extensions.example.com/repo
    repogen-version: v1.2.3
    repogen-commit: <40-character-release-commit>
```

## Full Example: Publishing Sysext Images

```yaml
name: Build and Publish Sysext
on:
  push:
    tags:
      - "v*.*.*"

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@<reviewed-checkout-commit>

      - name: Build sysext image
        run: |
          # Build your sysext (example)
          ./build-sysext.sh
          # Output: dist/myext_1.0.0_13_x86-64.raw.zst

      - name: Publish to repository
        uses: frostyard/repogen/.github/actions/publish-to-r2@<reviewed-action-commit>
        with:
          r2-account-id: ${{ secrets.R2_ACCOUNT_ID }}
          r2-access-key-id: ${{ secrets.R2_ACCESS_KEY_ID }}
          r2-secret-access-key: ${{ secrets.R2_SECRET_ACCESS_KEY }}
          r2-bucket: my-extensions
          repo-prefix: repo
          packages-dir: ./dist
          package-type: sysext
          base-url: https://extensions.example.com/repo
          repogen-version: v1.2.3
          repogen-commit: <40-character-release-commit>
```

## Action Inputs

| Input                  | Required | Default     | Description                                                                              |
| ---------------------- | -------- | ----------- | ---------------------------------------------------------------------------------------- |
| `r2-account-id`        | Yes      | -           | Cloudflare R2 Account ID                                                                 |
| `r2-access-key-id`     | Yes      | -           | Cloudflare R2 Access Key ID                                                              |
| `r2-secret-access-key` | Yes      | -           | Cloudflare R2 Secret Access Key                                                          |
| `r2-bucket`            | Yes      | -           | Cloudflare R2 Bucket name                                                                |
| `packages-dir`         | Yes      | -           | Directory containing packages to add                                                     |
| `package-type`         | Yes      | -           | Package type; `deb` is rejected by this legacy action                                    |
| `base-url`             | No\*     | -           | Base URL for the repository (\*required for `sysext`)                                    |
| `repo-prefix`          | No       | -           | Path prefix in R2 bucket                                                                 |
| `gpg-private-key`      | No       | -           | GPG private key (base64 or ASCII armored)                                                |
| `gpg-passphrase`       | No       | -           | GPG key passphrase                                                                       |
| `rsa-private-key`      | No       | -           | RSA private key for Alpine (PEM format)                                                  |
| `rsa-passphrase`       | No       | -           | RSA key passphrase                                                                       |
| `rsa-key-name`         | No       | `repogen`   | Key name for Alpine signatures                                                           |
| `codename`             | No       | -           | Codename for Debian repos; no default, required for `deb`; `stable` is refused           |
| `suite`                | No       | -           | Suite for Debian repos (defaults to codename); `stable` is refused                       |
| `components`           | No       | `main`      | Components for Debian repos                                                              |
| `architectures`        | No       | `all,amd64` | Architectures (comma-separated)                                                          |
| `origin`               | No       | -           | Repository origin name                                                                   |
| `label`                | No       | -           | Repository label                                                                         |
| `repo-name`            | No\*     | -           | Repository name (\*required for `pacman`)                                                |
| `repogen-version`      | Yes      | -           | Exact v-prefixed Repogen release tag                                                     |
| `repogen-commit`       | Yes      | -           | Exact 40-character commit embedded in the binary                                         |
| `skip-duplicates`      | No       | `false`     | Skip packages that already exist instead of failing                                      |
| `skip-published-identities` | No  | `false`     | sysext only: skip each built sysext whose identity the signature-verified `SHA256SUMS` already lists, whatever its bytes; nothing left means nothing is uploaded and the run succeeds. Requires `gpg-private-key` and a `./repo` without payloads. Known gap: a content change without a version or revision bump is not republished |
| `purge-cache`          | No       | `false`     | Purge Cloudflare cache after upload                                                      |
| `cloudflare-zone`      | No\*     | -           | Cloudflare Zone ID (\*required if `purge-cache` is `true`)                               |
| `cloudflare-api-token` | No\*     | -           | Cloudflare API Token with Cache Purge permission (\*required if `purge-cache` is `true`) |

## Action Outputs

| Output           | Description                                |
| ---------------- | ------------------------------------------ |
| `packages-added` | Number of packages added to the repository |
| `published`      | `'true'` when the action uploaded to R2; `'false'` when `skip-published-identities` left nothing to publish |

## How It Works

1. **Downloads repogen** from GitHub releases
2. **Syncs existing metadata** from R2 (only metadata files, not packages)
3. **Runs repogen** in incremental mode to add new packages
4. **Checks skipped identities** still have their payloads in the bucket (only
   with `skip-published-identities`)
5. **Uploads** the updated repository back to R2, or uploads nothing when
   every sysext identity was already published

The action uses incremental mode, which means:

- Existing packages are preserved
- Only metadata files are synced locally; a sysext metadata restore failure
  aborts instead of being treated as an empty repository
- Sysext identity includes OSVersion, so matching OS 13 and OS 14 artifacts
  coexist
- A same-identity sysext is skipped only when its SHA-256 is unchanged;
  changed or unverifiable bytes fail

With `skip-published-identities: true` (sysext only, needs `gpg-private-key`)
a rebuilt sysext is
skipped when its identity is already in the signature-verified `SHA256SUMS`,
whatever its bytes. Before any upload the action checks, with one `HEAD`
request per skipped identity, that each such payload object still exists, and
it refuses a `./repo` that already holds a payload from an earlier step. A run
that
skipped every built sysext uploads nothing, purges nothing, and reports
`published=false`, while an empty `packages-dir` stays an error. Known gap: a
content change without a version or revision bump is not republished
([ADR-0018](../adr/0018-opt-in-identity-level-skip-for-published-sysexts.md)).

The action is not a Debian publisher. Debian uses the separate protected
writer. A sysext caller must serialize the full restore/generate/upload cycle
across all extensions because `ext/index` is shared. The current R2 upload
cannot atomically replace `SHA256SUMS` and `SHA256SUMS.gpg`; do not activate
new production credentials until that provider-side commit boundary has been
reviewed and approved.

## Setting Up R2 Credentials

1. Go to Cloudflare Dashboard → R2 → Manage R2 API Tokens
2. Create an API token with "Object Read & Write" permissions
3. Note the Access Key ID and Secret Access Key
4. Add these as repository secrets in GitHub:
   - `R2_ACCOUNT_ID`
   - `R2_ACCESS_KEY_ID`
   - `R2_SECRET_ACCESS_KEY`

## Setting Up Cache Purge (Optional)

If your R2 bucket is served through a Cloudflare domain, you can configure the action to automatically purge the CDN cache after uploading. This ensures clients immediately see the updated repository metadata.

1. Go to Cloudflare Dashboard → Your Domain → Overview (note the Zone ID in the right sidebar)
2. Go to Profile → API Tokens → Create Token
3. Create a Custom Token with permission: **Zone → Cache Purge → Purge**
4. Restrict the token to the specific zone hosting your repository
5. Add these as repository secrets in GitHub:
   - `CLOUDFLARE_ZONE` (the Zone ID)
   - `CLOUDFLARE_API_TOKEN` (the API token you created)
6. Enable cache purging in your workflow:

```yaml
- uses: frostyard/repogen/.github/actions/publish-to-r2@<reviewed-action-commit>
  with:
    # ... other inputs ...
    purge-cache: "true"
    cloudflare-zone: ${{ secrets.CLOUDFLARE_ZONE }}
    cloudflare-api-token: ${{ secrets.CLOUDFLARE_API_TOKEN }}
```

## Configuring Public Access

To serve your repository publicly:

1. In Cloudflare Dashboard → R2 → Your Bucket → Settings
2. Enable "Public Access" or connect a custom domain
3. Use the public URL as your `base-url`
