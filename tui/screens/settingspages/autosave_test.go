package settingspages

import (
	"testing"

	"crude/tui/theme"
	"crude/types/setting"
)

func TestAutosaveSettingsRoundTripAndDefaultOff(t *testing.T) {
	defaults := setting.App{}
	setting.ApplyAppDefaults(&defaults)
	if defaults.Editor.AutoSave != setting.AutoSaveOff || defaults.Editor.AutoSaveDelay != 1000 {
		t.Fatalf("defaults=%+v", defaults.Editor)
	}

	want := setting.Editor{TabSize: 4, AutoSave: setting.AutoSaveTerminalFocusLoss, AutoSaveDelay: 1750, FormatOnSave: true}
	model := New(theme.DefaultDark(), setting.App{Editor: want}, nil)
	got := model.AppSettings().Editor
	if got.AutoSave != want.AutoSave || got.AutoSaveDelay != want.AutoSaveDelay || !got.FormatOnSave {
		t.Fatalf("editor settings=%+v want %+v", got, want)
	}
}
