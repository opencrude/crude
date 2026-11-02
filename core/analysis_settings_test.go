package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

func TestApplicationPreferenceIngressRejectsWorkspaceAnalysisAuthority(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(ws.Close)
	calls := 0
	ws.OnAppPreferencesCommit = func([]types.SettingEdit) (types.AppSettings, error) {
		calls++
		return ws.AppDefaults(), nil
	}
	a := &actor{ws: ws}
	for _, edit := range []types.SettingEdit{
		{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"trusted"`)},
		{Path: []string{"analysis"}, After: json.RawMessage(`{"trust":"trusted"}`)},
		{Path: []string{"analysis", "trust"}},
		{Path: []string{"analysis"}},
	} {
		in := coreprotocol.ApplicationPreferencesRequest{Edits: []types.SettingEdit{edit}}
		payload, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		run, _, known := a.prepareApplicationSettings(coreprotocol.Request{
			Method: coreprotocol.ApplicationPreferencesSave, Payload: payload, JSON: true,
		})
		if !known {
			t.Fatal("application preference request not recognized")
		}
		result := run(t.Context())
		if result.Error == nil || calls != 0 {
			t.Fatal("application preference ingress admitted workspace trust", edit.Path, result.Error, calls)
		}
	}
}

func TestAnalysisCoreReplacedFolderAsksAgainWithoutNotice(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	// Seed the old folder through the real preference transaction, then close
	// its resources before replacing the native directory at the same path.
	original := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
	t.Cleanup(original.Close)
	result := original.PreparePreferencesSave(types.SettingsScopeWorkspace, []types.SettingEdit{
		{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"trusted"`)},
	})()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	original.ApplyPreferencesSave(result)
	binding := result.Overrides.AnalysisRootIdentity
	if binding == "" || !result.Effective.Analysis.Trusted() {
		t.Fatal("old folder was not durably trusted", result)
	}
	original.Close()
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package replacement\n"), 0600); err != nil {
		t.Fatal(err)
	}

	engine := &countedAnalysisRuntime{engine: &semanticLSPClient{}}
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		ws.LSPMgr.SetRuntime(engine)
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	client := newResourceWire(t, r, "replaced-folder")
	var attached coreprotocol.AttachResult
	client.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
	var settings coreprotocol.SettingsFetchResult
	client.call(t, attached.WorkspaceID, coreprotocol.SettingsFetch, coreprotocol.Empty{}, &settings)
	if !settings.Effective.Analysis.DecisionNeeded() || settings.Effective.Analysis.Trusted() {
		t.Fatal("replacement inherited the old folder's Trusted choice", settings.Effective.Analysis)
	}
	published := make(chan coreprotocol.WorkspaceState, 4)
	subscription := client.c.Subscribe(t.Context(), attached.WorkspaceID, coreprotocol.TopicSettings, "", func(_ coreprotocol.TopicHeader, body []byte) error {
		var state coreprotocol.WorkspaceState
		if err := coreprotocol.Decode(body, &state); err != nil {
			return err
		}
		select {
		case published <- state:
			return nil
		case <-t.Context().Done():
			return t.Context().Err()
		}
	})
	t.Cleanup(subscription.Close)
	select {
	case state := <-published:
		if state.Settings == nil || !state.Settings.Effective.Analysis.DecisionNeeded() ||
			state.Settings.Effective.Analysis.Trusted() || state.Settings.Overrides.AnalysisRootIdentity != binding {
			t.Fatal("pushed settings disagreed with the bound replacement policy", state.Settings)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement settings were not published to the shell")
	}
	var stored coreprotocol.OverridesResult
	client.call(t, attached.WorkspaceID, coreprotocol.WorkspaceOverrides, coreprotocol.Empty{}, &stored)
	if stored.Overrides.AnalysisRootIdentity != binding {
		t.Fatal("attachment rebound the old choice to the replacement folder")
	}
	// Restricted replacement folders still open and edit through the actual
	// framed document entry points, without constructing a language server.
	var source document.Snapshot
	client.call(t, attached.WorkspaceID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &source)
	client.call(t, attached.WorkspaceID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{
		ID: source.ID, Revision: source.Revision,
		Edits: []document.Edit{{Start: len(source.Text), End: len(source.Text), Text: "// editable\n"}},
	}, nil)
	client.call(t, attached.WorkspaceID, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: source.ID}, &source)
	if string(source.Text) != "package replacement\n// editable\n" || !source.Dirty || engine.constructions.Load() != 0 {
		t.Fatal("restricted replacement lost editing or ran a project tool", source, engine.constructions.Load())
	}
	var status coreprotocol.StatusSnapshot
	client.call(t, attached.WorkspaceID, coreprotocol.StatusSnapshotMethod, coreprotocol.Empty{}, &status)
	for _, notice := range status.Notifications {
		if notice.Source == "analysis" {
			t.Fatalf("ordinary folder replacement posted an error/warning instead of asking again: %+v", notice)
		}
	}
}
