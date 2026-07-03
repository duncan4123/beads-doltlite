//go:build cgo

package doltlite_test

import (
	"path/filepath"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/conformance"
	"github.com/steveyegge/beads/internal/storage/doltlite"
)

// TestConformance runs the backend-agnostic storage conformance suite
// (internal/storage/conformance) against the DoltLite backend. TestMain in this
// package self-skips when the native libdoltlite-backed sqlite driver is not
// linked, so `make test-doltlite` is the intended entry point.
func TestConformance(t *testing.T) {
	conformance.RunAll(t, func(t *testing.T) storage.DoltStorage {
		ctx := t.Context()
		beadsDir := filepath.Join(t.TempDir(), ".beads")
		store, err := doltlite.New(ctx, beadsDir, "beads", "main")
		if err != nil {
			t.Fatalf("New DoltLite store: %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })

		// Match the post-`bd init` contract expected by the shared conformance
		// factory: an initialized store with issue_prefix configured.
		if err := store.SetConfig(ctx, "issue_prefix", "test"); err != nil {
			t.Fatalf("SetConfig(issue_prefix): %v", err)
		}
		if err := store.Commit(ctx, "bd init"); err != nil {
			t.Fatalf("Commit: %v", err)
		}
		return store
	})
}
