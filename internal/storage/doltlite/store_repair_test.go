//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"testing"
)

func TestEnsureDoltliteWispDependenciesShapeMigratesLegacyRows(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open(driverName, ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close() //nolint:errcheck // test cleanup

	for _, stmt := range []string{
		`CREATE TABLE issues (id TEXT PRIMARY KEY)`,
		`CREATE TABLE wisps (id TEXT PRIMARY KEY)`,
		`CREATE TABLE wisp_dependencies (
			issue_id TEXT NOT NULL,
			depends_on_id TEXT NOT NULL,
			type TEXT DEFAULT 'blocks',
			created_at TEXT DEFAULT CURRENT_TIMESTAMP,
			created_by TEXT DEFAULT '',
			metadata TEXT DEFAULT '{}',
			thread_id TEXT DEFAULT '',
			PRIMARY KEY (issue_id, depends_on_id)
		)`,
		`INSERT INTO issues (id) VALUES ('bd-issue-target')`,
		`INSERT INTO wisps (id) VALUES ('bd-wisp-source'), ('bd-wisp-target')`,
		`INSERT INTO wisp_dependencies (issue_id, depends_on_id, type) VALUES
			('bd-wisp-source', 'bd-issue-target', 'blocks'),
			('bd-wisp-source', 'bd-wisp-target', 'waits-for'),
			('bd-wisp-source', 'external:ticket-1', 'tracks')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("setup: %v\nstmt: %s", err, stmt)
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := ensureDoltliteWispDependenciesShape(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("ensureDoltliteWispDependenciesShape: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	for _, column := range []string{"depends_on_issue_id", "depends_on_wisp_id", "depends_on_external"} {
		ok, err := testDoltliteColumnExists(ctx, db, "wisp_dependencies", column)
		if err != nil {
			t.Fatalf("column %s: %v", column, err)
		}
		if !ok {
			t.Fatalf("wisp_dependencies missing repaired column %s", column)
		}
	}

	assertWispDependencyTarget(t, ctx, db, "blocks", "bd-issue-target", "", "")
	assertWispDependencyTarget(t, ctx, db, "waits-for", "", "bd-wisp-target", "")
	assertWispDependencyTarget(t, ctx, db, "tracks", "", "", "external:ticket-1")
}

func testDoltliteColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func assertWispDependencyTarget(t *testing.T, ctx context.Context, db *sql.DB, depType, wantIssue, wantWisp, wantExternal string) {
	t.Helper()
	var issue, wisp, external sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT depends_on_issue_id, depends_on_wisp_id, depends_on_external
		FROM wisp_dependencies
		WHERE type = ?
	`, depType).Scan(&issue, &wisp, &external)
	if err != nil {
		t.Fatalf("read repaired dependency %s: %v", depType, err)
	}
	if issue.String != wantIssue || issue.Valid != (wantIssue != "") {
		t.Fatalf("%s issue target = %q valid=%v, want %q", depType, issue.String, issue.Valid, wantIssue)
	}
	if wisp.String != wantWisp || wisp.Valid != (wantWisp != "") {
		t.Fatalf("%s wisp target = %q valid=%v, want %q", depType, wisp.String, wisp.Valid, wantWisp)
	}
	if external.String != wantExternal || external.Valid != (wantExternal != "") {
		t.Fatalf("%s external target = %q valid=%v, want %q", depType, external.String, external.Valid, wantExternal)
	}
}
