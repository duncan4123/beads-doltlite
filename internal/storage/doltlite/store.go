//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"github.com/steveyegge/beads/internal/types"
)

// Compile-time interface checks.
var _ storage.DoltStorage = (*DoltliteStore)(nil)
var _ storage.RawDBAccessor = (*DoltliteStore)(nil)
var _ storage.StoreLocator = (*DoltliteStore)(nil)
var _ storage.GarbageCollector = (*DoltliteStore)(nil)
var _ storage.Flattener = (*DoltliteStore)(nil)
var _ storage.Compactor = (*DoltliteStore)(nil)

// DoltliteStore implements storage.DoltStorage backed by the doltlite engine.
// Each method call opens a short-lived connection, executes within an explicit
// SQL transaction, and closes the connection immediately. This minimizes the
// time the embedded engine's write lock is held, reducing contention when
// multiple processes access the same database concurrently.
//
// Schema bootstrap is protected by a short exclusive flock. Normal operations
// rely on doltlite's file-level locking and conflict detection so multiple bd
// processes can read concurrently and serialize writes.
type DoltliteStore struct {
	dataDir       string
	beadsDir      string
	database      string
	branch        string
	credentialKey []byte
	dbMu          sync.Mutex
	db            *sql.DB
	dbCleanup     func() error
	closed        atomic.Bool
}

// errClosed is returned when a method is called after Close.
var errClosed = errors.New("doltlite: store is closed")

// Option configures optional behavior for New.
type Option func(*options)

type options struct {
	lock Unlocker // pre-acquired lock; nil means New acquires its own
}

// WithLock passes a pre-acquired exclusive lock to New for schema bootstrap.
// The caller retains ownership. Normal store operations do not hold this lock.
func WithLock(lock Unlocker) Option {
	return func(o *options) { o.lock = lock }
}

// New creates an DoltliteStore using the doltlite engine.
// beadsDir is the .beads/ root; the data directory is derived as <beadsDir>/doltlite/.
// The database is created automatically if it doesn't exist (initSchema handles this).
//
// Schema bootstrap is guarded by a short exclusive flock. After bootstrap, the
// lock is released and normal operations use doltlite's own file-level locks.
func New(ctx context.Context, beadsDir, database, branch string, opts ...Option) (*DoltliteStore, error) {
	if database == "" {
		return nil, fmt.Errorf("doltlite: database name must not be empty (caller should default to %q)", "beads")
	}

	var o options
	for _, fn := range opts {
		fn(&o)
	}

	// Resolve to absolute path so the SQLite database path is stable across
	// callers with different working directories.
	absBeadsDir, err := filepath.Abs(beadsDir)
	if err != nil {
		return nil, fmt.Errorf("doltlite: resolving beads dir: %w", err)
	}
	dataDir := filepath.Join(absBeadsDir, "doltlite")
	if err := os.MkdirAll(dataDir, config.BeadsDirPerm); err != nil {
		return nil, fmt.Errorf("doltlite: creating data directory: %w", err)
	}

	lock := o.lock
	ownsLock := lock == nil
	if ownsLock {
		var err error
		lock, err = WaitLock(ctx, dataDir)
		if err != nil {
			return nil, err
		}
	}
	s := &DoltliteStore{
		dataDir:  dataDir,
		beadsDir: absBeadsDir,
		database: database,
		branch:   branch,
	}

	if err := s.initSchema(ctx); err != nil {
		if lock != nil && ownsLock {
			lock.Unlock()
		}
		return nil, fmt.Errorf("doltlite: init schema: %w", err)
	}
	if lock != nil && ownsLock {
		lock.Unlock()
		lock = nil
	}
	if err := s.openPersistentDB(ctx); err != nil {
		return nil, fmt.Errorf("doltlite: open database: %w", err)
	}

	// Backfill custom_types / custom_statuses from config values,
	// fixing databases where schema migration created empty tables.
	if err := s.backfillCustomTables(ctx); err != nil {
		return nil, fmt.Errorf("doltlite: backfill custom tables: %w", err)
	}

	if s.branch == "" {
		branch, err := s.CurrentBranch(ctx)
		if err != nil {
			return nil, fmt.Errorf("doltlite: get current branch: %w", err)
		}
		s.branch = branch
	}
	// Ensure dolt_ignore'd wisp tables exist in the working set.
	// After a clone or branch switch, these tables are absent because
	// dolt_ignore prevents them from being committed. Server mode handles
	// this in newServerMode(); embedded mode must do it here. (GH#3270)
	if err := s.ensureIgnoredTables(ctx); err != nil {
		return nil, fmt.Errorf("doltlite: ensure ignored tables: %w", err)
	}

	return s, nil
}

func (s *DoltliteStore) openPersistentDB(ctx context.Context) error {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	if s.db != nil {
		return nil
	}
	var db *sql.DB
	var cleanup func() error
	if err := s.withRetry(ctx, func() error {
		var err error
		db, cleanup, err = OpenSQL(ctx, s.dataDir, s.database, "")
		return err
	}); err != nil {
		return err
	}
	s.db = db
	s.dbCleanup = cleanup
	return nil
}

