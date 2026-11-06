# frostyard/repogen

Repogen is a CLI tool written in Go that generates static repository
structures for multiple package managers. It scans directories for packages,
generates appropriate metadata files, and signs repositories with GPG/RSA
keys. Start at [docs/README.md](docs/README.md); read
[docs/design/overview.md](docs/design/overview.md) for codebase context
before performing tasks.

This file (`AGENTS.md`) is the CANONICAL agent instructions — `CLAUDE.md`,
`GEMINI.md`, `CONTRIBUTING.md`, `.cursorrules`, and
`.github/copilot-instructions.md` are symlinks to it, and `.claude/skills`
symlinks to `.agents/skills/`
([ADR-0012](docs/adr/0012-acmm-conformance-via-canonical-aliases.md);
pattern from
[frostyard/core ADR-0002](https://github.com/frostyard/core/blob/main/docs/adr/0002-agent-portable-instruction-surface.md)
and
[ADR-0029](https://github.com/frostyard/core/blob/main/docs/adr/0029-acmm-conformance-via-canonical-aliases.md)).
Edit only the canonical paths; keep content tool-agnostic.

## Supported package types

- **Debian/APT** (.deb packages)
- **Alpine/APK** (.apk packages)
- **Arch Linux/Pacman** (.pkg.tar.zst, .pkg.tar.xz, .pkg.tar.gz)
- **Homebrew** (bottle files)
- **systemd-sysext** (.raw, .raw.zst, .raw.xz, .raw.gz)

## Project structure

```
cmd/repogen/          # CLI entry point
internal/
  baseline/           # Repository baseline contract tests (test-only)
  cli/                # Command-line interface (Cobra commands)
  generator/          # Repository generators for each package type
    apk/              # Alpine APK repository generator
    deb/              # Debian APT repository generator
    homebrew/         # Homebrew bottle repository generator
    pacman/           # Arch Linux Pacman repository generator
    sysext/           # systemd-sysext repository generator
  models/             # Data models (Package, RepositoryConfig, errors)
  releaseasset/       # Release contract tests
  scanner/            # Package detection and file scanning
  signer/             # GPG and RSA signing utilities
  utils/              # Shared utilities (checksums, compression, file ops)
test/                 # Integration (e2e) tests and fixtures
```

## Skills (follow these for common tasks)

Step-by-step procedures live in [.agents/skills/](.agents/skills/); follow
them rather than improvising, whichever agent you are. They are synced from
frostyard/core — edit there, not here.

<!-- One bullet per skill: **When to use it** → [.agents/skills/<name>/SKILL.md]. -->

- **Creating or conforming a frostyard Go repository** →
  [.agents/skills/frostyard-go-repo/SKILL.md](.agents/skills/frostyard-go-repo/SKILL.md)
- **Scaffolding or updating the repo's docs/ tree** →
  [.agents/skills/frostyard-repo-docs/SKILL.md](.agents/skills/frostyard-repo-docs/SKILL.md)

## Working conventions (org-wide)

- Org-level conventions this repo follows are recorded as ADRs in
  frostyard/core — see [docs/org-adrs.md](docs/org-adrs.md) for the list
  that binds this repo. Change the ADR (in core) before changing behavior it
  covers.
- The org **squash-merges PRs**: branch every PR off `main`, never stack a
  branch on another PR's branch.
- A PR that touches a path in `.github/risk-paths.json` gets the label
  `risk:high` from the risk-paths caller
  ([core ADR-0068](https://github.com/frostyard/core/blob/main/docs/adr/0068-label-pull-requests-that-touch-high-risk-paths.md)).
  Never remove that label to make a PR ready: the author removes it only when
  the risky change leaves the PR, with the reason in the PR.
- Report a vulnerability privately, as [SECURITY.md](SECURITY.md) says; never
  in a public issue.

## Repo gotchas

- This repository is a GitHub fork of `ralt/repogen`, so every `gh` command
  must pass `--repo frostyard/repogen` explicitly or it may target the
  upstream parent.

## Releases and dependencies

Facts for hygiene work (`~/.pi/agent/skills/repo-hygiene/SKILL.md`); the
release process is in [RELEASING.md](RELEASING.md).

- **Trap: the highest semver tag is not the latest release.** Tags `v1.0.0`
  to `v1.0.4` come from the upstream fork `ralt/repogen` (2026-01-06 to
  2026-01-22) and have no GitHub release; `v1.0.4` is not on `main`. The real
  series is `v0.x`, from `v0.1.0` (2026-01-14); the latest release when this
  was written is `v0.7.2` (2026-11-03). So `git tag --sort=-v:refname | head -1`
  and the skill's "highest stable semver tag" fallback give a wrong answer here. Find the latest
  release with `gh release list --repo frostyard/repogen`, never from the tags,
  and count unreleased commits from that tag.
- Tag format: annotated `vMAJOR.MINOR.PATCH` ([RELEASING.md](RELEASING.md));
  one series, `v0.x`.
- How a release starts: the tag push runs `.github/workflows/release.yml`
  (GoReleaser `v2.18.1`), which publishes `repogen-linux-amd64`,
  `repogen-linux-arm64` and `SHA256SUMS`.
- Who tags: Brian. Agents never tag, push tags, run `make bump` or release.
- Version rule: `.svu.yml` sets `tag.prefix: "v"` and
  `tag.pattern: "v0.*"`, and `svu next` reads the Conventional Commit
  subjects since the latest release (core ADR-0012, ADR-0053). Without the
  pattern `svu next` answers `v2.0.0`, because the upstream fork's `v1.x`
  tags exist. On `v0.x` a breaking change gives a minor. `make bump` creates
  the annotated tag locally; a maintainer pushes it.
- Internal dependencies: none (`go.mod` has no frostyard or bketelsen module).
- Who uses this repository: `frostyard/snosi` `.github/workflows/build.yml`
  publishes sysexts with `frostyard/repogen/.github/actions/publish-to-r2`,
  pinned by SHA `b5dccc7` (`v0.7.2`), with `repogen-version: v0.7.2` and
  `repogen-commit` naming that same commit. A newer repogen release is not a
  bump to open in snosi on your own; ask its owner (Murbella) first. `frostyard/chairlift` and
  `frostyard/pilothouse` only name the action in pin-check test fixtures, not
  in a workflow. Debian packages are no longer published with repogen
  ([ADR-0015](docs/adr/0015-remove-the-superseded-production-path.md);
  frostyard/apt-publisher publishes them, core ADR-0055).

## Code conventions (live — the code exists)

- Follow standard Go conventions and idioms: meaningful names, small focused
  functions, godoc comments on exported functions and types,
  `context.Context` for cancellation where appropriate, structured logging
  with logrus.
- Handle errors explicitly; never ignore errors. Return errors rather than
  panicking; wrap with context using `fmt.Errorf("context: %w", err)`;
  custom error types live in `internal/models/errors.go`.
- Write unit tests for new functionality: table-driven where appropriate,
  covering success and error cases, in the same package (`*_test.go`).
- **Sysext signing:** when a GPG signer is configured, the sysext generator
  emits a detached binary `SHA256SUMS.gpg` beside each manifest and sets
  generated transfers to `Verify=true`. Unsigned generation remains
  supported and emits `Verify=false`.
- Conformance alias symlinks are listed in
  [ADR-0012](docs/adr/0012-acmm-conformance-via-canonical-aliases.md) —
  edit their canonical targets, never the aliases.
- CI gate: [.github/workflows/ci.yml](.github/workflows/ci.yml) runs
  `make ci` — `make verify` (tidy, vet, gofmt, lint at the mise pin, docs
  integrity, unit tests), a pinned `govulncheck` (a reachable vulnerability
  fails CI,
  [ADR-0016](docs/adr/0016-remove-rpm-generation.md)), the race tests, the
  packages built in Docker, the integration suite and the release builds.
  `node scripts/check-docs.mjs` checks that every doc is indexed, every
  relative link resolves and every symlink is intact — thresholds in
  `.coverage-thresholds.json`, `never_relax`. Run `make check` locally before
  pushing.
- Corrections go to `.memory/corrections.jsonl` (append-only five-field
  schema — see [.memory/README.md](.memory/README.md)); promote into this
  file, docs, or skills — never duplicate without setting `promoted_to`.
- Task runbooks live in [.github/prompts/](.github/prompts/README.md) as
  `*.prompt.md`; rules stay here.

## After every code change

After making any code changes, you MUST run:

```bash
make build   # code compiles
make check   # formats, then runs the verify gate: tidy, vet, gofmt, lint
             # at the mise pin, docs integrity, unit tests
```

`make ci` runs the full CI gate when Docker is available.

Fix any linting errors before considering the task complete. Common issues:
unused imports or variables, missing error checks, ineffective assignments,
formatting.

## Adding new package type support

1. Add detection logic in `internal/scanner/detector.go`
2. Add the new `PackageType` constant in `internal/scanner/scanner.go`
3. Create a new generator package under `internal/generator/<type>/`
4. Implement the `generator.Generator` interface:
   - `Generate(ctx, config, packages) error`
   - `ValidatePackages(packages) error`
   - `GetSupportedType() scanner.PackageType`
   - `ParseExistingMetadata(config) ([]Package, error)`
5. Register the generator in `internal/cli/generate.go`
6. Add package identity support in `internal/utils/package_identity.go`
7. Write comprehensive tests
8. Update README.md with new package type documentation

## Common make targets

`mise install` provisions Go (from `go.mod`) and the pinned `golangci-lint`,
`node` and `svu` (`mise.toml` plus `mise.lock`, core ADR-0043).

- `make build` — build the repogen binary
- `make test-unit` — run unit tests (fast)
- `make test` — run all tests including integration
- `make test-integration` — build test packages, then run the e2e suite in
  `test/` (see [test/e2e/README.md](test/e2e/README.md))
- `make fmt` — format code with go fmt
- `make lint` — run golangci-lint at the `mise.toml` pin
- `make verify` — the non-mutating gate: tidy, vet, gofmt check, lint, docs
  integrity, unit tests
- `make check` — `make fmt`, then `make verify` (the developer gate)
- `make ci` — what CI runs; it needs Docker
- `make vuln` — `govulncheck` at the pinned version
- `make test-race` — unit tests with the race detector and `coverage.txt`
- `make build-cross` — the linux amd64 and arm64 release builds
- `make bump` — create the annotated release tag locally (maintainers only;
  agents never run it and never push a tag)
- `make install` — install to /usr/local/bin
- `make clean` — clean build artifacts

## Documentation rules (enforced)

**Update documentation after any change to source code** — README.md and the
`docs/` tree. A task is not complete without reviewing and updating relevant
documentation.

All documentation lives in `docs/`, in the four-category shape defined by
[frostyard/core ADR-0025](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md)
(this tree replaced the former `yeti/` directory): `docs/adr/` (why),
`docs/design/` (how it fits together), `docs/specs/` (exact contracts),
`docs/plans/` (order of work) — see [docs/README.md](docs/README.md) for the
table, index, and conventions. Write these docs to be maximally useful to an
AI agent understanding the codebase — detailed architecture, patterns, and
decision rationale rather than user-facing guides.

- **README.md**: the root README follows core's README spec
  ([core ADR-0072](https://github.com/frostyard/core/blob/main/docs/adr/0072-write-every-readme-to-one-standard.md)):
  kind `tool`, at most 1000 words, a link out to the docs. Update it for new
  package types, CLI flags or commands, features or workflows, repository
  structure changes.
- **docs/guides/**: user guides live there until the docs site lands (core
  ADR-0070). They take no `TEMPLATE.md`, but each one is indexed in
  `docs/README.md`.
- **docs/**: update `docs/design/overview.md` and the relevant
  `docs/design/` or `docs/specs/` docs when architecture, data flow,
  generator output formats, or CI/CD change; index new docs in
  `docs/README.md`.
- Repo-local decisions get an ADR in `docs/adr/` (next free number, from the
  TEMPLATE); org-wide decisions go to frostyard/core and are listed in
  [docs/org-adrs.md](docs/org-adrs.md).
- **Every new doc starts from its category's `TEMPLATE.md`.** Cross-link
  between categories in both directions; adding a doc means adding it to the
  index in `docs/README.md`.
- Godoc comments on exported functions and types; brief inline comments only
  for non-obvious decisions, workarounds, or edge cases.

## Checklist before completing a task

- [ ] Code compiles (`make build`)
- [ ] `make check` passes (format, then the verify gate)
- [ ] Documentation updated if needed
- [ ] New features have tests
