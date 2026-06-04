//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/types"
)

// backfillCustomTables populates empty custom_types and custom_statuses
// tables from the corresponding config values. This mirrors the Dolt
// backend's BackfillCustomTables migration (016) and repairs databases
// where the schema migration (0024) created the tables but did not
// backfill them from config.
//
// Must be called after the persistent DB is open (s.db != nil).
func (s *DoltliteStore) backfillCustomTables(ctx context.Context) error {
	db, _, err := s.activeDB(ctx)
	if err != nil {
		return fmt.Errorf("backfill: open db: %w", err)
	}
	// Don't close the returned db — activeDB returns the persistent handle
	// or a clone; we use it for one-shot queries and let the connection
	// pool reuse it.

	if err := backfillCustomTypesSQLite(ctx, db); err != nil {
		return fmt.Errorf("custom_types: %w", err)
	}
	if err := backfillCustomStatusesSQLite(ctx, db); err != nil {
		return fmt.Errorf("custom_statuses: %w", err)
	}
	return nil
}

func backfillCustomTypesSQLite(ctx context.Context, db *sql.DB) error {
	// Check table exists
	var hasTable int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='custom_types'",
	).Scan(&hasTable); err != nil || hasTable == 0 {
		return err
	}

	// Skip if already populated
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM custom_types").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	// Read types.custom from config
	var value string
	err := db.QueryRowContext(ctx,
		"SELECT value FROM config WHERE `key` = ?", "types.custom",
	).Scan(&value)
	if err != nil || value == "" {
		return nil // No config to backfill from
	}

	for _, name := range parseTypesValue(value) {
		_, err = db.ExecContext(ctx,
			"INSERT OR IGNORE INTO custom_types (name) VALUES (?)", name,
		)
		if err != nil {
			return fmt.Errorf("inserting type %q: %w", name, err)
		}
	}
	return nil
}

func backfillCustomStatusesSQLite(ctx context.Context, db *sql.DB) error {
	var hasTable int
	if err := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='custom_statuses'",
	).Scan(&hasTable); err != nil || hasTable == 0 {
		return err
	}

	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM custom_statuses").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	var value string
	err := db.QueryRowContext(ctx,
		"SELECT value FROM config WHERE `key` = ?", "status.custom",
	).Scan(&value)
	if err != nil || value == "" {
		return nil
	}

	parsed, parseErr := types.ParseCustomStatusConfig(value)
	if parseErr != nil {
		// Invalid config value: log and skip (same behavior as Dolt migration)
		return nil
	}
	for _, s := range parsed {
		_, err = db.ExecContext(ctx,
			"INSERT OR IGNORE INTO custom_statuses (name, category) VALUES (?, ?)",
			s.Name, string(s.Category),
		)
		if err != nil {
			return fmt.Errorf("inserting status %q: %w", s.Name, err)
		}
	}
	return nil
}

// parseTypesValue tries JSON array first, then falls back to comma-separated.
// Mirrors dolt/migrations/015_custom_status_type_tables.go:parseTypesValue.
func parseTypesValue(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var jsonTypes []string
	if err := json.Unmarshal([]byte(value), &jsonTypes); err == nil {
		return jsonTypes
	}
	return splitCommaSeparated(value)
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
