//go:build cgo

package doltlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

func (s *DoltliteStore) GetStatistics(ctx context.Context) (*types.Statistics, error) {
	stats := &types.Statistics{}
	err := s.withConn(ctx, false, func(tx *sql.Tx) error {
		if err := issueops.ScanIssueCountsInTx(ctx, tx, stats); err != nil {
			return err
		}

		var blockedCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM issues
			WHERE is_blocked = 1 AND status <> 'closed' AND status <> 'pinned'
		`).Scan(&blockedCount); err != nil {
			return err
		}
		stats.BlockedIssues = blockedCount
		stats.ReadyIssues = stats.OpenIssues - stats.BlockedIssues
		if stats.ReadyIssues < 0 {
			stats.ReadyIssues = 0
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("doltlite: get statistics: %w", err)
	}
	return stats, nil
}