func (s *DoltliteStore) activeDB(ctx context.Context) (*sql.DB, func() error, error) {
	s.dbMu.Lock()
	db := s.db
	s.dbMu.Unlock()
	if db != nil {
		return db, func() error { return nil }, nil
	}
	var cleanup func() error
	if err := s.withRetry(ctx, func() error {
		var err error
		db, cleanup, err = OpenSQL(ctx, s.dataDir, s.database, "")
		return err
	}); err != nil {
		return nil, nil, err
	}
	return db, cleanup, nil
}

// DB returns the persistent DoltLite SQL connection for direct queries.
// Use sparingly; prefer the store's typed methods for normal operations.
func (s *DoltliteStore) DB() *sql.DB {
	s.dbMu.Lock()
	defer s.dbMu.Unlock()
	return s.db
}

// UnderlyingDB returns the persistent DoltLite SQL connection for diagnostics
// and raw SQL maintenance commands.
func (s *DoltliteStore) UnderlyingDB() *sql.DB {
	return s.DB()
}

// withRootConn opens a short-lived database connection without selecting any
// database or branch, begins an explicit SQL transaction, and passes it to fn.
// This is used during initialization when the database may not yet exist.
func (s *DoltliteStore) withRootConn(ctx context.Context, commit bool, fn func(tx *sql.Tx) error) (err error) {
	if commit {
		return s.withExclusiveLock(ctx, func() error {
			return s.withRetry(ctx, func() error {
				return s.withRootConnOnce(ctx, commit, fn)
			})
		})
	}
	return s.withRootConnOnce(ctx, commit, fn)
}

func (s *DoltliteStore) withRootConnOnce(ctx context.Context, commit bool, fn func(tx *sql.Tx) error) (err error) {
	if s.closed.Load() {
		err = errClosed
		return
	}

	var db *sql.DB
	var cleanup func() error
	db, cleanup, err = OpenSQL(ctx, s.dataDir, "", "")
	if err != nil {
		return
	}

	defer func() {
		err = errors.Join(err, cleanup())
	}()

	var tx *sql.Tx
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		err = fmt.Errorf("doltlite: begin tx: %w", err)
		return
	}

	err = fn(tx)
	if err != nil {
		err = errors.Join(err, tx.Rollback())
		return
	}

	if !commit {
		return tx.Rollback()
	}

	err = tx.Commit()
	return
}

// withConn opens a short-lived database connection configured for the store's
// database and branch, begins an explicit SQL transaction, and passes it to
// fn. If commit is true and fn returns nil, the transaction is committed;
// otherwise it is rolled back. The connection is closed before withConn
// returns regardless of outcome.
//
// The database must already exist (created during initSchema).
func (s *DoltliteStore) withConn(ctx context.Context, commit bool, fn func(tx *sql.Tx) error) (err error) {
	if commit {
		return s.withExclusiveLock(ctx, func() error {
			return s.withRetryRefreshingDB(ctx, func() error {
				return s.withConnOnce(ctx, commit, fn)
			})
		})
	}
	return s.withConnOnce(ctx, commit, fn)
}

func (s *DoltliteStore) withConnOnce(ctx context.Context, commit bool, fn func(tx *sql.Tx) error) (err error) {
	if s.closed.Load() {
		err = errClosed
		return
	}

	var db *sql.DB
	var cleanup func() error
	db, cleanup, err = s.activeDB(ctx)
	if err != nil {
		return
	}

	defer func() {
		err = errors.Join(err, cleanup())
	}()

	var tx *sql.Tx
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		err = fmt.Errorf("doltlite: begin tx: %w", err)
		return
	}

	err = fn(tx)
	if err != nil {
		err = errors.Join(err, tx.Rollback())
		return
	}

	if !commit {
		return tx.Rollback()
	}

	err = tx.Commit()
	return
}

func (s *DoltliteStore) withRetry(ctx context.Context, fn func() error) error {
	return s.withRetryAfter(ctx, fn, nil)
}

func (s *DoltliteStore) withRetryRefreshingDB(ctx context.Context, fn func() error) error {
	return s.withRetryAfter(ctx, fn, s.resetPersistentDB)
}

func (s *DoltliteStore) withRetryAfter(ctx context.Context, fn func() error, afterRetryable func()) error {
	const maxAttempts = 5
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = fn(); err == nil {
			return nil
		}
		if !isRetryableConcurrencyError(err) {
			return err
		}
		if afterRetryable != nil {
			afterRetryable()
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(time.Duration(50*(1<<attempt)) * time.Millisecond):
		}
	}
	return err
}

func (s *DoltliteStore) resetPersistentDB() {
	s.dbMu.Lock()
	cleanup := s.dbCleanup
	s.db = nil
	s.dbCleanup = nil
	s.dbMu.Unlock()
	if cleanup != nil {
		_ = cleanup()
	}
}

