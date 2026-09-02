package settings

import (
	"reflect"
	"testing"

	"crude/tui/theme"
	"crude/tui/types"
)

func TestLocalHistorySettingsRoundTrip(t *testing.T) {
	want := types.LocalHistorySettings{Enabled: "on", UseGitIgnore: "off", CaptureExternalChanges: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 25, DebounceMS: 300, ReconcileIntervalSeconds: 15, RetentionKeepHours: 12, RetentionKeepDaily: 7, RetentionMaxSnapshots: 120, StorageBudgetMB: 256, ExtraIgnorePatterns: []string{"vendor/", "*.generated"}}
	model := New(theme.DefaultDark(), types.AppSettings{LocalHistory: want}, nil)
	got := model.AppSettings().LocalHistory
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("local history settings\n got=%#v\nwant=%#v", got, want)
	}
}

func TestLocalHistoryDefaultsAreUsable(t *testing.T) {
	settings := types.AppSettings{}
	types.ApplyAppDefaults(&settings)
	history := settings.LocalHistory
	if !history.EnabledValue() || !history.UseGitIgnoreValue() || !history.CaptureExternalValue() || !history.SafetySnapshotValue() || history.MaxFileSizeMB <= 0 || history.StorageBudgetMB <= 0 {
		t.Fatalf("defaults=%+v", history)
	}
}
