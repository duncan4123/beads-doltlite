package issueops

import (
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestBuildSQLInClause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		ids              []string
		wantPlaceholders string
		wantArgs         []interface{}
	}{
		{
			name:             "single ID",
			ids:              []string{"42"},
			wantPlaceholders: "?",
			wantArgs:         []interface{}{"42"},
		},
		{
			name:             "multiple IDs",
			ids:              []string{"1", "2", "3"},
			wantPlaceholders: "?,?,?",
			wantArgs:         []interface{}{"1", "2", "3"},
		},
		{
			name:             "empty slice",
			ids:              []string{},
			wantPlaceholders: "",
			wantArgs:         []interface{}{},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotPlaceholders, gotArgs := buildSQLInClause(tt.ids)

			if gotPlaceholders != tt.wantPlaceholders {
				t.Errorf("placeholders = %q, want %q", gotPlaceholders, tt.wantPlaceholders)
			}

			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("args length = %d, want %d", len(gotArgs), len(tt.wantArgs))
			}

			for i := range gotArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %v, want %v", i, gotArgs[i], tt.wantArgs[i])
				}
			}
		})
	}
}

func TestReadyWispIssueFilterPreservesWorkFilters(t *testing.T) {
	t.Parallel()

	priority := 1
	assignee := "gascity/jj-refinery"
	parentID := "gc-parent"
	molType := types.MolTypeWork
	wispType := types.WispTypePatrol

	got := readyWispIssueFilter(types.WorkFilter{
		Status:         types.StatusOpen,
		Type:           "task",
		Priority:       &priority,
		Assignee:       &assignee,
		Unassigned:     true,
		Labels:         []string{"jj"},
		LabelsAny:      []string{"smoke", "pack"},
		ExcludeLabels:  []string{"blocked"},
		LabelPattern:   "gc-*",
		LabelRegex:     "smoke|pack",
		Limit:          7,
		ParentID:       &parentID,
		MolType:        &molType,
		WispType:       &wispType,
		ExcludeTypes:   []types.IssueType{types.TypeEpic},
		MetadataFields: map[string]string{"gc.routed_to": "gascity/jj-polecat"},
		HasMetadataKey: "molecule_id",
	})

	if got.Ephemeral == nil || !*got.Ephemeral {
		t.Fatalf("Ephemeral = %v, want true", got.Ephemeral)
	}
	if got.Status == nil || *got.Status != types.StatusOpen {
		t.Fatalf("Status = %v, want open", got.Status)
	}
	if got.IssueType == nil || *got.IssueType != types.TypeTask {
		t.Fatalf("IssueType = %v, want task", got.IssueType)
	}
	if got.Priority != &priority {
		t.Fatalf("Priority pointer not preserved")
	}
	if got.Assignee != nil {
		t.Fatalf("Assignee = %v, want nil when Unassigned is true", *got.Assignee)
	}
	if !got.NoAssignee {
		t.Fatalf("NoAssignee = false, want true")
	}
	if got.Limit != 7 {
		t.Fatalf("Limit = %d, want 7", got.Limit)
	}
	if got.ParentID != &parentID {
		t.Fatalf("ParentID pointer not preserved")
	}
	if got.MolType != &molType {
		t.Fatalf("MolType pointer not preserved")
	}
	if got.WispType != &wispType {
		t.Fatalf("WispType pointer not preserved")
	}
	if !reflect.DeepEqual(got.Labels, []string{"jj"}) {
		t.Fatalf("Labels = %v", got.Labels)
	}
	if !reflect.DeepEqual(got.LabelsAny, []string{"smoke", "pack"}) {
		t.Fatalf("LabelsAny = %v", got.LabelsAny)
	}
	if !reflect.DeepEqual(got.ExcludeLabels, []string{"blocked"}) {
		t.Fatalf("ExcludeLabels = %v", got.ExcludeLabels)
	}
	if got.LabelPattern != "gc-*" {
		t.Fatalf("LabelPattern = %q", got.LabelPattern)
	}
	if got.LabelRegex != "smoke|pack" {
		t.Fatalf("LabelRegex = %q", got.LabelRegex)
	}
	if !reflect.DeepEqual(got.ExcludeTypes, []types.IssueType{types.TypeEpic}) {
		t.Fatalf("ExcludeTypes = %v", got.ExcludeTypes)
	}
	if !reflect.DeepEqual(got.MetadataFields, map[string]string{"gc.routed_to": "gascity/jj-polecat"}) {
		t.Fatalf("MetadataFields = %v", got.MetadataFields)
	}
	if got.HasMetadataKey != "molecule_id" {
		t.Fatalf("HasMetadataKey = %q", got.HasMetadataKey)
	}
}

func TestReadyWispIssueFilterAppliesDefaultReadyTypeExclusions(t *testing.T) {
	t.Parallel()

	got := readyWispIssueFilter(types.WorkFilter{
		ExcludeTypes: []types.IssueType{types.TypeEpic},
	})

	want := []types.IssueType{
		types.IssueType("merge-request"),
		types.TypeGate,
		types.TypeMolecule,
		types.TypeMessage,
		types.IssueType("agent"),
		types.IssueType("role"),
		types.IssueType("rig"),
		types.TypeEpic,
	}
	if !reflect.DeepEqual(got.ExcludeTypes, want) {
		t.Fatalf("ExcludeTypes = %v, want %v", got.ExcludeTypes, want)
	}
}
