package types

import (
	"encoding/json"
	"testing"
)

func TestSettingsEditsPreserveIndependentChangesAndLastWriteWins(t *testing.T) {
	base := AppSettings{Editor: EditorSettings{TabSize: 4}}
	a, b := base, base
	a.Editor.TabSize = 8
	b.LSP.Enabled = true
	edits := SettingsEdits(base, a)
	if err := ApplySettingsEdits(&b, edits); err != nil {
		t.Fatal(err)
	}
	if !b.LSP.Enabled || b.Editor.TabSize != 8 {
		t.Fatal("independent edit lost")
	}
	if err := ApplySettingsEdits(&b, edits); err != nil {
		t.Fatal("idempotent save", err)
	}
	b.Editor.TabSize = 2
	if err := ApplySettingsEdits(&b, edits); err != nil || b.Editor.TabSize != 8 || !b.LSP.Enabled {
		t.Fatal("last explicit write was rejected or changed a neighbor", err)
	}
}

func TestWorkspaceEditsRetainPresenceAndExplicitFalse(t *testing.T) {
	base := WorkspaceOverrides{Editor: json.RawMessage(`{"tab_size":4}`)}
	before := MergeAppWorkspace(AppSettings{}, base)
	after := MergeAppWorkspace(AppSettings{}, base)
	after.LSP.Enabled = false
	edits := SettingsEdits(before, after)
	if err := ApplySettingsEdits(&base, edits); err != nil {
		t.Fatal(err)
	}
	if string(base.Editor) != `{"tab_size":4}` || string(base.LSP) != `{"enabled":false}` {
		t.Fatalf("unexpected writes: %+v", base)
	}
}

func TestStoredSettingsInvalidLeafInheritsWithoutPoisoningNeighbors(t *testing.T) {
	value := AppSettings{Editor: EditorSettings{TabSize: 4}}
	if err := DecodeStoredSettings([]byte(`{"editor":{"tab_size":"obsolete","word_wrap":true},"removed":42}`), &value); err != nil {
		t.Fatal(err)
	}
	if value.Editor.TabSize != 4 || !value.Editor.WordWrap {
		t.Fatal("stale leaf lost valid neighbor or parent")
	}
}
