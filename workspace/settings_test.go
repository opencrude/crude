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

	working := app
	working.Editor.TabSize = 8
	if err := w.SaveWorkspaceSettings(ttypes.OverridesFromDiff(app, working)); err != nil {
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
	if string(got.Theme) != `{"preset":"light"}` {
		t.Fatalf("legacy app_settings should become a sparse theme override: %s", got.Theme)
	}
	if len(got.Editor) != 0 {
		t.Fatalf("unchanged editor should inherit, got %s", got.Editor)
	}
	if w.LoadAppSettings().Theme.Preset != "light" {
		t.Fatalf("effective theme=%q want light", w.LoadAppSettings().Theme.Preset)
	}
}

// A workspace that overrides one field must keep tracking the application
// layer for every field it never touched, for as long as the workspace
// exists. This is the difference between inheriting and hard-copying.
func TestWorkspaceKeepsInheritingUntouchedAppSettings(t *testing.T) {
	setTestHome(t)
	w := &Workspace{WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	app := ttypes.AppSettings{
		Editor: ttypes.EditorSettings{TabSize: 4, Minimap: true},
		LSP:    ttypes.DefaultLSPSettings(),
	}
	ttypes.ApplyAppDefaults(&app)
	w.SetAppDefaults(app)

	working := app
	working.Editor.TabSize = 2
	if err := w.SaveWorkspaceSettings(ttypes.OverridesFromDiff(app, working)); err != nil {
		t.Fatal(err)
	}

	// The application later turns off two features this workspace never set.
	app.Editor.Minimap = false
	app.LSP.Completions = false
	w.SetAppDefaults(app)

	effective := w.LoadAppSettings()
	if effective.Editor.TabSize != 2 {
		t.Fatalf("workspace override lost: %#v", effective.Editor)
	}
	if effective.Editor.Minimap {
		t.Fatal("app-level minimap off must reach a workspace that never set it")
	}
	if effective.LSP.Completions {
		t.Fatal("app-level completions off must reach a workspace that never set it")
	}
	if !effective.LSP.Hover {
		t.Fatal("inheriting must not disable unrelated features")
	}
}

// Categories a legacy whole-category row never mentioned keep inheriting,
// and clearing a category returns it to full inheritance. A stored snapshot
// cannot say which of its values the user meant, so migration keeps whatever
// still differs; "Reset section" in workspace settings is the way out.
func TestLegacyWholeCategoryOverlayNarrowsToDifferingFields(t *testing.T) {
	setTestHome(t)
	wdb := openTestWorkspaceDB(t)
	w := &Workspace{WorkspaceDB: wdb, Logger: testLogger{}}
	app := ttypes.AppSettings{
		Editor: ttypes.EditorSettings{TabSize: 4, Minimap: true},
		LSP:    ttypes.DefaultLSPSettings(),
	}
	ttypes.ApplyAppDefaults(&app)
	w.SetAppDefaults(app)

	// How older releases wrote it: one key per overridden category, each
	// holding a full snapshot of that category.
	legacyEditor := app.Editor
	legacyEditor.TabSize = 2
	editorJSON, err := json.Marshal(legacyEditor)
	if err != nil {
		t.Fatal(err)
	}
	if err := wdb.SetSetting(workspaceSettingsKey, `{"editor":`+string(editorJSON)+`}`); err != nil {
		t.Fatal(err)
	}

	app.LSP.Completions = false
	w.SetAppDefaults(app)

	overrides := w.LoadWorkspaceOverrides()
	if len(overrides.LSP) != 0 {
		t.Fatalf("a category the overlay never mentioned must inherit, got %s", overrides.LSP)
	}
	if w.LoadAppSettings().LSP.Completions {
		t.Fatal("app-level completions off must reach this workspace")
	}
	if w.LoadAppSettings().Editor.TabSize != 2 {
		t.Fatal("legacy editor override lost")
	}

	overrides.ClearCategory("editor")
	if err := w.SaveWorkspaceSettings(overrides); err != nil {
		t.Fatal(err)
	}
	if got := w.LoadAppSettings().Editor.TabSize; got != 4 {
		t.Fatalf("clearing a category must restore inheritance, tab size %d", got)
	}
}