func (s *DoltliteStore) withExclusiveLock(ctx context.Context, fn func() error) error {
	lock, err := WaitLock(ctx, s.dataDir)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	return fn()
}

func isRetryableConcurrencyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "another connection committed") ||
		strings.Contains(msg, "please retry your transaction") ||
		strings.Contains(msg, "failed to prepare catalog")
}

// initSchema creates the database (if needed) and runs all pending migrations,
// committing them to Dolt history. Uses withRootConn so the database can be
// created before USE; this avoids running CREATE DATABASE inside withConn,
// which is not safe for concurrent use in the doltlite engine.
//
// After the schema-migration transaction commits, a fresh *sql.DB is opened
// and used to drive the idempotent compat-migration runner. Mirrors the
// server-mode open path in dolt/store.go:initSchemaOnDB and repairs
// pre-existing embedded databases that predate the embedded migration
// system's full coverage (GH#3412).
func (s *DoltliteStore) initSchema(ctx context.Context) error {
	if s.database != "" && !validIdentifier.MatchString(s.database) {
		return fmt.Errorf("doltlite: invalid database name: %q", s.database)
	}

	var db *sql.DB
	var cleanup func() error
	if err := s.withRetry(ctx, func() error {
		var err error
		db, cleanup, err = OpenSQL(ctx, s.dataDir, s.database, "")
		return err
	}); err != nil {
		return fmt.Errorf("doltlite: open for schema init: %w", err)
	}
	defer func() { _ = cleanup() }()

	currentVersion, err := schema.CurrentVersion(ctx, db)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such table") {
			currentVersion = 0
		} else {
			return err
		}
	}
	var applied int
	if currentVersion == 0 {
		applied, err = schema.MigrateFreshSQLite(ctx, db, schema.LatestVersion())
	} else {
		applied, err = schema.MigrateSQLiteUpTo(ctx, db, schema.LatestVersion())
	}
	if err != nil {
		return err
	}
	if applied > 0 {
		if err := commitAllNative(ctx, db, "schema: apply migrations"); err != nil {
			return fmt.Errorf("commit migration: %w", err)
		}
	}

	return nil
}

// ensureIgnoredTables creates dolt_ignore'd wisp tables if they don't exist.
// Uses withConn (not withRootConn) because the database is already created.
func (s *DoltliteStore) ensureIgnoredTables(ctx context.Context) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		currentVersion, err := schema.CurrentVersion(ctx, tx)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "no such table") {
				currentVersion = 0
			} else {
				return err
			}
		}
		if currentVersion == 0 {
			_, err = schema.MigrateFreshSQLite(ctx, tx, schema.LatestVersion())
		} else {
			_, err = schema.MigrateSQLiteUpTo(ctx, tx, schema.LatestVersion())
		}
		if err != nil {
			return err
		}
		return ensureDoltliteLocalSchemaCompat(ctx, tx)
	})
}

func ensureDoltliteLocalSchemaCompat(ctx context.Context, tx *sql.Tx) error {
	if err := ensureDoltliteColumn(ctx, tx, "wisps", "is_blocked", "ALTER TABLE wisps ADD COLUMN is_blocked TINYINT(1) NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_wisps_is_blocked ON wisps(is_blocked, status)"); err != nil {
		return fmt.Errorf("creating idx_wisps_is_blocked: %w", err)
	}
	if err := ensureDoltliteWispDependenciesShape(ctx, tx); err != nil {
		return err
	}
	return nil
}

func ensureDoltliteWispDependenciesShape(ctx context.Context, tx *sql.Tx) error {
	hasSplitTarget, err := doltliteColumnExists(ctx, tx, "wisp_dependencies", "depends_on_issue_id")
	if err != nil {
		return fmt.Errorf("checking wisp_dependencies shape: %w", err)
	}
	if hasSplitTarget {
		return nil
	}

	var rows int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM wisp_dependencies").Scan(&rows); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "no such table") {
			return fmt.Errorf("counting legacy wisp_dependencies rows: %w", err)
		}
	}
	if rows == 0 {
		if _, err := tx.ExecContext(ctx, sqliteWispDependenciesSchema("wisp_dependencies")); err != nil {
			return fmt.Errorf("repairing wisp_dependencies schema: %w", err)
		}
		return nil
	}

	if err := migrateLegacyDoltliteWispDependencies(ctx, tx); err != nil {
		return fmt.Errorf("repairing legacy wisp_dependencies schema with %d rows: %w", rows, err)
	}
	return nil
}

