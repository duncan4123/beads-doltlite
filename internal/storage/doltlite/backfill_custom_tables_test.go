//go:build cgo

package doltlite_test

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/steveyegge/beads/internal/storage/doltlite"
)

// TestBackfillCustomTablesOnNew verifies that creating a new doltlite store
// leaves normalized custom config tables stable across reopen. Fresh upstream
// config does not seed types.custom, so the tables may legitimately be empty.
func TestBackfillCustomTablesOnNew(t *testing.T) {
	ctx := context.Background()

	dir := filepath.Join(t.TempDir(), ".beads")

	// First open: backfill should succeed even when config has no custom types.
	store1, err := doltlite.New(ctx, dir, "beads", "main")
	if err != nil {
		t.Fatalf("New (first): %v", err)
	}

	types1, err := store1.GetCustomTypes(ctx)
	if err != nil {
		store1.Close()
		t.Fatalf("GetCustomTypes (first): %v", err)
	}
	if !slices.Equal(types1, []string{}) {
		store1.Close()
		t.Fatalf("fresh custom types = %v, want empty", types1)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("Close (first): %v", err)
	}

	// Re-open: backfill should be a no-op (table already populated).
	store2, err := doltlite.New(ctx, dir, "beads", "main")
	if err != nil {
		t.Fatalf("New (second): %v", err)
	}
	defer store2.Close()

	types2, err := store2.GetCustomTypes(ctx)
	if err != nil {
		t.Fatalf("GetCustomTypes (second): %v", err)
	}
	if !slices.Equal(types1, types2) {
		t.Errorf("custom types changed across re-open:\n  first:  %v\n  second: %v", types1, types2)
	}
}
