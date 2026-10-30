package core

import (
	"encoding/json"
	"testing"

	"crude/coreprotocol"
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
		{Path: []string{"analysis", "trust"}, After: json.RawMessage(`"project_execution_allowed"`)},
		{Path: []string{"analysis"}, After: json.RawMessage(`{"trust":"project_execution_allowed"}`)},
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