func migrateLegacyDoltliteWispDependencies(ctx context.Context, tx *sql.Tx) error {
	columns, err := doltliteColumns(ctx, tx, "wisp_dependencies")
	if err != nil {
		return err
	}
	if !columns["depends_on_id"] {
		return fmt.Errorf("legacy wisp_dependencies missing depends_on_id")
	}

	const tmpTable = "wisp_dependencies_doltlite_repair"
	if _, err := tx.ExecContext(ctx, sqliteWispDependenciesSchema(tmpTable)); err != nil {
		return fmt.Errorf("creating repair table: %w", err)
	}

	target := "NULLIF(depends_on_id, '')"
	wispExists := "EXISTS (SELECT 1 FROM wisps w WHERE w.id = " + target + ")"
	issueExists := "EXISTS (SELECT 1 FROM issues i WHERE i.id = " + target + ")"
	idExpr := doltliteLegacyColumnValue(columns, "id", "issue_id || ':' || "+target)
	typeExpr := doltliteLegacyColumnValue(columns, "type", "'blocks'")
	createdAtExpr := doltliteLegacyColumnValue(columns, "created_at", "CURRENT_TIMESTAMP")
	createdByExpr := doltliteLegacyColumnValue(columns, "created_by", "''")
	metadataExpr := doltliteLegacyColumnValue(columns, "metadata", "'{}'")
	threadIDExpr := doltliteLegacyColumnValue(columns, "thread_id", "''")

	// #nosec G201 -- interpolated fragments are fixed schema identifiers and expressions from this migration.
	copySQL := fmt.Sprintf(`
INSERT OR IGNORE INTO %[1]s (
    id, issue_id, depends_on_issue_id, depends_on_wisp_id, depends_on_external,
    type, created_at, created_by, metadata, thread_id
)
SELECT
    %[2]s,
    issue_id,
    CASE WHEN %[3]s NOT LIKE 'external:%%' AND NOT (%[4]s) AND (%[5]s) THEN %[3]s ELSE NULL END,
    CASE WHEN %[3]s NOT LIKE 'external:%%' AND (%[4]s) THEN %[3]s ELSE NULL END,
    CASE WHEN %[3]s LIKE 'external:%%' OR (NOT (%[4]s) AND NOT (%[5]s)) THEN %[3]s ELSE NULL END,
    %[6]s,
    %[7]s,
    %[8]s,
    %[9]s,
    %[10]s
FROM wisp_dependencies
WHERE NULLIF(issue_id, '') IS NOT NULL
  AND %[3]s IS NOT NULL
`, tmpTable, idExpr, target, wispExists, issueExists, typeExpr, createdAtExpr, createdByExpr, metadataExpr, threadIDExpr)
	if _, err := tx.ExecContext(ctx, copySQL); err != nil {
		return fmt.Errorf("copying legacy rows: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE wisp_dependencies"); err != nil {
		return fmt.Errorf("dropping legacy table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "ALTER TABLE "+tmpTable+" RENAME TO wisp_dependencies"); err != nil {
		return fmt.Errorf("renaming repair table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqliteWispDependenciesIndexes("wisp_dependencies")); err != nil {
		return fmt.Errorf("creating repaired indexes: %w", err)
	}
	return nil
}

func doltliteLegacyColumnValue(columns map[string]bool, column, fallback string) string {
	if !columns[column] {
		return fallback
	}
	return fmt.Sprintf("COALESCE(NULLIF(%s, ''), %s)", column, fallback)
}

func sqliteWispDependenciesSchema(table string) string {
	return fmt.Sprintf(`DROP TABLE IF EXISTS %[1]s;
CREATE TABLE %[1]s (
    id CHAR(36) NOT NULL PRIMARY KEY,
    issue_id VARCHAR(255) NOT NULL,
    depends_on_issue_id VARCHAR(255) NULL,
    depends_on_wisp_id VARCHAR(255) NULL,
    depends_on_external VARCHAR(255) NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'blocks',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_by VARCHAR(255) DEFAULT '',
    metadata TEXT DEFAULT '{}',
    thread_id VARCHAR(255) DEFAULT '',
    UNIQUE (issue_id, depends_on_issue_id),
    UNIQUE (issue_id, depends_on_wisp_id),
    UNIQUE (issue_id, depends_on_external)
);
%[2]s`, table, sqliteWispDependenciesIndexes(table))
}

func sqliteWispDependenciesIndexes(table string) string {
	return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_issue ON %[1]s (type, depends_on_issue_id);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_wisp ON %[1]s (type, depends_on_wisp_id);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_external ON %[1]s (type, depends_on_external);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_wisp_target ON %[1]s (depends_on_wisp_id);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_issue_target ON %[1]s (depends_on_issue_id);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_external_target ON %[1]s (depends_on_external);`, table)
}

func ensureDoltliteColumn(ctx context.Context, tx *sql.Tx, table, column, alterSQL string) error {
	hasColumn, err := doltliteColumnExists(ctx, tx, table, column)
	if err != nil {
		return err
	}
	if hasColumn {
		return nil
	}
	if _, err := tx.ExecContext(ctx, alterSQL); err != nil {
		return fmt.Errorf("adding %s.%s: %w", table, column, err)
	}
	return nil
}

func doltliteColumnExists(ctx context.Context, tx *sql.Tx, table, column string) (bool, error) {
	columns, err := doltliteColumns(ctx, tx, table)
	if err != nil {
		return false, err
	}
	return columns[column], nil
}

func doltliteColumns(ctx context.Context, tx *sql.Tx, table string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return nil, fmt.Errorf("reading %s columns: %w", table, err)
	}
	defer rows.Close()
	columns := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, fmt.Errorf("scanning %s columns: %w", table, err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading %s columns: %w", table, err)
	}
	return columns, nil
}

// GetIssue is implemented in get_issue.go.

func (s *DoltliteStore) GetIssueByExternalRef(ctx context.Context, externalRef string) (*types.Issue, error) {
	var id string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		id, err = issueops.GetIssueByExternalRefInTx(ctx, tx, externalRef)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetIssue(ctx, id)
}

// GetIssuesByIDs is implemented in dependencies.go.

// UpdateIssue is implemented in issues.go.

// CloseIssue is implemented in issues.go.

func (s *DoltliteStore) DeleteIssue(ctx context.Context, id string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.DeleteIssueSQLiteInTx(ctx, tx, id)
	})
}

// AddDependency is implemented in dependencies.go.

// RemoveDependency is implemented in dependencies.go.

func (s *DoltliteStore) GetDependencies(ctx context.Context, issueID string) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetDependenciesInTx(ctx, tx, issueID)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetDependents(ctx context.Context, issueID string) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetDependentsInTx(ctx, tx, issueID)
		return err
	})
	return result, err
}

// GetDependenciesWithMetadata is implemented in dependencies.go.

// GetDependentsWithMetadata is implemented in dependencies.go.

func (s *DoltliteStore) GetDependencyTree(ctx context.Context, issueID string, maxDepth int, showAllPaths bool, reverse bool) ([]*types.TreeNode, error) {
	var result []*types.TreeNode
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetDependencyTreeInTx(ctx, tx, issueID, maxDepth, showAllPaths, reverse)
		return err
	})
	return result, err
}

// AddLabel is implemented in labels.go.

// RemoveLabel is implemented in labels.go.

// GetLabels is implemented in labels.go.

func (s *DoltliteStore) GetIssuesByLabel(ctx context.Context, label string) ([]*types.Issue, error) {
	var ids []string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		ids, err = issueops.GetIssuesByLabelInTx(ctx, tx, label)
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.GetIssuesByIDs(ctx, ids)
}

// GetReadyWork is implemented in queries.go.

func (s *DoltliteStore) GetBlockedIssues(ctx context.Context, filter types.WorkFilter) ([]*types.BlockedIssue, error) {
	var result []*types.BlockedIssue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetBlockedIssuesInTx(ctx, tx, filter)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetEpicsEligibleForClosure(ctx context.Context) ([]*types.EpicStatus, error) {
	var result []*types.EpicStatus
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetEpicsEligibleForClosureInTx(ctx, tx)
		return err
	})
	return result, err
}

func (s *DoltliteStore) AddIssueComment(ctx context.Context, issueID, author, text string) (*types.Comment, error) {
	var result *types.Comment
	err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.AddIssueCommentInTx(ctx, tx, issueID, author, text)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetIssueComments(ctx context.Context, issueID string) ([]*types.Comment, error) {
	var result []*types.Comment
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetIssueCommentsInTx(ctx, tx, issueID)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetEvents(ctx context.Context, issueID string, limit int) ([]*types.Event, error) {
	var result []*types.Event
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetEventsInTx(ctx, tx, issueID, limit)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetAllEventsSince(ctx context.Context, since time.Time) ([]*types.Event, error) {
	var result []*types.Event
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetAllEventsSinceInTx(ctx, tx, since)
		return err
	})
	return result, err
}

// RunInTransaction is implemented in transaction.go.

// Close marks the store as closed and cleans up orphaned git-remote-cache
// garbage. Subsequent method calls will return errClosed.
func (s *DoltliteStore) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		s.dbMu.Lock()
		cleanup := s.dbCleanup
		s.db = nil
		s.dbCleanup = nil
		s.dbMu.Unlock()
		if cleanup != nil {
			_ = cleanup()
		}
		s.cleanGitRemoteCacheGarbage()
	}
	return nil
}

// DoltGC runs Dolt garbage collection to reclaim disk space.
func (s *DoltliteStore) DoltGC(ctx context.Context) error {
	return s.withDBConn(ctx, func(db versioncontrolops.DBConn) error {
		if _, err := db.ExecContext(ctx, "SELECT dolt_gc()"); err != nil {
			return fmt.Errorf("doltlite gc: %w", err)
		}
		return nil
	})
}

// Flatten squashes all doltlite commit history into a single commit.
func (s *DoltliteStore) Flatten(ctx context.Context) error {
	return s.withDBWrite(ctx, func(db versioncontrolops.DBConn) error {
		var initialHash string
		if err := db.QueryRowContext(ctx,
			"SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1",
		).Scan(&initialHash); err != nil {
			return fmt.Errorf("find initial commit: %w", err)
		}

		var commitCount int
		if err := db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM dolt_log",
		).Scan(&commitCount); err != nil {
			return fmt.Errorf("count commits: %w", err)
		}
		if commitCount <= 1 {
			return nil
		}

		steps := []struct {
			name  string
			query string
			args  []any
		}{
			{"create temp branch", "SELECT dolt_branch('flatten-tmp')", nil},
			{"checkout temp branch", "SELECT dolt_checkout('flatten-tmp')", nil},
			{"soft reset to initial", "SELECT dolt_reset('--soft', ?)", []any{initialHash}},
			{"commit flattened snapshot", "SELECT dolt_commit('-A', '-m', 'flatten: squash all history into single commit')", nil},
			{"checkout main", "SELECT dolt_checkout('main')", nil},
			{"reset main to flattened", "SELECT dolt_reset('--hard', 'flatten-tmp')", nil},
			{"delete temp branch", "SELECT dolt_branch('-D', 'flatten-tmp')", nil},
		}
		for _, step := range steps {
			if _, err := db.ExecContext(ctx, step.query, step.args...); err != nil {
				return fmt.Errorf("flatten step %q: %w", step.name, err)
			}
		}
		return nil
	})
}

// Compact squashes old doltlite commits while preserving recent ones.
func (s *DoltliteStore) Compact(ctx context.Context, initialHash, boundaryHash string, oldCommits int, recentHashes []string) error {
	return s.withDBWrite(ctx, func(db versioncontrolops.DBConn) (retErr error) {
		branchCreated := false
		defer func() {
			if retErr != nil && branchCreated {
				_, _ = db.ExecContext(ctx, "SELECT dolt_checkout('main')")
				_, _ = db.ExecContext(ctx, "SELECT dolt_branch('-D', 'compact-tmp')")
			}
		}()

		execSQL := func(name, query string, args ...any) error {
			if _, err := db.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("compact step %q: %w", name, err)
			}
			return nil
		}

		if err := execSQL("create temp branch", "SELECT dolt_branch('compact-tmp', ?)", boundaryHash); err != nil {
			return err
		}
		branchCreated = true

		if err := execSQL("checkout temp", "SELECT dolt_checkout('compact-tmp')"); err != nil {
			return err
		}
		if err := execSQL("soft reset to initial", "SELECT dolt_reset('--soft', ?)", initialHash); err != nil {
			return err
		}
		msg := fmt.Sprintf("compact: squash %d commits into base snapshot", oldCommits)
		if err := execSQL("commit squashed base", "SELECT dolt_commit('-A', '-m', ?)", msg); err != nil {
			return err
		}

		for _, hash := range recentHashes {
			label := hash
			if len(label) > 8 {
				label = label[:8]
			}
			if err := execSQL("cherry-pick "+label, "SELECT dolt_cherry_pick(?)", hash); err != nil {
				return err
			}
		}

		if err := execSQL("checkout main", "SELECT dolt_checkout('main')"); err != nil {
			return err
		}
		if err := execSQL("reset main to compacted", "SELECT dolt_reset('--hard', 'compact-tmp')"); err != nil {
			return err
		}
		if err := execSQL("delete temp branch", "SELECT dolt_branch('-D', 'compact-tmp')"); err != nil {
			return err
		}

		return nil
	})
}

// Path returns the doltlite data directory (.beads/doltlite/).
func (s *DoltliteStore) Path() string {
	return s.dataDir
}

// CLIDir returns the directory for dolt CLI operations (push/pull/remote).
// This is the actual database directory within the data dir.
func (s *DoltliteStore) CLIDir() string {
	if s.dataDir == "" {
		return ""
	}
	_, dbFile, err := buildDSN(s.dataDir, s.database)
	if err != nil {
		return ""
	}
	return dbFile
}

// ---------------------------------------------------------------------------
// storage.VersionControl
// ---------------------------------------------------------------------------

// Branch, Checkout, CurrentBranch, DeleteBranch, ListBranches are
// implemented in version_control.go.

func (s *DoltliteStore) CommitPending(ctx context.Context, actor string) (bool, error) {
	var hasPending bool
	var msg string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		hasPending, err = hasPendingChangesDoltlite(ctx, tx)
		if err != nil {
			return err
		}
		if hasPending {
			msg = buildDoltliteBatchCommitMessage(ctx, tx, actor)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if !hasPending {
		return false, nil
	}

	if err := s.CommitWithConfig(ctx, msg); err != nil {
		if issueops.IsNothingToCommitError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CommitExists is implemented in version_control.go.

func (s *DoltliteStore) GetCurrentCommit(ctx context.Context) (string, error) {
	var hash string
	err := s.withDBConn(ctx, func(db versioncontrolops.DBConn) error {
		return db.QueryRowContext(ctx, "SELECT dolt_hashof('HEAD')").Scan(&hash)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}

// Status, Log, Merge, GetConflicts, ResolveConflicts are implemented in
// version_control.go.

// ---------------------------------------------------------------------------
// storage.HistoryViewer
// ---------------------------------------------------------------------------

func (s *DoltliteStore) History(ctx context.Context, issueID string) ([]*storage.HistoryEntry, error) {
	var result []*storage.HistoryEntry
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = doltliteHistoryInTx(ctx, tx, issueID)
		return err
	})
	return result, err
}

func (s *DoltliteStore) AsOf(ctx context.Context, issueID string, ref string) (*types.Issue, error) {
	var result *types.Issue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = doltliteAsOfInTx(ctx, tx, issueID, ref)
		return err
	})
	return result, err
}

func (s *DoltliteStore) Diff(ctx context.Context, fromRef, toRef string) ([]*storage.DiffEntry, error) {
	var result []*storage.DiffEntry
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = doltliteDiffInTx(ctx, tx, fromRef, toRef)
		return err
	})
	return result, err
}

// ---------------------------------------------------------------------------
// storage.RemoteStore
// ---------------------------------------------------------------------------

// RemoveRemote, ListRemotes, Push, Pull, ForcePush, Fetch, PushTo, PullFrom
// are implemented in version_control.go.

// ---------------------------------------------------------------------------
// storage.SyncStore
// ---------------------------------------------------------------------------

// Sync and SyncStatus are implemented in federation.go.

// ---------------------------------------------------------------------------
// storage.FederationStore
// ---------------------------------------------------------------------------

// AddFederationPeer, GetFederationPeer, ListFederationPeers, RemoveFederationPeer
// are implemented in federation.go via issueops.

// ---------------------------------------------------------------------------
// storage.BulkIssueStore
// ---------------------------------------------------------------------------

// CreateIssuesWithFullOptions is implemented in create_issue.go.

func (s *DoltliteStore) DeleteIssues(ctx context.Context, ids []string, cascade bool, force bool, dryRun bool) (*types.DeleteIssuesResult, error) {
	var result *types.DeleteIssuesResult
	err := s.withConn(ctx, !dryRun, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.DeleteIssuesInTx(ctx, tx, ids, cascade, force, dryRun)
		return err
	})
	return result, err
}

func (s *DoltliteStore) DeleteIssuesBySourceRepo(ctx context.Context, sourceRepo string) (int, error) {
	var count int
	err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		var err error
		count, err = issueops.DeleteIssuesBySourceRepoInTx(ctx, tx, sourceRepo)
		return err
	})
	return count, err
}

func (s *DoltliteStore) UpdateIssueID(ctx context.Context, oldID, newID string, issue *types.Issue, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.UpdateIssueIDInTx(ctx, tx, oldID, newID, issue, actor)
	})
}

// ClaimIssue is implemented in issues.go.

func (s *DoltliteStore) PromoteFromEphemeral(ctx context.Context, id string, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.PromoteFromEphemeralInTx(ctx, tx, id, actor)
	})
}

// GetNextChildID is implemented in child_id.go.

func (s *DoltliteStore) RenameCounterPrefix(ctx context.Context, oldPrefix, newPrefix string) error {
	return nil // Hash-based IDs don't use counters.
}

// ---------------------------------------------------------------------------
// storage.DependencyQueryStore
// ---------------------------------------------------------------------------

func (s *DoltliteStore) GetDependencyRecords(ctx context.Context, issueID string) ([]*types.Dependency, error) {
	var result []*types.Dependency
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		m, err := issueops.GetDependencyRecordsForIssuesInTx(ctx, tx, []string{issueID})
		if err != nil {
			return err
		}
		result = m[issueID]
		return nil
	})
	return result, err
}

// IsBlocked is implemented in issues.go.

// GetNewlyUnblockedByClose is implemented in issues.go.

// DetectCycles is implemented in dependencies.go.

func (s *DoltliteStore) FindWispDependentsRecursive(ctx context.Context, ids []string) (map[string]bool, error) {
	var result map[string]bool
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.FindWispDependentsRecursiveInTx(ctx, tx, ids)
		return err
	})
	return result, err
}

func (s *DoltliteStore) RenameDependencyPrefix(ctx context.Context, oldPrefix, newPrefix string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		for _, table := range []string{"dependencies", "wisp_dependencies"} {
			for _, column := range []string{"issue_id", "depends_on_issue_id", "depends_on_wisp_id"} {
				if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
					UPDATE %s
					SET %s = ? || substr(%s, ?)
					WHERE %s = ? OR %s LIKE ?
				`, table, column, column, column, column),
					newPrefix, len(oldPrefix)+1, oldPrefix, oldPrefix+".%",
				); err != nil {
					return fmt.Errorf("rename dependency prefix in %s.%s: %w", table, column, err)
				}
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// storage.AnnotationQueryStore
// ---------------------------------------------------------------------------

func (s *DoltliteStore) AddComment(ctx context.Context, issueID, actor, comment string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.AddCommentEventInTx(ctx, tx, issueID, actor, comment)
	})
}

