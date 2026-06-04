//go:build cgo

package doltlite

import (
	"context"
	"fmt"
	"strings"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

func buildDoltliteBatchCommitMessage(ctx context.Context, db issueops.SQLQuerier, actor string) string {
	if actor == "" {
		actor = "bd"
	}

	var added, modified, removed int
	rows, err := db.QueryContext(ctx, `
		SELECT diff_type, COUNT(*) as cnt
		FROM dolt_diff_issues('HEAD', 'WORKING')
		GROUP BY diff_type
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var diffType string
			var count int
			if scanErr := rows.Scan(&diffType, &count); scanErr == nil {
				switch diffType {
				case "added":
					added = count
				case "modified":
					modified = count
				case "removed":
					removed = count
				}
			}
		}
		_ = rows.Err()
	}

	var otherTables []string
	statusRows, statusErr := db.QueryContext(ctx, "SELECT table_name FROM dolt_status WHERE table_name != 'issues' ORDER BY table_name")
	if statusErr == nil {
		defer statusRows.Close()
		for statusRows.Next() {
			var table string
			if scanErr := statusRows.Scan(&table); scanErr == nil {
				if isDoltliteRuntimeTable(table) {
					continue
				}
				otherTables = append(otherTables, table)
			}
		}
		_ = statusRows.Err()
	}

	msg := fmt.Sprintf("bd: batch commit by %s", actor)
	var parts []string
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d created", added))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", modified))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("%d deleted", removed))
	}
	if len(parts) > 0 {
		msg += " - " + strings.Join(parts, ", ")
	}
	if len(otherTables) > 0 {
		msg += fmt.Sprintf(" (+ %s)", strings.Join(otherTables, ", "))
	}
	return msg
}
