# Prepare and verify the R10 Repogen release

Use this procedure only with the authority granted for the current assignment.
Candidate preparation, merge, and release publication are separate actions.
Never infer permission to merge, tag, publish, or change a downstream from
permission to prepare or review a candidate.

## 1. Prepare the candidate

1. Start from the exact reviewed R2-R9 lineage and record every consumed
   commit.
2. Confirm the worktree is clean except for the intended R10 change.
3. Run the complete gate from
   [`docs/specs/r10-release-acceptance.md`](../../docs/specs/r10-release-acceptance.md).
4. Run GoReleaser `v2.18.1` in snapshot mode and retain the generated asset
   names, checksums, embedded version/commit output, tool version, and exact
   candidate commit as review evidence.
5. Commit the candidate, register that exact clean commit, and obtain
   independent review. Any material correction invalidates prior gate and
   review evidence.

Stop here unless a human separately authorizes merging the exact reviewed
commit.

## 2. Verify the merge

1. Record the human-authorized merge operation and the reviewed candidate
   commit/tree.
2. After the merge, resolve the default-branch commit read-only.
3. Require the merged tree to equal the independently reviewed candidate
   tree. A different tree returns to candidate review.

Stop here unless a human separately authorizes publishing one exact version
from that merge commit.

## 3. Publish one immutable release

1. Choose a previously unused semantic version and verify that neither its tag
   nor release exists.
2. Create the annotated tag at the exact accepted merge commit and publish it
   only through the authorized human operation.
3. Record the tag commit and the one `release.yml` workflow run. A second
   publisher or an ambiguous workflow is a failure.
4. Wait for the release to become visible. Do not retry publication until
   external state proves the first action failed without creating the tag or
   release.

## 4. Verify published bytes

1. Require exactly `repogen-linux-amd64`, `repogen-linux-arm64`, and
   `SHA256SUMS`.
2. Download all three assets from the exact tag and retain their SHA-256
   digests.
3. Require exactly one lowercase checksum line per binary and verify both
   binaries against it.
4. On matching amd64 and arm64 runners, install with:

   ```bash
   ./scripts/install-release.sh \
     --github-release \
     <tag> \
     <40-character-tag-commit> \
     <architecture> \
     ./repogen-release
   ./repogen-release version --short
   ```

5. Require both binaries to report the tag version without `v` and the exact
   tag commit. Record the release URL, workflow run, asset names, asset
   digests, checksum bytes, and installer output without credentials.
6. Only this verified published release may become a downstream version pin.

If a visible release is wrong, do not move its tag or replace its assets.
Preserve evidence, correct forward in a new reviewed candidate, and publish a
new version only with separate authorization.
