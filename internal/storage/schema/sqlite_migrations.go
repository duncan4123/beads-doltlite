package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// MigrateFreshSQLite applies the main schema migration stream to a fresh
// SQLite-compatible database. It records the same migration versions and
// content hashes as the Dolt/MySQL path, while executing SQLite-compatible SQL
// for fresh DoltLite stores.
func MigrateFreshSQLite(ctx context.Context, db DBConn, maxVersion int) (int, error) {
	if _, err := db.ExecContext(ctx, mainSource.bootstrapSQL()); err != nil {
		return 0, fmt.Errorf("creating %s: %w", mainSource.cursorTable, err)
	}
	if has, err := mainSource.hasContentHashColumn(ctx, db); err != nil {
		return 0, err
	} else if !has {
		if _, err := db.ExecContext(ctx, "ALTER TABLE "+mainSource.cursorTable+" ADD COLUMN content_hash CHAR(64)"); err != nil {
			return 0, fmt.Errorf("adding %s.content_hash: %w", mainSource.cursorTable, err)
		}
	}

	current, err := mainSource.currentVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	if current != 0 {
		return 0, fmt.Errorf("sqlite fresh migration requires an empty schema cursor, found version %d", current)
	}

	target := mainSource.latest()
	if maxVersion > 0 && maxVersion < target {
		target = maxVersion
	}

	count := 0
	for _, mf := range mainSource.list() {
		if mf.version > target {
			continue
		}
		data, err := mainSource.files.ReadFile(mainSource.dir + "/" + mf.name)
		if err != nil {
			return count, fmt.Errorf("reading migration %s: %w", mf.name, err)
		}
		if mf.name == "0046_add_is_blocked.up.sql" {
			if err := applySQLiteMigration0046(ctx, db); err != nil {
				return count, fmt.Errorf("migration %s: %w", mf.name, err)
			}
		} else {
			sqlText := sqliteCompatibleMigrationSQL(mf.name, string(data))
			if strings.TrimSpace(sqlText) != "" {
				if _, err := db.ExecContext(ctx, sqlText); err != nil {
					return count, fmt.Errorf("migration %s: %w", mf.name, err)
				}
			}
		}
		sum := sha256.Sum256(data)
		if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO "+mainSource.cursorTable+" (version, content_hash) VALUES (?, ?)", mf.version, hex.EncodeToString(sum[:])); err != nil {
			return count, fmt.Errorf("recording %s in %s: %w", mf.name, mainSource.cursorTable, err)
		}
		count++
	}
	return count, nil
}

// MigrateSQLiteUpTo applies pending main schema migrations to an existing
// SQLite-compatible database. It records the original migration hashes while
// executing the SQLite-compatible migration body, which may intentionally be a
// no-op for Dolt/MySQL-only migrations.
func MigrateSQLiteUpTo(ctx context.Context, db DBConn, maxVersion int) (int, error) {
	if _, err := db.ExecContext(ctx, mainSource.bootstrapSQL()); err != nil {
		return 0, fmt.Errorf("creating %s: %w", mainSource.cursorTable, err)
	}
	if _, err := mainSource.ensureContentHashColumn(ctx, db); err != nil {
		return 0, err
	}

	target := mainSource.latest()
	if maxVersion > 0 && maxVersion < target {
		target = maxVersion
	}

	current, err := mainSource.currentVersion(ctx, db)
	if err != nil {
		return 0, err
	}
	if current >= target {
		return 0, nil
	}

	count := 0
	for _, mf := range mainSource.list() {
		if mf.version <= current || mf.version > target {
			continue
		}
		data, err := mainSource.files.ReadFile(mainSource.dir + "/" + mf.name)
		if err != nil {
			return count, fmt.Errorf("reading migration %s: %w", mf.name, err)
		}
		if mf.name == "0046_add_is_blocked.up.sql" {
			if err := applySQLiteMigration0046(ctx, db); err != nil {
				return count, fmt.Errorf("migration %s: %w", mf.name, err)
			}
		} else {
			sqlText := sqliteCompatibleMigrationSQL(mf.name, string(data))
			if strings.TrimSpace(sqlText) != "" {
				if _, err := db.ExecContext(ctx, sqlText); err != nil {
					return count, fmt.Errorf("migration %s: %w", mf.name, err)
				}
			}
		}
		sum := sha256.Sum256(data)
		if _, err := db.ExecContext(ctx, "INSERT OR IGNORE INTO "+mainSource.cursorTable+" (version, content_hash) VALUES (?, ?)", mf.version, hex.EncodeToString(sum[:])); err != nil {
			return count, fmt.Errorf("recording %s in %s: %w", mf.name, mainSource.cursorTable, err)
		}
		count++
	}
	return count, nil
}

