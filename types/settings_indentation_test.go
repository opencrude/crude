package types

import (
	"encoding/json"
	"testing"
)

func TestIndentationSettingsExplicitDetectAndReset(t *testing.T) {
	base := AppSettings{Editor: EditorSettings{TabSize: 8, IndentStyle: "tab", IndentSize: 4}}
	overlay := WorkspaceOverrides{Editor: json.RawMessage(`{"indent_style":"","indent_size":0}`)}
	current := MergeAppWorkspace(base, overlay)
	if current.Editor.IndentStyle != "" || current.Editor.IndentSize != 0 || current.Editor.TabSize != 8 {
		t.Fatal("explicit Detect did not override parent", current.Editor)
	}
	if err := ApplySettingsEdits(&overlay, []SettingEdit{{Path: []string{"editor", "indent_style"}}, {Path: []string{"editor", "indent_size"}}}); err != nil {
		t.Fatal(err)
	}
	current = MergeAppWorkspace(base, overlay)
	if current.Editor.IndentStyle != "tab" || current.Editor.IndentSize != 4 {
		t.Fatal("Reset did not restore live parent", current.Editor)
	}
}
