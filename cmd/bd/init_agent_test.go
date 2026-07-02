package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/templates/agents"
)

func TestUpdateAgentFileNoRemoteCreatesLocalOnlyDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	opts := agents.RenderOpts{HasRemote: false}
	if err := updateAgentFile(path, false, "", agents.ProfileFull, opts); err != nil {
		t.Fatalf("updateAgentFile: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	content := string(data)

	for _, stale := range []string{
		"bd dolt push",
		"bd dolt push/pull",
		"Push beads data to remote",
		"refs/dolt/data",
	} {
		if strings.Contains(content, stale) {
			t.Errorf("local-only AGENTS.md should not contain %q", stale)
		}
	}

	if !strings.Contains(content, "no configured Dolt remote") {
		t.Error("local-only AGENTS.md should explain that no Dolt remote is configured")
	}
	if !strings.Contains(content, "profile:full") {
		t.Error("local-only AGENTS.md should keep managed section metadata")
	}
}
