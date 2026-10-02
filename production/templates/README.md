# Production reconcile templates

These files are the starting point for the exact, digest-pinned inputs of
`repogen reconcile-production`, `repogen submit-intake`, and the
[`reconcile-production` Action](../../.github/actions/reconcile-production/action.yml).
They are **templates and authorize nothing**: every `REPLACE-<field>` value,
`authoritative_target_absent: false` and the empty `allowed_pool_objects`
list are deliberately invalid, so the CLI's own validation rejects an
unfilled copy. `internal/production/template_test.go` keeps them canonical,
complete and rejected while unfilled.

The concrete config and policy for a request belong to the producer
repository's reviewed canary change, not to this repository, because they pin
values that exist only once the request, the producer build and the repogen
release exist.

| File | Schema |
| --- | --- |
| `trixie-config.template.json` | `org.frostyard.repogen.production-config.v1` |
| `submit-config.template.json` | `org.frostyard.repogen.intake-submit-config.v1` |
| `authorization-policy.template.json` | `org.frostyard.repogen.authorization-policy.v1` |

## Fixed values

- `target` is `trixie`; the frozen signed `stable` suite is never a target.
- `region` is `auto`; `operation` is `initialize`.
- `publication_bucket` is `frostyardrepo`, the existing package-repository
  bucket.

## Placeholders

| Field | Filled with | Who supplies it |
| --- | --- | --- |
| `account_id`, `endpoint` | R2 account id and `https://<account_id>.r2.cloudflarestorage.com` | Must equal the `r2-account-id` Action input |
| `intake_bucket`, `coordination_bucket` | Exact bucket names | Repository administrator creates the buckets |
| `request_sha256` (both files) | Digest of the retained request | Producer submission |
| `provenance_sha256` | Digest of the retained provenance | Producer submission |
| `operator` | Identity of the person authorizing the write | Authorizing operator |
| `producer`, `producer_commit`, `producer_tree` | Producer repository and exact commit/tree | Producer build |
| `repogen_version` | Version embedded in the release binary: the tag **without** its `v` (tag `v0.6.0` → `0.6.0`) | Repogen release |
| `action_commit` | Full commit embedded in the release binary, equal to the `repogen-commit` Action input | Repogen release |
| `repogen_sha256` | SHA-256 of the installed release binary | Repogen release |
| `signing_key_fingerprint`, `signing_public_key_sha256` | Uppercase 40-hex fingerprint and public-key digest | Signing-key custodian |
| `release_time` | RFC 3339 UTC time | Authorizing operator |
| `allowed_pool_objects` | Every `pool/main/...` object with SHA-256 and size | Producer build |
| `authoritative_target_absent` | `true` only after confirming the target is absent | Authorizing operator |
| `provider_permission_evidence` | Reference to provider-side permission evidence | Repository administrator |

`submit-config.template.json` configures
[`repogen submit-intake`](../../docs/specs/intake-submit.md). It fixes `kind`
`debian`, `target` `trixie` and `publication_bucket` `frostyardrepo` (the
command requires exactly this value and refuses intake or coordination buckets
equal to it; the intake and coordination buckets must also differ).
Fill `account_id`, `endpoint`, `intake_bucket` and `coordination_bucket` as
above, and `producer` with the submitting repository (`owner/name`, equal to
`GITHUB_REPOSITORY`).

## Filling and pinning

The files must remain canonical RFC 8785 JSON: sorted keys, no insignificant
whitespace and no trailing newline (`jq -cSj .` produces this for these
schemas). Pin the exact bytes:

```sh
sha256sum production-config.json authorization-policy.json
```

The digests are passed as `config-sha256` and `policy-sha256`. See the
[production contract](../../docs/specs/production-r2-reconciliation.md).
