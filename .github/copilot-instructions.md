# Copilot Instructions for Repogen

## Project Overview

Repogen is a CLI tool written in Go that generates static repository structures for multiple package managers. It scans directories for packages, generates appropriate metadata files, and signs repositories with GPG/RSA keys.

### Supported Package Types

- **Debian/APT** (.deb packages)
- **Yum/RPM** (.rpm packages)
- **Alpine/APK** (.apk packages)
- **Arch Linux/Pacman** (.pkg.tar.zst, .pkg.tar.xz, .pkg.tar.gz)
- **Homebrew** (bottle files)
- **systemd-sysext** (.raw, .raw.zst, .raw.xz, .raw.gz)

### Project Structure

```
cmd/repogen/          # CLI entry point
internal/
  cli/                # Command-line interface (Cobra commands)
  generator/          # Repository generators for each package type
    apk/              # Alpine APK repository generator
    deb/              # Debian APT repository generator
    homebrew/         # Homebrew bottle repository generator
    pacman/           # Arch Linux Pacman repository generator
    rpm/              # RPM/Yum repository generator
    sysext/           # systemd-sysext repository generator
  models/             # Data models (Package, RepositoryConfig, errors)
  scanner/            # Package detection and file scanning
  signer/             # GPG and RSA signing utilities
  utils/              # Shared utilities (checksums, compression, file ops)
test/                 # Integration tests and fixtures
```

## Go Development Best Practices

### Code Style

- Follow standard Go conventions and idioms
- Use meaningful variable and function names
- Keep functions focused and small
- Add comments for exported functions and types (godoc style)
- Use `context.Context` for cancellation where appropriate
- Handle errors explicitly; never ignore errors
- Use structured logging with logrus

### Error Handling

- Return errors rather than panicking
- Wrap errors with context using `fmt.Errorf("context: %w", err)`
- Define custom error types in `models/errors.go` when appropriate

### Testing

- Write unit tests for new functionality
- Use table-driven tests where appropriate
- Test both success and error cases
- Place tests in the same package as the code being tested (`*_test.go`)

## After Every Code Change

After making any code changes, you MUST run:

```bash
make build
```

Then format the code using:

```bash
make fmt
```

Then run the linter:

```bash
make lint
```

Fix any linting errors before considering the task complete. Common linting issues include:

- Unused imports or variables
- Missing error checks
- Ineffective assignments
- Formatting issues

## Documentation Requirements

When making changes, update relevant documentation. Architecture docs live in
the four-category `docs/` tree (formerly the `yeti/` directory) — `docs/adr/`,
`docs/design/`, `docs/specs/`, `docs/plans/` per
[frostyard/core ADR-0025](https://github.com/frostyard/core/blob/main/docs/adr/0025-consolidate-repository-docs-into-docs.md);
read `docs/design/overview.md` for codebase context and see `docs/README.md`
for the index and conventions.

1. **README.md**: Update if you add/modify:

   - New package type support
   - New CLI flags or commands
   - New features or workflows
   - Repository structure changes

2. **docs/**: Update `docs/design/overview.md` and the relevant
   `docs/design/` or `docs/specs/` docs when architecture, data flow,
   generator output formats, or CI/CD change; index new docs in
   `docs/README.md`.

3. **Code Comments**: Add/update godoc comments for:

   - Exported functions and types
   - Complex logic that needs explanation
   - Configuration options

4. **Inline Comments**: Add brief comments for:
   - Non-obvious code decisions
   - Workarounds or edge cases

## Adding New Package Type Support

When adding support for a new package type:

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

## Common Make Targets

- `make build` - Build the repogen binary
- `make test-unit` - Run unit tests (fast)
- `make test` - Run all tests including integration
- `make fmt` - Format code with go fmt
- `make lint` - Run golangci-lint
- `make install` - Install to /usr/local/bin
- `make clean` - Clean build artifacts

## Checklist Before Completing a Task

- [ ] Code compiles (`make build`)
- [ ] Code is formatted (`make fmt`)
- [ ] Linter passes (`make lint`)
- [ ] Tests pass (`make test-unit`)
- [ ] Documentation updated if needed
- [ ] New features have tests
