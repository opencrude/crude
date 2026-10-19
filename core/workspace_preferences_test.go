package core

import (
	"context"
	"encoding/json"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func TestWorkspacePreferencesWorkerPersistsBeforeActorPublishes(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{})
	a := &actor{ws: ws}
	request := coreprotocol.WorkspacePreferencesRequest{Edits: types.SettingsEdits(ws.LoadWorkspaceOverrides(), types.WorkspaceOverrides{LSP: json.RawMessage(`{"enabled":false}`)})}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	run, apply, known := a.prepareWorkspacePreferences(coreprotocol.Request{Method: coreprotocol.WorkspaceOverridesSet, Payload: payload})
	if !known || !ws.LSPMgr.Settings().Enabled {
		t.Fatal("preparation changed live projection")
	}
	out := run(context.Background())
	if out.Error != nil {
		t.Fatal(out.Error)
	}
	if !ws.LSPMgr.Settings().Enabled {
		t.Fatal("worker mutated live LSP state off actor")
	}
	apply(out)
	if ws.LSPMgr.Settings().Enabled {
		t.Fatal("actor failed to install committed preferences")
	}
	stale, _, _ := a.prepareWorkspacePreferences(coreprotocol.Request{Method: coreprotocol.WorkspaceOverridesSet, Payload: payload})
	if out := stale(context.Background()); out.Error != nil {
		t.Fatal("repeating the already-committed desired value must succeed", out.Error)
	}
}
