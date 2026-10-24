package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/provider"
	"crude/types"
	"crude/workspace"
)

func TestSettingsLaneCapturesOnlyAfterPriorPublication(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "test", types.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "p", Label: "before"}}})
		baseline := ws.AppDefaults()
		ws.OnAppAccountCommit = func(change types.AccountChange) (types.AppSettings, error) {
			close(entered)
			<-release
			baseline.Accounts = map[string]provider.Account{"one": *change.After}
			return baseline, nil
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c, err := r.Connect("lane-client")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	attached := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()})
	var owner coreprotocol.AttachResult
	if err = coreprotocol.Decode(attached.Payload, &owner); err != nil {
		t.Fatal(err)
	}
	initial := callCore(t, c, "fetch", owner.WorkspaceID, coreprotocol.SettingsFetch, coreprotocol.Empty{})
	var fetched coreprotocol.SettingsFetchResult
	if err = coreprotocol.Decode(initial.Payload, &fetched); err != nil {
		t.Fatal(err)
	}
	draft := coreprotocol.AccountDraft{ID: "one", ProviderID: "p", Label: "after", Endpoint: coreprotocol.SecretString{Keep: true}}
	body, _ := coreprotocol.Marshal(coreprotocol.Ptr(coreprotocol.AccountChangeRequest{ID: "one", After: &draft}))
	changed := c.Dispatch(context.Background(), coreprotocol.Request{ID: "change", Owner: c.Owner, WorkspaceID: owner.WorkspaceID, Method: coreprotocol.AccountChange, Payload: body})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("commit did not start")
	}
	later := c.Dispatch(context.Background(), coreprotocol.Request{ID: "later", Owner: c.Owner, WorkspaceID: owner.WorkspaceID, Method: coreprotocol.SettingsFetch, Payload: emptyPayload})
	// This independent read proves the actor has admitted both messages while
	// the settings worker remains blocked; settings fetch must not capture A.
	callCore(t, c, "barrier", owner.WorkspaceID, coreprotocol.Snapshot, coreprotocol.Empty{})
	select {
	case <-later:
		t.Fatal("settings read overtook uncommitted settings writer")
	default:
	}
	close(release)
	if result := <-changed; result.Error != nil {
		t.Fatal(result.Error)
	}
	result := <-later
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	if err = coreprotocol.Decode(result.Payload, &fetched); err != nil {
		t.Fatal(err)
	}
	if fetched.Accounts["one"].Label != "after" {
		t.Fatal("queued read captured stale policy before preceding apply")
	}
}
