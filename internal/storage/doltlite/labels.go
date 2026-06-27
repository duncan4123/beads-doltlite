//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func (s *DoltliteStore) GetLabels(ctx context.Context, issueID string) ([]string, error) {
	var labels []string
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		var err error
		labels, err = issueops.GetLabelsInTx(ctx, tx, "", issueID)
		return err
	})
	return labels, err
}

func (s *DoltliteStore) AddLabel(ctx context.Context, issueID, label, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		isWisp := issueops.IsActiveWispInTx(ctx, tx, issueID)
		_, labelTable, eventTable, _ := issueops.WispTableRouting(isWisp)
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT OR IGNORE INTO %s (issue_id, label) VALUES (?, ?)", labelTable), issueID, label); err != nil {
			return fmt.Errorf("add label: %w", err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (id, issue_id, event_type, actor, comment) VALUES (?, ?, ?, ?, ?)", eventTable),
			issueops.NewEventID(), issueID, types.EventLabelAdded, actor, "Added label: "+label); err != nil {
			return fmt.Errorf("add label: record event: %w", err)
		}
		return nil
	})
}

// RemoveLabel removes a label from an issue.
func (s *DoltliteStore) RemoveLabel(ctx context.Context, issueID, label, actor string) error {
	return s.withConn(ctx, true, func(tx *sql.Tx) error {
		return issueops.RemoveLabelInTx(ctx, tx, "", "", issueID, label, actor)
	})
}
