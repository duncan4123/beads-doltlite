//go:build cgo

package doltlite_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/doltlite"
)

func TestMain(m *testing.M) {
	if err := requireNativeDoltliteForTests(); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP internal/storage/doltlite: %v\n", err)
		fmt.Fprintln(os.Stderr, "Run: make test-doltlite")
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func requireNativeDoltliteForTests() error {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "bd-doltlite-native-probe.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	dataDir := filepath.Join(dir, "doltlite")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}

	db, cleanup, err := doltlite.OpenSQL(ctx, dataDir, "beads", "")
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()

	var version string
	if err := db.QueryRowContext(ctx, "SELECT dolt_version()").Scan(&version); err != nil {
		if strings.Contains(err.Error(), "no such function") {
			return fmt.Errorf("libdoltlite SQL functions are not linked into the sqlite driver: %w", err)
		}
		return err
	}
	return nil
}
