# End-to-end tests

Repogen's e2e suite is the integration suite in
[`test/integration_test.go`](../integration_test.go): it builds real
packages for every supported type (`test/build-test-packages.sh`, fixtures
in [`test/fixtures/`](../fixtures/)), runs the `repogen` binary end-to-end,
and asserts on the published repository output (metadata, checksums,
signatures) under `test/integration-output/`.

Run it with:

```bash
make test-integration          # builds test packages, then runs the suite
make test-integration-quick    # skips the package rebuild
```

This directory exists as the discoverable e2e entry point
([ADR-0012](../../docs/adr/0012-acmm-conformance-via-canonical-aliases.md));
the suite itself lives one level up so `go test ./test` keeps working
unchanged.
