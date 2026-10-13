package workspace

import (
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

func TestWorkspaceCanExplicitlyDisableInheritedAutosave(t *testing.T) {
	setTestHome(t)
	w := &Workspace{WorkspaceDB: openTestWorkspaceDB(t), Logger: testLogger{}}
	app := ttypes.AppSettings{Editor: ttypes.EditorSettings{AutoSave: ttypes.AutoSaveAfterDelay, AutoSaveDelay: 900}}
	ttypes.ApplyAppDefaults(&app)
	w.SetAppDefaults(app)
	working := app
	working.Editor.AutoSave = ttypes.AutoSaveOff
	if err := w.SaveWorkspaceSettings(ttypes.OverridesFromDiff(app, working)); err != nil {
		t.Fatal(err)
	}
	if got := w.LoadAppSettings().Editor.AutoSave; got != ttypes.AutoSaveOff {
		t.Fatalf("effective mode=%q", got)
	}
	if got := w.AppDefaults().Editor.AutoSave; got != ttypes.AutoSaveAfterDelay {
		t.Fatalf("app default mutated to %q", got)
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
