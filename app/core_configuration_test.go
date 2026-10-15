package app

import (
	"context"
	"errors"
	"testing"

	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

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
		return application.commitCorePreferences(before, after)
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
	// Actual concurrent preference changes must still refuse the old preimage.
	after := captured
	after.Theme.Preset = "light"
	if _, err := application.commitCorePreferences(captured, after); !errors.Is(err, workspace.ErrApplicationSettingsConflict) {
		t.Fatalf("changed durable preimage was accepted: %v", err)
	}
}