func (s *DoltliteStore) ImportIssueComment(ctx context.Context, issueID, author, text string, createdAt time.Time) (*types.Comment, error) {
	var result *types.Comment
	err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.ImportIssueCommentInTx(ctx, tx, issueID, author, text, createdAt)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetCommentsForIssues(ctx context.Context, issueIDs []string) (map[string][]*types.Comment, error) {
	var result map[string][]*types.Comment
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetCommentsForIssuesInTx(ctx, tx, issueIDs)
		return err
	})
	return result, err
}

// ---------------------------------------------------------------------------
// storage.ConfigMetadataStore
// ---------------------------------------------------------------------------

func (s *DoltliteStore) DeleteConfig(ctx context.Context, key string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.DeleteConfigInTx(ctx, tx, key)
	})
}

func (s *DoltliteStore) GetCustomStatuses(ctx context.Context) ([]string, error) {
	detailed, err := s.GetCustomStatusesDetailed(ctx)
	if err != nil {
		return nil, err
	}
	return types.CustomStatusNames(detailed), nil
}

func (s *DoltliteStore) GetCustomStatusesDetailed(ctx context.Context) ([]types.CustomStatus, error) {
	var result []types.CustomStatus
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var txErr error
		result, txErr = issueops.ResolveCustomStatusesDetailedInTx(ctx, tx)
		return txErr
	})
	if err != nil {
		// DB unavailable — fall back to config.yaml.
		if yamlStatuses := config.GetCustomStatusesFromYAML(); len(yamlStatuses) > 0 {
			return issueops.ParseStatusFallback(yamlStatuses), nil
		}
		return nil, nil
	}
	return result, nil
}

