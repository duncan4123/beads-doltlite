# Implementation Plan: DoltLite Query/Write Audit

## Summary

Audit and harden the DoltLite backend paths used by controller-formula and
Gas City workers so read queries stay compatible with DoltLite's
SQLite-compatible SQL surface and write operations consistently update derived
state, dirty-table tracking, and Dolt version history.

The implementation should keep Beads inside the existing storage boundary:
Beads routes behavior through `internal/storage` interfaces and shared
`issueops` helpers. DoltLite-specific handling belongs in the DoltLite storage
adapter only when it is about connection lifecycle, SQLite-compatible SQL, native
DoltLite commits, or local schema compatibility.

## Current System

- `internal/storage/doltlite` implements the CGO-only DoltLite store.
- `DoltliteStore.withConn` opens a transaction around each operation and now
  uses a bounded exclusive lock/retry path for write transactions.
- `RunInTransaction` passes an `embeddedTransaction` that marks dirty logical
  tables and commits DoltLite history after successful SQL transaction commit.
- Shared issue mutation logic lives under `internal/storage/issueops`.
- Several issueops paths already have SQLite-compatible variants, including
  update, close, delete, dependency add/remove, ready/search/count, and blocked
  recomputation helpers.
- Existing Dolt/server SQL paths must remain available for the embedded Dolt
  and server-backed storage adapters.

Important constraints:

- Do not add orchestration policy to Beads core.
- Do not leak DoltLite internals through public storage return types.
- Do not add Beads-side crash recovery or engine introspection outside the
  storage interface.
- Do not rely on a Dolt SQL server, runtime port, or server-state file for
  DoltLite behavior.
- Keep maintenance bounded and non-fatal unless correctness requires a hard
  failure.

## Proposed Implementation

1. Route DoltLite read/write methods through SQLite-compatible issueops helpers.

   Update DoltLite store methods so every query or mutation that touches issue,
   dependency, ready, count, search, close, update, delete, or wisp-derived state
   uses the helper variant that is valid against DoltLite's SQLite-compatible
   engine. Keep generic issueops behavior shared where it is SQL-neutral.

2. Preserve derived-state correctness for all write paths.

   Ensure each DoltLite mutation recomputes or marks `is_blocked` through the
   SQLite-compatible helpers. Cover these mutation classes:

   - issue status changes that can unblock or block dependents
   - dependency add/remove, including wisp and cross-prefix targets
   - issue delete, including cascade and forced orphaning paths
   - close/reopen/update flows that write event history

3. Maintain DirtyTableTracker coverage inside `embeddedTransaction`.

   For transaction-scoped writes, mark every logical table that can be touched by
   the operation. This includes direct tables such as `issues`, `dependencies`,
   `events`, `labels`, and their wisp counterparts when a shared helper can route
   between regular issues and wisps. Do not overfit dirty tracking to a single
   branch of wisp routing when the helper may touch both table families.

4. Keep DoltLite transaction behavior bounded and retryable.

   Keep write transactions behind `withExclusiveLock` and the bounded retry
   wrapper. Refresh the persistent connection after retryable concurrency errors
   so stale prepared/catalog state does not poison subsequent attempts. Reads
   should avoid the exclusive lock path.

5. Repair local DoltLite schema compatibility during open.

   During DoltLite store initialization, run the SQLite migration path for fresh
   or existing stores, commit native schema changes when needed, and repair local
   DoltLite-only table shape where safe. For legacy local wisp dependency tables,
   only perform automatic shape repair when the table is empty; otherwise fail
   with an explicit error instead of silently rewriting user data.

6. Keep the shared SQL-builder boundary clean.

   Put SQL expressions that must be shared between issueops and query builders
   in `internal/storage/sqlbuild`. Keep DoltLite-specific branch decisions in the
   adapter and keep storage-neutral dependency target logic in issueops.

## Testing

Required tests:

- Unit tests for SQLite-compatible issueops helpers covering update, delete,
  dependency add/remove, and status-change derived-state recomputation.
- DoltLite smoke or integration tests proving an existing store opens,
  migrates, and can run representative ready/search/count/query flows.
- Tests for `wisp_dependencies` compatibility repair:
  - no-op when the split target columns already exist
  - empty legacy table is repaired
  - non-empty legacy table fails with a clear error
- Tests that DoltLite writes create native commits only when tracked dirty tables
  changed.
- Regression tests for concurrent write retry behavior around lock/catalog
  errors where practical without making the suite flaky.

Validation commands:

```bash
make test
CGO_ENABLED=1 go test -tags gms_pure_go ./internal/storage/doltlite ./internal/storage/issueops ./internal/storage/sqlbuild ./cmd/bd/...
```

The CGO command is intentionally scoped to the affected DoltLite and CLI storage
surface. Broader shipped-config CGO runs can be added by maintainers if the
review uncovers cross-package risk.

## Rollout

- Land behind the existing DoltLite backend selection; no new CLI or config
  surface is required.
- Keep behavior compatible for existing `.beads/doltlite/*.db` stores by
  running compatible migrations at open.
- Fail fast on unsafe local schema repair and report the table/shape that needs
  manual intervention.
- Do not change the default non-DoltLite storage behavior.

## Open Questions

- Should the DoltLite smoke tests create a fixture database at an older schema
  version, or is constructing the legacy table shape in-test sufficient?
- Should direct `bd sql` coverage be added for the same ready/search/count cases
  to catch query-builder drift separately from typed store methods?
- Is the dirty-table set for transaction-scoped import/create operations
  intentionally broad, or should a follow-up narrow it after wisp routing is
  made explicit in the helper return values?
