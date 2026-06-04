//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
)

func (s *DoltliteStore) GetNextChildID(ctx context.Context, parentID string) (string, error) {
	var childID string
	err := s.withConn(ctx, true, func(tx *sql.Tx) error {
		counterTable, issueTable := "child_counters", "issues"
		if issueops.IsActiveWispInTx(ctx, tx, parentID) {
			counterTable, issueTable = "wisp_child_counters", "wisps"
		}

		var lastChild int
		err := tx.QueryRowContext(ctx, fmt.Sprintf("SELECT last_child FROM %s WHERE parent_id = ?", counterTable), parentID).Scan(&lastChild)
		if err == sql.ErrNoRows {
			lastChild = 0
		} else if err != nil {
			return fmt.Errorf("get next child ID: read counter: %w", err)
		}

		rows, err := tx.QueryContext(ctx, fmt.Sprintf(`
			SELECT id FROM %s
			WHERE id LIKE ?
			  AND id NOT LIKE ?
		`, issueTable), parentID+".%", parentID+".%.%")
		if err != nil {
			return fmt.Errorf("get next child ID: query existing children: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return fmt.Errorf("get next child ID: scan child row: %w", err)
			}
			_, childNum, ok := issueops.ParseHierarchicalID(id)
			if ok && childNum > lastChild {
				lastChild = childNum
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("get next child ID: iterate children: %w", err)
		}

		nextChild := lastChild + 1
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`
			INSERT INTO %s (parent_id, last_child) VALUES (?, ?)
			ON CONFLICT(parent_id) DO UPDATE SET last_child = excluded.last_child
		`, counterTable), parentID, nextChild); err != nil {
			return fmt.Errorf("get next child ID: update counter: %w", err)
		}
		childID = fmt.Sprintf("%s.%d", parentID, nextChild)
		return nil
	})
	return childID, err
}
