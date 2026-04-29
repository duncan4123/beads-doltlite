//go:build cgo

package beads

import (
	"context"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/storage/doltlite"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
)

// OpenBestAvailable opens a beads database using the best available backend
// for the given .beads directory. It reads metadata.json to determine the
// configured mode:
//
//   - Embedded mode (default): Opens via the CGo embedded Dolt engine with an
//     exclusive flock to prevent corruption from concurrent access. This matches
//     the behavior of the bd CLI.
//   - Server mode: Connects to an external dolt sql-server via OpenFromConfig.
//
// The returned Storage must be closed when no longer needed. In embedded mode
// the caller must also defer the Unlocker returned by the flock; pass nil-safe
// patterns such as:
//
//	store, lock, err := beads.OpenBestAvailable(ctx, beadsDir)
//	if err != nil { ... }
//	defer lock.Unlock()
//	defer store.Close()
//
// beadsDir is the path to the .beads directory.
func OpenBestAvailable(ctx context.Context, beadsDir string) (Storage, embeddeddolt.Unlocker, error) {
	cfg, err := configfile.Load(beadsDir)
	if err == nil && cfg != nil && cfg.IsDoltServerMode() {
		store, err := dolt.NewFromConfig(ctx, beadsDir)
		if err != nil {
			return nil, nil, err
		}
		return store, embeddeddolt.NoopLock{}, nil
	}

	database := configfile.DefaultDoltDatabase
	if cfg != nil {
		database = cfg.GetDoltDatabase()
	}
	store, err := doltlite.New(ctx, beadsDir, database, "main")
	if err != nil {
		return nil, nil, err
	}
	return store, embeddeddolt.NoopLock{}, nil
}
