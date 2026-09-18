package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/types"
)

func TestAutosaveSettingsRoundTripAndDefaultOff(t *testing.T) {
	defaults := types.AppSettings{}
	types.ApplyAppDefaults(&defaults)
	if defaults.Editor.AutoSave != types.AutoSaveOff || defaults.Editor.AutoSaveDelay != 1000 {
		t.Fatalf("defaults=%+v", defaults.Editor)
	}

	want := types.EditorSettings{TabSize: 4, AutoSave: types.AutoSaveTerminalFocusLoss, AutoSaveDelay: 1750, FormatOnSave: true}
	model := New(theme.DefaultDark(), types.AppSettings{Editor: want}, nil)
	got := model.AppSettings().Editor
	if got.AutoSave != want.AutoSave || got.AutoSaveDelay != want.AutoSaveDelay || !got.FormatOnSave {
		t.Fatalf("editor settings=%+v want %+v", got, want)
	}
}
