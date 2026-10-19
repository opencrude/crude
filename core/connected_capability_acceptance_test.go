package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestConnectedCapabilityReviewedInstallRejectsChangedBinding(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	directory := filepath.Join(root, "source", "guide")
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := skills.Create(directory, "guide", "Acceptance skill instructions."); err != nil {
		t.Fatal(err)
	}
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
		app := ws.AppDefaults()
		app.Capabilities.Sources["local"] = captypes.SourceConfig{ID: "local", Name: "Local", Kind: "local", Location: filepath.Dir(directory), Trusted: true}
		ws.SetAppDefaults(app)
		if _, err := ws.CapabilitySnapshot(t.Context()); err != nil {
			ws.Close()
			return nil, err
		}
		ws.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
			next := ws.AppDefaults()
			err := types.ApplySettingsEdits(&next, edits)
			return next, err
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	client, err := runtime.Connect("capability-acceptance")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	attached := callCore(t, client, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
	var attachment coreprotocol.AttachResult
	if err := json.Unmarshal(attached.Payload, &attachment); err != nil {
		t.Fatal(err)
	}
	wsID := attachment.WorkspaceID
	fetch := func(id string) coreprotocol.SettingsFetchResult {
		result := callCore(t, client, id, wsID, coreprotocol.SettingsFetch, struct{}{})
		var value coreprotocol.SettingsFetchResult
		if err := json.Unmarshal(result.Payload, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	search := callCore(t, client, "discover", wsID, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: captypes.Action{Operation: "discover", Kind: captypes.KindSkill, Query: "guide", Scope: captypes.ScopeApplication, Arguments: map[string]string{"source_id": "local"}}})
	var discovered coreprotocol.CapabilityResult
	if err := json.Unmarshal(search.Payload, &discovered); err != nil {
		t.Fatal(err)
	}
	if len(discovered.Entries) != 1 {
		t.Fatal("exact local source did not return skill")
	}
	action := captypes.Action{Operation: "install", ID: discovered.Entries[0].ID, Kind: captypes.KindSkill, Scope: captypes.ScopeApplication}
	previewResponse := callCore(t, client, "preview", wsID, coreprotocol.CapabilityAcquisitionPreview, coreprotocol.CapabilityActionRequest{Action: action})
	var preview coreprotocol.CapabilityPreviewResult
	if err := json.Unmarshal(previewResponse.Payload, &preview); err != nil {
		t.Fatal(err)
	}
	passive := fetch("passive")
	if preview.Preview.Token == "" || len(passive.Application.Capabilities.Skills) != 0 {
		t.Fatal("preview lacked exact token or published binding")
	}
	action.Preview = &preview.Preview
	callCore(t, client, "approve", wsID, coreprotocol.CapabilityAction, coreprotocol.CapabilityActionRequest{Action: action})
	installed := fetch("installed")
	if !installed.Application.Capabilities.Skills[action.ID].Managed {
		t.Fatal("approved exact binding did not publish")
	}
	// The old exact installation review cannot authorize a replacement binding.
	raw, _ := json.Marshal(coreprotocol.CapabilityActionRequest{Action: action})
	refused := client.Call(t.Context(), coreprotocol.Request{ID: "old-preview", Owner: client.Owner, WorkspaceID: wsID, Method: coreprotocol.CapabilityAction, Payload: raw})
	if refused.Error == nil {
		t.Fatal("changed binding accepted old preview")
	}
	final := fetch("final")
	if final.CapabilitiesRevision != installed.CapabilitiesRevision {
		t.Fatal("failed approval changed binding")
	}
}
