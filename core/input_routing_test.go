package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"crude/collaboration"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestSubmitInputSteeringReceiptReplaysWithoutPublishingAnotherInput(t *testing.T) {
	home := t.TempDir()
	setCoreTestHome(t, home)
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted work\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	var store *collaboration.Store
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "routing-model", "tester", types.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		agents := session.DefaultAgentsConfig()
		profile := agents.Profiles["default"]
		off := false
		profile.MultiAgent = &off
		profile.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "test", Model: "routing-model"}
		agents.Profiles["default"] = profile
		ws.SetBackendConfig(&session.Config{Agents: agents, Accounts: map[string]provider.Account{"test": {
			ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "Test", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol},
		}}}, home)
		store = ws.Collaboration
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	client, err := runtime.Connect("routing-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	var attached coreprotocol.AttachResult
	if err := json.Unmarshal(callCore(t, client, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()}).Payload, &attached); err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := json.Unmarshal(callCore(t, client, "create", attached.WorkspaceID, coreprotocol.NewSession, struct{}{}).Payload, &created); err != nil {
		t.Fatal(err)
	}
	callCore(t, client, "first", attached.WorkspaceID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Work until I correct you"})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first turn did not reach the provider")
	}
	note := coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Exact correction", DraftRevision: 9}
	first := callCore(t, client, "correction", attached.WorkspaceID, coreprotocol.SubmitInput, note)
	var receipt coreprotocol.InputResult
	if err := json.Unmarshal(first.Payload, &receipt); err != nil || receipt.Status != workspace.InputAdmissionSteered ||
		receipt.Receipt.EventID == "" || receipt.Receipt.RunID == "" || receipt.Receipt.OwnerEpoch == 0 || receipt.Receipt.DraftRevision != 9 {
		t.Fatalf("steering had no exact durable receipt: %+v err=%v", receipt, err)
	}
	replay := callCore(t, client, "correction", attached.WorkspaceID, coreprotocol.SubmitInput, note)
	if string(first.Payload) != string(replay.Payload) {
		t.Fatal("replayed input changed its acceptance route or identity")
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: created.OrchID, Kind: collaboration.EventHumanSteering, Limit: 20})
	if err != nil || len(page.Events) != 1 || page.Events[0].ID != receipt.Receipt.EventID || page.Events[0].RunID != receipt.Receipt.RunID {
		t.Fatalf("one Enter published another correction: %+v err=%v", page.Events, err)
	}
}