func (s *DoltliteStore) GetCustomTypes(ctx context.Context) ([]string, error) {
	var result []string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var txErr error
		result, txErr = issueops.ResolveCustomTypesInTx(ctx, tx)
		return txErr
	})
	if err != nil {
		// DB unavailable — fall back to config.yaml.
		if yamlTypes := config.GetCustomTypesFromYAML(); len(yamlTypes) > 0 {
			return yamlTypes, nil
		}
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// storage.CompactionStore
// ---------------------------------------------------------------------------

func (s *DoltliteStore) CheckEligibility(ctx context.Context, issueID string, tier int) (bool, string, error) {
	var eligible bool
	var reason string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		eligible, reason, err = issueops.CheckEligibilityInTx(ctx, tx, issueID, tier)
		return err
	})
	return eligible, reason, err
}

func (s *DoltliteStore) ApplyCompaction(ctx context.Context, issueID string, tier int, originalSize int, _ int, commitHash string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.ApplyCompactionInTx(ctx, tx, issueID, tier, originalSize, commitHash)
	})
}

func (s *DoltliteStore) SnapshotIssue(ctx context.Context, issueID string, tier int) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.SnapshotIssueInTx(ctx, tx, issueID, tier)
	})
}

func (s *DoltliteStore) GetCompactionSnapshot(ctx context.Context, issueID string) (*types.IssueSnapshot, error) {
	var snap *types.IssueSnapshot
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		snap, err = issueops.GetLatestSnapshotInTx(ctx, tx, issueID)
		return err
	})
	return snap, err
}

