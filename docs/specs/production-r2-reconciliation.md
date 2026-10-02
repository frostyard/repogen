# Spec: Exact R2 production reconciliation

This contract governs `repogen reconcile-production`, the R2/S3-compatible
stores it uses, and the exact nonsecret configuration and authorization bytes
consumed by the durable Debian recovery writer. It is a code boundary, not
proof of provider-side permissions and not authorization to run a production
operation.

## Interface

The command processes one exact retained request:

```text
repogen reconcile-production \
  --config ./production-config.json \
  --config-sha256 <sha256> \
  --policy ./authorization-policy.json \
  --policy-sha256 <sha256> \
  --request-sha256 <sha256> \
  --provenance-sha256 <sha256> \
  --credentials-file ./r2-credentials.json \
  --signing-key ./repository-private-key.asc \
  [--signing-passphrase-file ./signing-passphrase]
```

Every flag except `--signing-passphrase-file` is mandatory and must be
supplied explicitly. Configuration and policy files use the RFC 8785 subset
already enforced for durable intake: canonical UTF-8 JSON, no duplicate or
unknown fields, and safe-range integers only.

### Production configuration

Schema: `org.frostyard.repogen.production-config.v1`

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `account_id` | string | yes | Exact R2 account; no path or whitespace |
| `endpoint` | string | yes | Exactly `https://<account_id>.r2.cloudflarestorage.com` |
| `region` | string | yes | Exactly `auto` |
| `publication_bucket` | string | yes | Exact package-repository bucket |
| `publication_prefix` | string | yes | Clean relative prefix; may be empty |
| `intake_bucket` | string | yes | Exact durable-intake bucket |
| `intake_prefix` | string | yes | Clean relative prefix; may be empty |
| `coordination_bucket` | string | yes | Exact liveness-lock bucket |
| `coordination_prefix` | string | yes | Clean relative prefix; may be empty |
| `target` | string | yes | Exactly `trixie` |
| `request_sha256` | string | yes | Exact retained request digest |

### Authorization policy

Schema: `org.frostyard.repogen.authorization-policy.v1`

The policy fixes one operator, target, operation, request and provenance
digest, producer repository/commit/tree, Repogen version/commit/binary digest,
signing fingerprint/public-key digest, release time, and every allowed
`pool/main` object. It also records an authoritative provider-side absence
assertion and a nonempty provider-permission evidence reference. These fields
are reviewable evidence. The CLI cannot prove that the named operator is the
person running it or that an R2 token has path-level permissions.

### Secret credential file

The R2 credential file is noncanonical secret JSON and MUST be a regular,
non-symlink mode-`0600` file:

| Field | Type | Required | Constraints |
| --- | --- | --- | --- |
| `access_key_id` | string | yes | Nonempty |
| `secret_access_key` | string | yes | Nonempty |
| `session_token` | string | no | Explicit temporary token when used |

No environment, shared AWS config, instance metadata, or web-identity
credential discovery is loaded. The signing passphrase, when needed, is read
from a separate regular mode-`0600` file and passed to `gpg` through standard
input with loopback pinentry, never a process argument or environment value.

### CI surface

The composite Action
[`.github/actions/reconcile-production`](../../.github/actions/reconcile-production/action.yml)
is the only CI entry point. No workflow in this repository calls it; a
producer wires it in its own reviewed change, pinned to a repogen release that
contains it.

| Input | Required | Constraints |
| --- | --- | --- |
| `repogen-version` | yes | Exact `vMAJOR.MINOR.PATCH` tag; no `latest` or branch |
| `repogen-commit` | yes | Full lowercase 40-hex commit the tag must resolve to |
| `config`, `policy` | yes | Regular non-symlink files |
| `config-sha256`, `policy-sha256` | yes | Lowercase SHA-256 of the exact bytes; not trimmed or normalised |
| `request-sha256`, `provenance-sha256` | yes | Lowercase SHA-256 |
| `r2-account-id` | yes | Equals config `account_id`; endpoint must be its R2 endpoint |
| `r2-access-key-id`, `r2-secret-access-key` | yes | Existing R2 credentials |
| `gpg-private-key` | yes | Existing signing key, ASCII-armored or base64 |
| `gpg-passphrase` | no | Written to a passphrase file only when nonempty |

There is no codename, suite or target input: the target comes only from the
pinned config and must be exactly `trixie`.

Steps, in order:

1. **Validate inputs** runs `scripts/validate-reconcile-inputs.sh` before
   anything is installed. It refuses a missing input, a malformed pin, a
   config or policy that is a symlink or does not match its pin, a config or
   policy target of `stable` (any case or padding) or anything but `trixie`,
   a config or policy `request_sha256` other than `request-sha256`, and a
   config account or endpoint that does not match `r2-account-id`, and a
   policy `repogen_version` or `action_commit` other than the tag without its
   `v` and the exact commit the binary embeds. It never
   prints secret values.
2. **Install repogen** runs `scripts/install-release.sh --github-release`
   with the exact tag and commit.
3. **Reconcile** writes the credential JSON, the decoded signing key and the
   optional passphrase under `umask 077` into
   `$RUNNER_TEMP/repogen-reconcile-secrets` (directory `0700`, files `0600`),
   then runs `reconcile-production` with every flag explicit. Secrets reach
   the step only through `env:`; jq reads the credentials with `env.`, so none is a process argument, written to
   `$GITHUB_ENV`, or echoed.
4. **Remove secrets** deletes that directory with `if: always()`.

Canonical templates for the config and policy live in
[`production/templates/`](../../production/templates/README.md). Their
placeholders are deliberately invalid, so an unfilled template fails
`Validate()`; they authorize nothing. The concrete files are written and
digest-pinned in the producer's reviewed canary change.

Prerequisites outside this repository's code before any run:

- a repogen release that contains this Action and the CLI;
- the intake and coordination buckets named in the config;
- a producer submission that places the request, provenance and receipt in
  the durable intake store, through
  [`repogen submit-intake`](intake-submit.md) (plan 0001 T0 equivalent,
  [ADR-0015](../adr/0015-ci-run-submit-only-intake.md));
- the person's authorization of the exact policy and the first production
  write.

## Rules

- Configuration, policy, request, and provenance digests MUST all match their
  explicit command-line pins before any provider mutation.
- The running executable bytes, embedded version, and embedded commit MUST
  match the policy.
- The signer's exported public-key bytes and primary-key fingerprint MUST
  match the policy before any provider mutation.
- Exactly one selected receipt is processed. A later receipt or an unresolved
  predecessor fails before the writer runs. Replay verifies the completed
  result and performs no new publication write.
- Initialization enumerates the complete `dists/trixie/` prefix without a
  delimiter. A 403, timeout, 5xx, truncated/nonprogressing page, duplicate key,
  or ambiguous provider response is not absence.
- Conditional creation uses one `PutObject` with `If-None-Match: *`.
- Replacement reads the prior body and ETag from the same `GetObject`, verifies
  the full body SHA-256 against the expected prior, then uses that exact ETag
  in `If-Match`. A 409 or 412 is a conflict and never a successful write.
- Publication reads and read-back use the configured R2 S3 origin endpoint,
  not the public CDN/cache path.
- Code permits writes only to exact policy-listed `pool/main` objects,
  `dists/trixie/`, the exact durable-intake namespace, and the separate
  coordination namespace. The S3 interface exposes no delete, copy,
  multipart, sync, cache, stable, other-codename, root-public-key, or sysext
  method.
- Publication still follows pool, by-hash, canonical index, Release,
  Release.gpg, then exactly one final InRelease order. Every object is read
  back and hashed.
- The coordination record uses conditional create and ETag compare-and-swap.
  A held record is never stolen because of age. Release changes it to
  `released` with `If-Match`; a release failure is returned. A writer crash
  can therefore leave a stuck lock requiring separately reviewed exact-state
  recovery.
- The lock improves liveness and operator serialization only. It is not a
  fencing token and is not a technical safety boundary; object-level
  preconditions and read-back remain the write safety controls.
- Cloudflare R2 API-token bucket scope, local code allowlists, and an advisory
  authorization policy MUST NOT be described as provider-enforced path
  permissions. Provider effective permissions, signer custody, operator
  identity, authoritative absence, exact production config/policy, credentials,
  and the write itself remain external gates.

## Derived artifacts

| Artifact | Derivation |
| --- | --- |
| Durable attempt | Canonical request digest plus one random attempt ID |
| Production request manifest | Exact staged packages, signer fingerprint, target, operation, and object list |
| Durable result | Request/provenance/policy lineage, executable identity, signer fingerprint, Release/InRelease digests, and all published objects |
| Liveness lock | SHA-256 of the exact internal target name under the configured coordination prefix |

## References

- Rationale: [ADR-0013](../adr/0013-separate-generic-generation-from-production-publishing.md)
- Context: [Repogen overview](../design/overview.md)
- Delivery sequence: [Frostyard production publisher boundary](../plans/0001-frostyard-production-publisher.md)
- Transaction details: [Generator details](generators.md)
