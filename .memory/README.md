# .memory/ — corrections inbox

The single sanctioned inbox for durable learned corrections (pattern from
[frostyard/core ADR-0018](https://github.com/frostyard/core/blob/main/docs/adr/0018-org-wide-agent-instruction-and-knowledge-surfaces.md);
adopted here by
[ADR-0012](../docs/adr/0012-acmm-conformance-via-canonical-aliases.md)).

Contract:

- Each correction is 1 file in `.memory/corrections/`, in the form that
  [frostyard/core ADR-0081](https://github.com/frostyard/core/blob/main/docs/adr/0081-store-each-correction-in-a-file-of-its-own.md)
  sets. 2 pull requests that each add a record add 2 different files, so they
  do not conflict.
- **Name:** `<date>-<slug>.json`; the date part equals the record's `date`,
  and the slug has at most 60 characters. A record from a pull request uses
  the slug `pr-<N>-<words>`, for example `2026-11-13-pr-412-cache-key.json`.
  A record with no pull request uses 2 to 5 words and 4 random hexadecimal
  characters, for example `2026-11-13-agent-scope-rule-3f9a.json`. The slug
  prefix `legacy-` is reserved: the `<date>-legacy-<NNNN>.json` files hold
  line `NNNN` of the old `.memory/corrections.jsonl`, which is retired here.
- **Content:** 1 JSON object with exactly 5 string fields, in this order, and
  in the canonical form `JSON.stringify(record, null, 2) + "\n"`:

  ```json
  {
    "date": "YYYY-MM-DD",
    "scope": "…",
    "correction": "…",
    "evidence": "…",
    "promoted_to": ""
  }
  ```

  `date` is a real date; `scope`, `correction` and `evidence` are not empty.
- **Permanent:** a record file is never removed or renamed. Its text never
  changes, except that an empty `promoted_to` is filled once.
- **Checked here:** this repository runs core's `scripts/check-corrections.mjs`
  itself, as a byte-identical copy (change it in core, then copy it).
  `make corrections-check` (part of `make verify` and `make ci`) checks that
  every file is a record in the canonical form without comparing it with
  anything. `make ci` also runs `make corrections-base`; with `BASE` set to a
  commit (CI passes the pull request's base commit), that runs
  `node scripts/check-corrections.mjs --base`, which refuses a removed,
  renamed or edited record, except that an empty `promoted_to` may be filled
  in once. A `BASE` that does not resolve to a commit is an error, and a
  `BASE` from before this gate skips the comparison with a notice.
- frostyard/core's `scripts/new-correction.mjs` writes a record file in the
  canonical form; a file written by hand is fine when it passes.

- `promoted_to` starts empty; when a correction graduates into
  [AGENTS.md](../AGENTS.md), a doc under [docs/](../docs/README.md), or a
  skill, set it to that path. Promotion is the only sanctioned duplication —
  the `frostyard-repo-docs` maintenance pass drains this inbox.
- A blocking review point adds 1 record file in the pull request that fixes
  it, next to the guard that catches it.
- Never record credentials or non-public vulnerability details.
