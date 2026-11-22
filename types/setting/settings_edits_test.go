package setting

import (
	"encoding/json"
	"testing"
)

func TestSettingsEditsPreserveIndependentChangesAndLastWriteWins(t *testing.T) {
	base := App{Editor: Editor{TabSize: 4}}
	a, b := base, base
	a.Editor.TabSize = 8
	b.LSP.Enabled = true
	edits := Edits(base, a)
	if err := ApplyEdits(&b, edits); err != nil {
		t.Fatal(err)
	}
	if !b.LSP.Enabled || b.Editor.TabSize != 8 {
		t.Fatal("independent edit lost")
	}
	if err := ApplyEdits(&b, edits); err != nil {
		t.Fatal("idempotent save", err)
	}
	b.Editor.TabSize = 2
	if err := ApplyEdits(&b, edits); err != nil || b.Editor.TabSize != 8 || !b.LSP.Enabled {
		t.Fatal("last explicit write was rejected or changed a neighbor", err)
	}
}

func TestWorkspaceEditsRetainPresenceAndExplicitFalse(t *testing.T) {
	base := WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":4}`)}
	before := MergeAppWorkspace(App{}, base)
	after := MergeAppWorkspace(App{}, base)
	after.LSP.Enabled = false
	edits := Edits(before, after)
	if err := ApplyEdits(&base, edits); err != nil {
		t.Fatal(err)
	}
	if string(base.Editor) != `{"tab_size":4}` || string(base.LSP) != `{"enabled":false}` {
		t.Fatalf("unexpected writes: %+v", base)
	}
}

func TestStoredSettingsInvalidLeafInheritsWithoutPoisoningNeighbors(t *testing.T) {
	value := App{Editor: Editor{TabSize: 4}}
	if err := DecodeStored([]byte(`{"editor":{"tab_size":"obsolete","word_wrap":true},"removed":42}`), &value); err != nil {
		t.Fatal(err)
	}
	if value.Editor.TabSize != 4 || !value.Editor.WordWrap {
		t.Fatal("stale leaf lost valid neighbor or parent")
	}
}

// Removed worktree-hygiene thresholds may remain in stored config files. They
// must be ignored without losing the agent settings stored beside them.
func TestStoredRemovedExecutionHygieneIsIgnored(t *testing.T) {
	var value App
	stored := `{"agents":{"MaxConcurrent":3,"execution_hygiene":{"warnings":false,"retained_count":0}}}`
	if err := DecodeStored([]byte(stored), &value); err != nil {
		t.Fatal(err)
	}
	if value.Agents.MaxConcurrent != 3 {
		t.Fatalf("removed setting poisoned its neighbors: %+v", value.Agents)
	}
}
