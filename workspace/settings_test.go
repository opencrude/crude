package workspace

import (
	"encoding/json"
	"testing"

	ttypes "crude/types"
)

func TestWorkspaceSettingsOverlayDoesNotMutateAppDefaults(t *testing.T) {
	setTestHome(t)
	w := &Workspace{WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	app := ttypes.AppSettings{
		Theme:  ttypes.ThemeSettings{Preset: "dark"},
		Editor: ttypes.EditorSettings{TabSize: 4, WordWrap: true},
	}
	ttypes.ApplyAppDefaults(&app)
	w.SetAppDefaults(app)

	editor := app.Editor
	editor.TabSize = 8
	if err := w.SaveWorkspaceSettings(ttypes.WorkspaceOverrides{Editor: &editor}); err != nil {
		t.Fatal(err)
	}
	if got := w.AppDefaults().Editor.TabSize; got != 4 {
		t.Fatalf("application defaults mutated: tab size %d", got)
	}
	effective := w.LoadAppSettings()
	if effective.Editor.TabSize != 8 || !effective.Editor.WordWrap {
		t.Fatalf("effective editor overlay missing: %#v", effective.Editor)
	}
	if effective.Theme.Preset != "dark" {
		t.Fatalf("theme should inherit from app, got %q", effective.Theme.Preset)
	}
}

func TestLoadWorkspaceOverridesMigratesLegacyAppSettings(t *testing.T) {
	setTestHome(t)
	wdb := openTestWorkspaceDB(t)
	w := &Workspace{WorkspaceDB: wdb, Logger: testLogger{}}
	app := ttypes.AppSettings{Theme: ttypes.ThemeSettings{Preset: "dark"}, Editor: ttypes.EditorSettings{TabSize: 4}}
	ttypes.ApplyAppDefaults(&app)
	w.SetAppDefaults(app)

	legacy := app
	legacy.Theme.Preset = "light"
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := wdb.SetSetting(legacyAppSettingsDBKey, string(data)); err != nil {
		t.Fatal(err)
	}
	got := w.LoadWorkspaceOverrides()
	if got.Theme == nil || got.Theme.Preset != "light" {
		t.Fatalf("legacy app_settings should become a theme override: %#v", got)
	}
	if got.Editor != nil {
		t.Fatalf("unchanged editor should inherit, got %#v", got.Editor)
	}
	if w.LoadAppSettings().Theme.Preset != "light" {
		t.Fatalf("effective theme=%q want light", w.LoadAppSettings().Theme.Preset)
	}
}
