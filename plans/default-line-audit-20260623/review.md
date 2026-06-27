# JJ Review Report

Verdict: changes_required

Reviewed change: `nwzzkuqzmnynzkzpmppszqnumpnrurqm` (`fix(doltlite): run compatible migrations for existing stores`)

Note: the bead metadata named source change `owrmnolqmwrxtokxnzyvzlomxmvmqusz`, but that revision was not present in this workspace. The document workspace is an empty review commit whose parent is the change above, so this report reviews that concrete parent change.

## Findings

### P1: SQLite upgrade path drops existing dependency rows

`MigrateSQLiteUpTo` executes the SQLite-compatible body for each pending migration on existing stores (`internal/storage/schema/sqlite_migrations.go:105-114`). For both `0041_split_dependencies_target.up.sql` and `0043_drop_dependencies_generated_column.up.sql`, `sqliteCompatibleMigrationSQL` returns `sqliteFinalDependenciesSchema` (`internal/storage/schema/sqlite_migrations.go:198-199`), and that schema starts with `DROP TABLE IF EXISTS dependencies` (`internal/storage/schema/sqlite_migrations.go:304`).

That means any existing DoltLite store upgrading from before migration 0041, or from 0041/0042 into 0043, loses all dependency records during startup. This breaks the change's stated goal of making existing stores compatible and can silently unblock or detach beads from their blockers. The compatibility migration needs to preserve rows while reshaping the table, or use an idempotent copy/rename flow with a regression test that seeds dependencies before the migration and verifies they survive after `MigrateSQLiteUpTo`.

## Validation

- `go test ./internal/storage/schema ./internal/storage/sqlbuild ./internal/storage/doltlite`
- Result: schema and sqlbuild packages passed; `internal/storage/doltlite` failed in this environment because DoltLite SQL did not provide `dolt_commit` during migration commit.

