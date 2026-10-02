# Spec: Durable intake submit (`repogen submit-intake`)

The contract by which a producer places one Debian `trixie` request, its
provenance and its artifacts in the durable R2 intake, so that
`repogen reconcile-production` has a receipt to process. Consumers are
producer release workflows (through the `submit-intake` Action) and the
reconciler, which reads exactly the records written here.

## Interface

### CLI

```sh
repogen submit-intake \
  --config submit-config.json --config-sha256 <sha256> \
  --policy-sha256 <sha256> \
  --request request.json --provenance provenance.json \
  --artifacts-dir dist/ --submission-key <key> \
  --credentials-file r2-credentials.json
```

| Flag | Meaning |
| --- | --- |
| `--config`, `--config-sha256` | Canonical submit configuration and the SHA-256 of its exact bytes |
| `--policy-sha256` | SHA-256 of the reconcile authorization policy; bound into the receipt |
| `--request` | Canonical request JSON (`org.frostyard.repogen.request.v1`) |
| `--provenance` | Canonical provenance JSON whose SHA-256 is the request's `provenance_digest` |
| `--artifacts-dir` | Flat directory holding exactly the request's artifacts |
| `--submission-key` | Idempotency key: letters, digits, `.`, `_`, `-` |
| `--credentials-file` | Regular, non-symlink, mode-`0600` R2 credentials JSON |

Every flag is mandatory. On success the canonical receipt JSON is printed on
stdout. Any failure exits non-zero and never prints credential values.

### Submit configuration

Schema `org.frostyard.repogen.intake-submit-config.v1`, canonical RFC 8785
JSON, unknown and duplicate fields refused. Template:
[`production/templates/submit-config.template.json`](../../production/templates/submit-config.template.json).

| Field | Constraint |
| --- | --- |
| `schema` | exactly the schema above |
| `account_id` | safe name |
| `endpoint` | exactly `https://<account_id>.r2.cloudflarestorage.com` |
| `region` | `auto` |
| `intake_bucket`, `coordination_bucket` | safe names, different from each other and (case-insensitively) from `frostyardrepo` |
| `intake_prefix`, `coordination_prefix` | empty or a clean relative path |
| `publication_bucket` | exactly `frostyardrepo`, the bucket fixed in code; a decoy value is refused |
| `kind` | `debian` |
| `target` | exactly `trixie` (`stable` in any case or padding is refused) |
| `producer` | one `owner/name` repository |

### Action `.github/actions/submit-intake`

Inputs: `repogen-version`, `repogen-commit`, `config`, `config-sha256`,
`policy-sha256`, `request`, `provenance`, `artifacts-dir`, `submission-key`,
`r2-account-id`, `r2-access-key-id`, `r2-secret-access-key`. There is no
target, codename or suite input.

1. **Validate inputs** — `scripts/validate-submit-inputs.sh` refuses a missing
   input, a version that is not `vX.Y.Z`, a commit that is not 40 lowercase
   hex, a malformed digest or submission key, a symlinked or non-regular
   config, request or provenance, a non-directory artifacts path, a config
   that does not match its pin, a target other than `trixie` (`stable` named),
   a `producer` other than `GITHUB_REPOSITORY`, an `account_id` or endpoint
   that does not match `r2-account-id`, a `publication_bucket` other than
   `frostyardrepo`, an intake or coordination bucket equal to it, and an
   intake bucket equal to the coordination bucket.
2. **Install repogen** — `scripts/install-release.sh --github-release` with the
   exact tag and commit.
3. **Submit** — under `umask 077`, writes the credentials with
   `jq -n '{…: env.R2_…}'` into `$RUNNER_TEMP/repogen-submit-secrets` (mode
   `0700`, file `0600`) and runs `repogen submit-intake` with every flag.
4. **Remove secrets** — `if: always()`, removes that directory.

Secrets reach shell steps only through `env:`; no `run:` block interpolates an
expression, no secret is a process argument, and nothing is written to
`GITHUB_ENV`. No workflow in this repository calls the Action.

## Rules

- Before the first write: the config matches its pin and validates; the
  request decodes canonically and validates; `kind` is `debian`; `target`,
  `codename` and `suite` are each exactly `trixie`; the principal (the
  config's `producer`) equals the request's `producer`; the provenance is
  canonical and hashes to `provenance_digest`; the artifacts directory
  contains only regular non-symlink files whose digests equal
  `artifact_digests` exactly (none missing, none extra, none duplicated), and
  each is copied into a private snapshot and hashed before anything is
  stored. Artifacts are uploaded only from those verified snapshots, so a
  source file that changes afterwards cannot occupy a blob key.
- The publication bucket is fixed in code (`frostyardrepo`), not trusted from
  the configuration. The intake and coordination buckets must differ from it
  and from each other, because lock records are replaced in the coordination
  bucket.
- Writes, in order: each artifact at `blobs/sha256/<2hex>/<sha256>`, the
  provenance at `manifests/provenance/v1/sha256/<sha256>.json`, then
  `Recorder.Accept` writes `manifests/request/v1/sha256/<sha256>.json`,
  `submissions/v1/sha256/<sha256(principal NUL key)>.json` and
  `receipts/v1/debian/trixie/<seq>-<request sha256>.json`.
- Every write is `If-None-Match: *` to the intake bucket and is read back.
  Any other key fails with `intake.ErrScope` before reaching the store. The
  only other provider writes are the coordination-bucket lock records.
- Idempotency: the same request bytes with the same submission key return the
  existing receipt and write nothing new. A reused submission key with
  different request bytes fails with `ErrConflict`. An existing blob or
  provenance object with different bytes fails with `ErrIntegrity` and no
  receipt is written.
- The receipt's `policy_sha256` is `--policy-sha256`; `reconcile-production`
  later requires it to equal its pinned policy digest.

## Prerequisites

Outside this repository's code, before any producer can submit:

- a repogen release that contains this command and Action;
- the intake and coordination buckets named in the config, which do not
  exist yet.

## References

- Rationale: [ADR-0015](../adr/0015-ci-run-submit-only-intake.md)
- Context: [design/ci-cd.md](../design/ci-cd.md),
  [specs/production-r2-reconciliation.md](production-r2-reconciliation.md),
  [plan 0001](../plans/0001-frostyard-production-publisher.md) T0