func (s *DoltliteStore) RestoreFromSnapshot(ctx context.Context, issueID string) (*types.IssueSnapshot, error) {
	var snap *types.IssueSnapshot
	err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		var err error
		snap, err = issueops.RestoreFromSnapshotInTx(ctx, tx, issueID)
		return err
	})
	return snap, err
}

func (s *DoltliteStore) GetTier1Candidates(ctx context.Context) ([]*types.CompactionCandidate, error) {
	var result []*types.CompactionCandidate
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetTier1CandidatesInTx(ctx, tx)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetTier2Candidates(ctx context.Context) ([]*types.CompactionCandidate, error) {
	var result []*types.CompactionCandidate
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetTier2CandidatesInTx(ctx, tx)
		return err
	})
	return result, err
}

// ---------------------------------------------------------------------------
// storage.AdvancedQueryStore
// ---------------------------------------------------------------------------

func (s *DoltliteStore) GetRepoMtime(ctx context.Context, repoPath string) (int64, error) {
	var result int64
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetRepoMtimeInTx(ctx, tx, repoPath)
		return err
	})
	return result, err
}

func (s *DoltliteStore) SetRepoMtime(ctx context.Context, repoPath, jsonlPath string, mtimeNs int64) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.SetRepoMtimeInTx(ctx, tx, repoPath, jsonlPath, mtimeNs)
	})
}

func (s *DoltliteStore) ClearRepoMtime(ctx context.Context, repoPath string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.ClearRepoMtimeInTx(ctx, tx, repoPath)
	})
}

// GetMoleculeProgress is implemented in queries.go.

func (s *DoltliteStore) GetMoleculeLastActivity(ctx context.Context, moleculeID string) (*types.MoleculeLastActivity, error) {
	var result *types.MoleculeLastActivity
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetMoleculeLastActivityInTx(ctx, tx, moleculeID)
		return err
	})
	return result, err
}

func (s *DoltliteStore) GetStaleIssues(ctx context.Context, filter types.StaleFilter) ([]*types.Issue, error) {
	var result []*types.Issue
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = issueops.GetStaleIssuesInTx(ctx, tx, filter)
		return err
	})
	return result, err
}
