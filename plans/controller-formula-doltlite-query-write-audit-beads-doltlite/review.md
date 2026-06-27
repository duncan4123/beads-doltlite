# JJ Review Report

Verdict: changes_required

Reviewed change: `rqnqrpvsklwsmvouqoqmnummsllwqzkk` (`decomposition: record DoltLite audit work breakdown`)

## Findings

### P1: Ordinary DoltLite package tests now silently skip

The new package-level `TestMain` in `internal/storage/doltlite/native_test.go:16` exits with status 0 when `dolt_version()` is unavailable (`internal/storage/doltlite/native_test.go:17-20`). Because this repository's `Makefile` exports `CGO_ENABLED=1` globally (`Makefile:33`), `native_test.go` is included in ordinary runs such as `go test ./internal/storage/doltlite`, not only in the new `make test-doltlite` target.

On this host, `go test -count=1 -v ./internal/storage/doltlite` now prints the native-link skip message and exits `ok` without running the existing smoke tests in `smoke_test.go`. That creates a false green for the whole DoltLite package anywhere libdoltlite is not linked into the default sqlite driver, and it reduces coverage for the non-native path while presenting the package as passing.

The native-link probe should be isolated from the ordinary package suite, for example behind a dedicated build tag used only by `make test-doltlite`, or converted into an explicit test that calls `t.Skip` without a package-wide `os.Exit(0)` gate. The existing smoke tests should continue to fail or pass on their own merits under normal test commands.

## Validation

- `go test -count=1 -v ./internal/storage/doltlite`
- Result: exited `ok` after printing `SKIP internal/storage/doltlite: libdoltlite SQL functions are not linked into the sqlite driver: no such function: dolt_version`; no individual smoke tests ran.
- `make test-doltlite`
- Result: passed in 15.172s using the default linked library path.
