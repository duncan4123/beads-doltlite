//go:build cgo

package doltlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/doltlite"
	"github.com/steveyegge/beads/internal/types"
)

func doltliteCommitCount(t *testing.T, store *doltlite.DoltliteStore) int {
	t.Helper()
	commits, err := store.Log(t.Context(), 1000)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	return len(commits)
}

func requireDoltliteClean(t *testing.T, store *doltlite.DoltliteStore) {
	t.Helper()
	status, err := store.Status(t.Context())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Staged) != 0 || len(status.Unstaged) != 0 {
		t.Fatalf("status not clean: %+v", status)
	}
}

func TestSmokeCreateGetCommit(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	now := time.Now().UTC()
	issue := &types.Issue{
		ID:          "bd-test",
		Title:       "doltlite smoke",
		Description: "verify doltlite backend",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	got, err := store.GetIssue(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if got.Title != issue.Title {
		t.Fatalf("title = %q, want %q", got.Title, issue.Title)
	}

	if err := store.Commit(ctx, "test: doltlite smoke"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
}

func TestSmokeLabels(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	now := time.Now().UTC()
	issue := &types.Issue{
		ID:        "bd-label",
		Title:     "doltlite labels",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
		Labels:    []string{"gc:session"},
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if err := store.AddLabel(ctx, issue.ID, "agent:worker", "test"); err != nil {
		t.Fatalf("AddLabel: %v", err)
	}
	labels, err := store.GetLabels(ctx, issue.ID)
	if err != nil {
		t.Fatalf("GetLabels: %v", err)
	}
	got := map[string]bool{}
	for _, label := range labels {
		got[label] = true
	}
	for _, want := range []string{"gc:session", "agent:worker"} {
		if !got[want] {
			t.Fatalf("labels = %v, missing %q", labels, want)
		}
	}
}

func TestSmokeChildIDAndDependencyUseSQLiteDialect(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	now := time.Now().UTC()
	parent := &types.Issue{
		ID:        "bd-parent",
		Title:     "parent",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	child := &types.Issue{
		ID:        "bd-parent.1",
		Title:     "child",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, parent, "test"); err != nil {
		t.Fatalf("CreateIssue parent: %v", err)
	}
	if err := store.CreateIssue(ctx, child, "test"); err != nil {
		t.Fatalf("CreateIssue child: %v", err)
	}

	next, err := store.GetNextChildID(ctx, parent.ID)
	if err != nil {
		t.Fatalf("GetNextChildID: %v", err)
	}
	if next != "bd-parent.2" {
		t.Fatalf("next child ID = %q, want bd-parent.2", next)
	}

	dep := &types.Dependency{
		IssueID:     child.ID,
		DependsOnID: parent.ID,
		Type:        types.DepParentChild,
	}
	if err := store.AddDependency(ctx, dep, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	deps, err := store.GetDependencyRecords(ctx, child.ID)
	if err != nil {
		t.Fatalf("GetDependencyRecords: %v", err)
	}
	if len(deps) != 1 || deps[0].DependsOnID != parent.ID || deps[0].Type != types.DepParentChild {
		t.Fatalf("deps = %#v, want parent-child to %s", deps, parent.ID)
	}
}

func TestSmokeCloseUsesSQLiteBlockedRecompute(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	now := time.Now().UTC()
	blocked := &types.Issue{
		ID:        "bd-blocked",
		Title:     "blocked",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	blocker := &types.Issue{
		ID:        "bd-blocker",
		Title:     "blocker",
		Status:    types.StatusOpen,
		Priority:  2,
		IssueType: types.TypeTask,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.CreateIssue(ctx, blocked, "test"); err != nil {
		t.Fatalf("CreateIssue blocked: %v", err)
	}
	if err := store.CreateIssue(ctx, blocker, "test"); err != nil {
		t.Fatalf("CreateIssue blocker: %v", err)
	}
	if err := store.AddDependency(ctx, &types.Dependency{
		IssueID:     blocked.ID,
		DependsOnID: blocker.ID,
		Type:        types.DepBlocks,
	}, "test"); err != nil {
		t.Fatalf("AddDependency: %v", err)
	}
	isBlocked, _, err := store.IsBlocked(ctx, blocked.ID)
	if err != nil {
		t.Fatalf("IsBlocked before close: %v", err)
	}
	if !isBlocked {
		t.Fatalf("%s should be blocked before closing %s", blocked.ID, blocker.ID)
	}

	if err := store.CloseIssue(ctx, blocker.ID, "done", "test", "sess"); err != nil {
		t.Fatalf("CloseIssue: %v", err)
	}
	isBlocked, _, err = store.IsBlocked(ctx, blocked.ID)
	if err != nil {
		t.Fatalf("IsBlocked after close: %v", err)
	}
	if isBlocked {
		t.Fatalf("%s should be unblocked after closing %s", blocked.ID, blocker.ID)
	}
}

func TestSmokeVersionControl(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := store.CommitWithConfig(ctx, "test: config"); err != nil {
		t.Fatalf("Commit config: %v", err)
	}

	branch, err := store.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}

	if err := store.Branch(ctx, "feature"); err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if err := store.Checkout(ctx, "feature"); err != nil {
		t.Fatalf("Checkout feature: %v", err)
	}
	branch, err = store.CurrentBranch(ctx)
	if err != nil {
		t.Fatalf("CurrentBranch feature: %v", err)
	}
	if branch != "feature" {
		t.Fatalf("branch = %q, want feature", branch)
	}

	branches, err := store.ListBranches(ctx)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) < 2 {
		t.Fatalf("branches = %v, want at least main and feature", branches)
	}

	if err := store.Checkout(ctx, "main"); err != nil {
		t.Fatalf("Checkout main: %v", err)
	}
	if err := store.DeleteBranch(ctx, "feature"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}

	if _, err := store.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if commits, err := store.Log(ctx, 5); err != nil {
		t.Fatalf("Log: %v", err)
	} else if len(commits) == 0 {
		t.Fatal("Log returned no commits")
	}
	if hash, err := store.GetCurrentCommit(ctx); err != nil {
		t.Fatalf("GetCurrentCommit: %v", err)
	} else if hash == "" {
		t.Fatal("GetCurrentCommit returned empty hash")
	}
}

func TestCommitPendingSkipsWispOnlyChanges(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := store.CommitWithConfig(ctx, "test: config"); err != nil {
		t.Fatalf("Commit config: %v", err)
	}
	requireDoltliteClean(t, store)
	before := doltliteCommitCount(t, store)

	now := time.Now().UTC()
	wisps := []*types.Issue{
		{
			ID:          "bd-wisp-ephemeral",
			Title:       "ephemeral wisp",
			Description: "operational state",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeTask,
			Ephemeral:   true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "bd-wisp-no-history",
			Title:       "no-history wisp",
			Description: "operational state",
			Status:      types.StatusOpen,
			Priority:    1,
			IssueType:   types.TypeTask,
			NoHistory:   true,
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}
	if err := store.CreateIssues(ctx, wisps, "test"); err != nil {
		t.Fatalf("CreateIssues: %v", err)
	}

	committed, err := store.CommitPending(ctx, "test")
	if err != nil {
		t.Fatalf("CommitPending: %v", err)
	}
	if committed {
		status, statusErr := store.Status(ctx)
		t.Fatalf("CommitPending committed wisp-only changes; status=%+v statusErr=%v", status, statusErr)
	}
	after := doltliteCommitCount(t, store)
	if after != before {
		t.Fatalf("commit count changed after wisp-only writes: before=%d after=%d", before, after)
	}
}

func TestCommitPendingCommitsPermanentIssue(t *testing.T) {
	ctx := t.Context()
	store, err := doltlite.New(ctx, filepath.Join(t.TempDir(), ".beads"), "beads", "main")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if err := store.CommitWithConfig(ctx, "test: config"); err != nil {
		t.Fatalf("Commit config: %v", err)
	}
	before := doltliteCommitCount(t, store)

	now := time.Now().UTC()
	issue := &types.Issue{
		ID:          "bd-permanent",
		Title:       "permanent issue",
		Description: "versioned state",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.CreateIssue(ctx, issue, "test"); err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}

	committed, err := store.CommitPending(ctx, "test")
	if err != nil {
		t.Fatalf("CommitPending: %v", err)
	}
	if !committed {
		t.Fatal("CommitPending did not commit permanent issue")
	}
	after := doltliteCommitCount(t, store)
	if after != before+1 {
		t.Fatalf("commit count after permanent write = %d, want %d", after, before+1)
	}
}

func TestCommitPendingRefreshesStaleConnectionAfterConcurrentCommit(t *testing.T) {
	ctx := t.Context()
	beadsDir := filepath.Join(t.TempDir(), ".beads")

	bootstrap, err := doltlite.New(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatalf("bootstrap New: %v", err)
	}
	if err := bootstrap.SetConfig(ctx, "issue_prefix", "bd"); err != nil {
		t.Fatalf("bootstrap SetConfig: %v", err)
	}
	if err := bootstrap.CommitWithConfig(ctx, "test: bootstrap config"); err != nil {
		t.Fatalf("bootstrap CommitWithConfig: %v", err)
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatalf("bootstrap Close: %v", err)
	}

	stale, err := doltlite.New(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatalf("stale New: %v", err)
	}
	t.Cleanup(func() { _ = stale.Close() })

	peer, err := doltlite.New(ctx, beadsDir, "beads", "main")
	if err != nil {
		t.Fatalf("peer New: %v", err)
	}
	t.Cleanup(func() { _ = peer.Close() })

	base := doltliteCommitCount(t, stale)
	now := time.Now().UTC()
	peerIssue := &types.Issue{
		ID:          "bd-peer",
		Title:       "peer commit",
		Description: "advance branch from another connection",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := peer.CreateIssue(ctx, peerIssue, "peer"); err != nil {
		t.Fatalf("peer CreateIssue: %v", err)
	}
	committed, err := peer.CommitPending(ctx, "peer")
	if err != nil {
		t.Fatalf("peer CommitPending: %v", err)
	}
	if !committed {
		t.Fatal("peer CommitPending did not commit")
	}
	afterPeer := doltliteCommitCount(t, peer)
	if afterPeer != base+1 {
		t.Fatalf("commit count after peer write = %d, want %d", afterPeer, base+1)
	}

	staleIssue := &types.Issue{
		ID:          "bd-stale",
		Title:       "stale commit",
		Description: "commit from a connection opened before peer advanced HEAD",
		Status:      types.StatusOpen,
		Priority:    2,
		IssueType:   types.TypeTask,
		CreatedAt:   now.Add(time.Second),
		UpdatedAt:   now.Add(time.Second),
	}
	if err := stale.CreateIssue(ctx, staleIssue, "stale"); err != nil {
		t.Fatalf("stale CreateIssue: %v", err)
	}
	committed, err = stale.CommitPending(ctx, "stale")
	if err != nil {
		t.Fatalf("stale CommitPending after peer commit: %v", err)
	}
	if !committed {
		t.Fatal("stale CommitPending did not commit")
	}
	afterStale := doltliteCommitCount(t, stale)
	if afterStale != afterPeer+1 {
		t.Fatalf("commit count after stale write = %d, want %d", afterStale, afterPeer+1)
	}

	if got, err := stale.GetIssue(ctx, peerIssue.ID); err != nil || got == nil {
		t.Fatalf("peer issue missing after stale commit: issue=%+v err=%v", got, err)
	}
	if got, err := stale.GetIssue(ctx, staleIssue.ID); err != nil || got == nil {
		t.Fatalf("stale issue missing after stale commit: issue=%+v err=%v", got, err)
	}
}
