package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestProjectToolDecisionsInertApplicationWriters(t *testing.T) {
	restore := setTestHome(t, t.TempDir())
	defer restore()
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(), []byte(`{"capabilities":{"project_tool_decisions":{"z":"<value>","a":[1,2]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := LoadFileConfig()
	if got := FileConfigToAppSettings(loaded).Capabilities.ProjectToolDecisions; got != nil {
		t.Fatalf("application decision exposed: %s", got)
	}
	application, err := New(Opts{FC: loaded})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.commitCorePreferences([]types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage(`6`)}}); err != nil {
		t.Fatalf("ordinary preference blocked: %v", err)
	}
	for _, path := range [][]string{{"capabilities"}, {"capabilities", "project_tool_decisions"}, {"capabilities", "project_tool_decisions", "x"}} {
		for _, value := range []json.RawMessage{nil, json.RawMessage(`{}`)} {
			if _, err := application.commitCorePreferences([]types.SettingEdit{{Path: path, After: value}}); err == nil {
				t.Fatalf("accepted authority write %v", path)
			}
		}
	}
	if err := SaveFileConfig(FileConfig{Editor: &types.EditorSettings{TabSize: 7}}); err != nil {
		t.Fatalf("full config save blocked: %v", err)
	}
	stored := LoadFileConfig()
	if stored.Editor == nil || stored.Editor.TabSize != 7 || stored.Capabilities == nil || len(stored.Capabilities.ProjectToolDecisions) == 0 {
		t.Fatalf("lost neighbor or inert data: %+v", stored)
	}
	forged := stored
	forged.Capabilities = &captypes.Settings{ProjectToolDecisions: json.RawMessage(`{"forged":true}`)}
	if err := SaveFileConfig(forged); err == nil {
		t.Fatal("full config accepted forged decision")
	}
}

func TestPreparedCapabilityCommitAcceptsUnchangedDurableSettings(t *testing.T) {
	restore := setTestHome(t, t.TempDir())
	defer restore()
	application, err := New(Opts{FC: DefaultFileConfig()})
	if err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, &appLogger{})
	defer ws.Close()
	application.configureWorkspace(ws)
	if _, err := ws.CapabilitySnapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	var captured types.AppSettings
	prepared, err := ws.PrepareCapabilities(func(_ context.Context, before, after types.AppSettings, scope captypes.Scope) (types.AppSettings, error) {
		captured = before
		return application.commitCorePreferences(types.SettingsEdits(before, after))
	})
	if err != nil {
		t.Fatal(err)
	}
	source := ws.AppDefaults().Capabilities.Sources[captypes.SkillsDirectorySourceID]
	source.Name = "My skill directory"
	_, _, _, published, err := prepared.PrepareAction(captypes.Action{Operation: "save-source", ID: source.ID, Kind: "source", Source: &source, Scope: captypes.ScopeApplication})(t.Context())
	if err != nil {
		t.Fatalf("unchanged durable settings refused capability publication: %v", err)
	}
	if published.Capabilities.Sources[source.ID].Name != source.Name || FileConfigToAppSettings(LoadFileConfig()).Capabilities.Sources[source.ID].Name != source.Name {
		t.Fatal("capability commit did not persist the selected source")
	}
	// A source edit is independent of theme, even from the earlier snapshot.
	after := captured
	after.Theme.Preset = "light"
	if _, err := application.commitCorePreferences(types.SettingsEdits(captured, after)); err != nil {
		t.Fatalf("independent theme edit refused: %v", err)
	}
	if FileConfigToAppSettings(LoadFileConfig()).Capabilities.Sources[source.ID].Name != source.Name {
		t.Fatal("theme save erased source edit")
	}
}
