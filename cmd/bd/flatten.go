package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/storage"
)

var (
	flattenDryRun bool
	flattenForce  bool
)

var flattenCmd = &cobra.Command{
	Use:     "flatten",
	GroupID: "maint",
	Short:   "Squash all Dolt history into a single commit",
	Long: `Nuclear option: squash ALL Dolt commit history into a single commit.

This uses the Tim Sehn recipe:
  1. Create a new branch from the current state
  2. Soft-reset to the initial commit (preserving all data)
  3. Commit everything as a single snapshot
  4. Swap main branch to the new flattened branch
  5. Run Dolt GC to reclaim space from old history

This is irreversible — all commit history is lost. The resulting database
has exactly one commit containing all current data.

Use this when:
  - Your .beads/dolt directory has grown very large
  - You don't need commit-level history (time travel)
  - You want to start fresh with minimal storage

Examples:
  bd flatten --dry-run               # Preview: show commit count and disk usage
  bd flatten --force                 # Actually squash all history
  bd flatten --force --json          # JSON output`,
	Run: func(_ *cobra.Command, _ []string) {
		if !flattenDryRun {
			CheckReadonly("flatten")
		}
		ctx := rootCtx
		start := time.Now()

		flattener, ok := storage.UnwrapStore(store).(storage.Flattener)
		if !ok {
			FatalError("storage backend does not support flatten")
		}

		// Get commit count and initial hash for reporting.
		// Use store.Log() which works across both backends.
		logEntries, logErr := store.Log(ctx, 0)
		if logErr != nil {
			FatalError("failed to read commit log: %v", logErr)
		}
		commitCount := len(logEntries)

		var initialHash string
		if commitCount > 0 {
			initialHash = logEntries[commitCount-1].Hash // oldest is last
		}


		// Get WAL size (Dolt database directory size) for reporting.
		var walSizeBefore int64
		if locator, ok := storage.UnwrapStore(store).(storage.StoreLocator); ok {
			if path := locator.Path(); path != "" {
				if s, err := getDirSize(path); err == nil {
					walSizeBefore = s
				}
			}
		}
		if flattenDryRun {
			if jsonOutput {
				result := map[string]interface{}{
					"dry_run":          true,
					"commit_count":     commitCount,
					"initial_hash":     initialHash,
					"would_flatten":    commitCount > 1,
					"wal_size":         walSizeBefore,
					"wal_size_display": formatBytes(walSizeBefore),
				}
				outputJSON(result)
				return
			}
			fmt.Printf("DRY RUN — Flatten preview\n\n")
			fmt.Printf("  Commits:        %d\n", commitCount)
			fmt.Printf("  WAL size:       %s\n", formatBytes(walSizeBefore))
			fmt.Printf("  Initial commit: %s\n", initialHash)
			if commitCount <= 1 {
				fmt.Printf("\n  Already flat (1 commit). Nothing to do.\n")
			} else {
				fmt.Printf("\n  Would squash %d commits into 1.\n", commitCount)
				fmt.Printf("  Run with --force to proceed.\n")
			}
			return
		}

	if commitCount <= 1 {
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"success":          true,
				"message":          "already flat",
				"commit_count":     commitCount,
				"wal_size":         walSizeBefore,
				"wal_size_display": formatBytes(walSizeBefore),
			})
			return
		}
		fmt.Printf("Already flat (1 commit). Nothing to do.\n")
		fmt.Printf("WAL size: %s\n", formatBytes(walSizeBefore))
		return
	}

		if !flattenForce {
			FatalErrorWithHint(
				fmt.Sprintf("would squash %d commits into 1 (irreversible)", commitCount),
				"Use --force to confirm or --dry-run to preview.")
		}

	if !jsonOutput {
		fmt.Printf("Flattening %d commits...\n", commitCount)
		fmt.Printf("  WAL size before: %s\n", formatBytes(walSizeBefore))
	}

	if err := flattener.Flatten(ctx); err != nil {
		FatalError("flatten failed: %v", err)
	}

	// Reclaim disk space from the now-orphaned old history.
	if gc, ok := storage.UnwrapStore(store).(storage.GarbageCollector); ok {
		if err := gc.DoltGC(ctx); err != nil {
			WarnError("dolt gc after flatten failed: %v", err)
		}
	}

	// Measure WAL size after flatten+GC.
	var walSizeAfter int64
	if locator, ok := storage.UnwrapStore(store).(storage.StoreLocator); ok {
		if path := locator.Path(); path != "" {
			if s, err := getDirSize(path); err == nil {
				walSizeAfter = s
			}
		}
	}

	elapsed := time.Since(start)
	freed := walSizeBefore - walSizeAfter
	if freed < 0 {
		freed = 0
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":             true,
			"commits_before":      commitCount,
			"commits_after":       1,
			"wal_size_before":     walSizeBefore,
			"wal_size_after":      walSizeAfter,
			"wal_size_freed":      freed,
			"wal_size_display":    formatBytes(walSizeAfter),
			"wal_freed_display":   formatBytes(freed),
			"elapsed_ms":          elapsed.Milliseconds(),
		})
		return
	}
	fmt.Printf("✓ Flattened %d commits → 1\n", commitCount)
	fmt.Printf("  WAL size:  %s → %s (freed %s)\n", formatBytes(walSizeBefore), formatBytes(walSizeAfter), formatBytes(freed))
	fmt.Printf("  Time: %v\n", elapsed.Round(time.Millisecond))
	},
}

func init() {
	flattenCmd.Flags().BoolVar(&flattenDryRun, "dry-run", false, "Preview without making changes")
	flattenCmd.Flags().BoolVarP(&flattenForce, "force", "f", false, "Confirm irreversible history squash")

	rootCmd.AddCommand(flattenCmd)
}