func applySQLiteMigration0046(ctx context.Context, db DBConn) error {
	hasIsBlocked, err := sqliteColumnExists(ctx, db, "issues", "is_blocked")
	if err != nil {
		return fmt.Errorf("checking issues.is_blocked: %w", err)
	}
	if !hasIsBlocked {
		if _, err := db.ExecContext(ctx, "ALTER TABLE issues ADD COLUMN is_blocked TINYINT(1) NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err := db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_issues_is_blocked ON issues(is_blocked, status)"); err != nil {
		return fmt.Errorf("creating idx_issues_is_blocked: %w", err)
	}
	return nil
}

func sqliteColumnExists(ctx context.Context, db DBConn, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func sqliteCompatibleMigrationSQL(name, sqlText string) string {
	switch name {
	case "0019_wisps_dolt_ignore.up.sql", "0028_local_state_dolt_ignore.up.sql",
		"0040_ignored_tables_also_nonlocal_tables.up.sql":
		return ""
	case "0022_wisp_dep_type_index.up.sql":
		return `CREATE INDEX IF NOT EXISTS idx_wisp_dep_type ON wisp_dependencies (type);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_issue ON wisp_dependencies (type, depends_on_issue_id);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_wisp ON wisp_dependencies (type, depends_on_wisp_id);
CREATE INDEX IF NOT EXISTS idx_wisp_dep_type_external ON wisp_dependencies (type, depends_on_external);`
	case "0023_add_no_history_column.up.sql":
		return cliMigration0023AddNoHistoryColumn
	case "0027_add_started_at.up.sql":
		return cliMigration0027AddStartedAt
	case "0020_create_wisps.up.sql":
		return sqliteNormalizeMigrationSQL(sqlText) + `
ALTER TABLE wisps ADD COLUMN is_blocked TINYINT(1) NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_wisps_is_blocked ON wisps(is_blocked, status);`
	case "0030_migrate_local_metadata_keys.up.sql":
		return ""
	case "0031_wisp_events_created_at_index.up.sql":
		return `CREATE INDEX IF NOT EXISTS idx_wisp_events_created_at ON wisp_events (created_at);`
	case "0032_drop_schema_migrations_applied_at.up.sql":
		return ""
	case "0035_migrate_infra_to_wisps.up.sql", "0036_cleanup_autopush_metadata.up.sql",
		"0037_uuid_primary_keys.up.sql", "0038_drop_hop_columns.up.sql",
		"0039_drop_child_counters_fk.up.sql", "0042_add_on_update_cascade.up.sql", "0047_recompute_mixed_is_blocked.up.sql",
		"0048_widen_event_value_columns.up.sql", "0049_longtext_large_content_columns.up.sql",
		"0050_dependencies_deterministic_id.up.sql", "0051_drop_aux_id_defaults.up.sql",
		"0053_repair_rig_wisps.up.sql":
		return ""
	case "0041_split_dependencies_target.up.sql", "0043_drop_dependencies_generated_column.up.sql":
		return sqliteFinalDependenciesSchema
	case "0046_add_is_blocked.up.sql":
		return cliMigration0046AddIsBlocked
	case "0052_add_date_indexes.up.sql":
		return `DROP INDEX IF EXISTS idx_issues_status;
CREATE INDEX IF NOT EXISTS idx_issues_status_updated_at ON issues (status, updated_at);
CREATE INDEX IF NOT EXISTS idx_issues_defer_until ON issues (defer_until);`
	case "0054_add_lease_columns.up.sql":
		return cliMigration0054AddLeaseColumns
	default:
		return sqliteNormalizeMigrationSQL(sqlText)
	}
}

func sqliteNormalizeMigrationSQL(sqlText string) string {
	sqlText = strings.ReplaceAll(sqlText, " ON UPDATE CURRENT_TIMESTAMP", "")
	sqlText = strings.ReplaceAll(sqlText, "JSON DEFAULT (JSON_OBJECT())", "JSON DEFAULT '{}'")
	sqlText = strings.ReplaceAll(sqlText, "INSERT IGNORE", "INSERT OR IGNORE")
	sqlText = strings.ReplaceAll(sqlText, "UTC_TIMESTAMP()", "CURRENT_TIMESTAMP")
	sqlText = strings.ReplaceAll(sqlText, "NOW()", "CURRENT_TIMESTAMP")
	sqlText = sqliteNormalizeViews(sqlText)
	return sqliteExtractInlineIndexes(sqlText)
}

var createOrReplaceViewRE = regexp.MustCompile(`(?is)CREATE\s+OR\s+REPLACE\s+VIEW\s+([A-Za-z_][A-Za-z0-9_]*)\s+AS`)

func sqliteNormalizeViews(sqlText string) string {
	return createOrReplaceViewRE.ReplaceAllString(sqlText, "DROP VIEW IF EXISTS $1;\nCREATE VIEW $1 AS")
}

var (
	createTableRE    = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+([A-Za-z_][A-Za-z0-9_]*)\s*\((.*?)\)\s*;`)
	inlineIndexRE    = regexp.MustCompile(`(?i)^INDEX\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\(.*\))$`)
	inlineUniqueRE   = regexp.MustCompile(`(?i)^UNIQUE\s+KEY\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\(.*\))$`)
	inlineKeyRE      = regexp.MustCompile(`(?i)^KEY\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\(.*\))$`)
	firstColumnRE    = regexp.MustCompile(`(?i)\s+FIRST\b`)
	defaultUUIDColRE = regexp.MustCompile(`(?i)\s+DEFAULT\s+\(UUID\(\)\)`)
)

func sqliteExtractInlineIndexes(sqlText string) string {
	return createTableRE.ReplaceAllStringFunc(sqlText, func(stmt string) string {
		m := createTableRE.FindStringSubmatch(stmt)
		if len(m) != 3 {
			return stmt
		}
		table, body := m[1], m[2]
		parts := splitTopLevelComma(body)
		var columns []string
		var indexes []string
		for _, part := range parts {
			item := strings.TrimSpace(part)
			item = strings.TrimSuffix(item, ",")
			switch {
			case inlineIndexRE.MatchString(item):
				im := inlineIndexRE.FindStringSubmatch(item)
				indexes = append(indexes, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s %s;", im[1], table, im[2]))
			case inlineKeyRE.MatchString(item):
				im := inlineKeyRE.FindStringSubmatch(item)
				indexes = append(indexes, fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s %s;", im[1], table, im[2]))
			case inlineUniqueRE.MatchString(item):
				im := inlineUniqueRE.FindStringSubmatch(item)
				indexes = append(indexes, fmt.Sprintf("CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s %s;", im[1], table, im[2]))
			default:
				item = firstColumnRE.ReplaceAllString(item, "")
				item = defaultUUIDColRE.ReplaceAllString(item, "")
				columns = append(columns, item)
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "CREATE TABLE IF NOT EXISTS %s (\n    %s\n);", table, strings.Join(columns, ",\n    "))
		if len(indexes) > 0 {
			b.WriteString("\n")
			b.WriteString(strings.Join(indexes, "\n"))
		}
		return b.String()
	})
}

func splitTopLevelComma(s string) []string {
	var parts []string
	start, depth := 0, 0
	inSingle := false
	for i, r := range s {
		switch r {
		case '\'':
			inSingle = !inSingle
		case '(':
			if !inSingle {
				depth++
			}
		case ')':
			if !inSingle && depth > 0 {
				depth--
			}
		case ',':
			if !inSingle && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

const sqliteFinalDependenciesSchema = `DROP TABLE IF EXISTS dependencies;
CREATE TABLE dependencies (
    id CHAR(36) NOT NULL PRIMARY KEY,
    issue_id VARCHAR(255) NOT NULL,
    depends_on_issue_id VARCHAR(255) NULL,
    depends_on_wisp_id VARCHAR(255) NULL,
    depends_on_external VARCHAR(255) NULL,
    type VARCHAR(32) NOT NULL DEFAULT 'blocks',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_by VARCHAR(255) NOT NULL,
    metadata JSON DEFAULT '{}',
    thread_id VARCHAR(255) DEFAULT '',
    UNIQUE (issue_id, depends_on_issue_id),
    UNIQUE (issue_id, depends_on_wisp_id),
    UNIQUE (issue_id, depends_on_external),
    CONSTRAINT ck_dep_one_target CHECK ((depends_on_issue_id IS NOT NULL) + (depends_on_wisp_id IS NOT NULL) + (depends_on_external IS NOT NULL) = 1)
);
CREATE INDEX IF NOT EXISTS idx_dep_type_issue ON dependencies (type, depends_on_issue_id);
CREATE INDEX IF NOT EXISTS idx_dep_type_wisp ON dependencies (type, depends_on_wisp_id);
CREATE INDEX IF NOT EXISTS idx_dep_type_external ON dependencies (type, depends_on_external);
CREATE INDEX IF NOT EXISTS idx_dep_wisp_target ON dependencies (depends_on_wisp_id);
CREATE INDEX IF NOT EXISTS idx_dep_issue_target ON dependencies (depends_on_issue_id);
CREATE INDEX IF NOT EXISTS idx_dep_external_target ON dependencies (depends_on_external);`
