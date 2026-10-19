package core

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func TestCapabilityObservationDoesNotWaitForSettingsWriter(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	entered, release := make(chan struct{}), make(chan struct{})
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
		w.OnAppPreferencesCommit = func(edits []types.SettingEdit) (types.AppSettings, error) {
			close(entered)
			<-release
			next := w.AppDefaults()
			err := types.ApplySettingsEdits(&next, edits)
			return next, err
		}
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(release); _ = runtime.Close() })
	client, err := runtime.Connect("settings-observation")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	var attached coreprotocol.AttachResult
	if err := json.Unmarshal(callCore(t, client, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()}).Payload, &attached); err != nil {
		t.Fatal(err)
	}
	var settings coreprotocol.SettingsFetchResult
	if err := json.Unmarshal(callCore(t, client, "before", attached.WorkspaceID, coreprotocol.SettingsFetch, struct{}{}).Payload, &settings); err != nil {
		t.Fatal(err)
	}
	settings.Application.Accounts = nil
	raw, err := json.Marshal(coreprotocol.ApplicationPreferencesRequest{Edits: []types.SettingEdit{}, ProxyURL: coreprotocol.SecretUpdate[string]{Keep: true}})
	if err != nil {
		t.Fatal(err)
	}
	client.Dispatch(t.Context(), coreprotocol.Request{ID: "writer", Owner: client.Owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.ApplicationPreferencesSave, Payload: raw})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("writer never entered its held persistence phase")
	}
	for i, method := range []string{coreprotocol.CapabilityRuntime, coreprotocol.CapabilitySnapshot} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		result := client.Call(ctx, coreprotocol.Request{ID: string(rune('a' + i)), Owner: client.Owner, WorkspaceID: attached.WorkspaceID, Method: method, Payload: json.RawMessage(`{}`)})
		cancel()
		if result.Error != nil {
			t.Fatalf("passive %s blocked behind writer: %v", method, result.Error)
		}
	}
}
