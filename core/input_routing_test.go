package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/agent/session"
	"crude/core/coreprotocol"
	"crude/internal/providersim"
	"crude/types/setting"
	"crude/workspace"
)

func TestSubmitInputSteeringReceiptReplaysWithoutPublishingAnotherInput(t *testing.T) {
	home := t.TempDir()
	setCoreTestHome(t, home)
	gate := providersim.NewGate()
	server := providersim.New(t)
	// Chat flushes an assistant-role event, then the first text delta.
	server.Script(providersim.Text("accepted work").WithGateAfter(2, gate))
	var store *collaboration.Store
	runtime, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "routing-model", "tester", setting.Settings{}, coreTestLogger{})
		ws.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}})
		agents := session.DefaultAgentsConfig()
		profile := agents.Profiles["default"]
		off := false
		profile.MultiAgent = &off
		profile.Categories[session.CategoryDefault] = session.CategoryConfig{AccountID: "test", Model: "routing-model"}
		agents.Profiles["default"] = profile
		ws.SetBackendConfig(&session.Config{Agents: agents, Accounts: map[string]provider.Account{"test": {
			ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "Test", ProviderURL: server.BaseURL(providersim.Chat), APIStandard: "openai", AuthType: provider.AuthProtocol},
		}}}, home)
		store = ws.Collaboration
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	t.Cleanup(gate.Release)
	client, err := runtime.Connect("routing-client")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	var attached coreprotocol.AttachResult
	if err := coreprotocol.Decode(callCore(t, client, "attach", "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}).Payload, &attached); err != nil {
		t.Fatal(err)
	}
	var created workspace.NewSessionResult
	if err := coreprotocol.Decode(callCore(t, client, "create", attached.WorkspaceID, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{}).Payload, &created); err != nil {
		t.Fatal(err)
	}
	callCore(t, client, "first", attached.WorkspaceID, coreprotocol.SubmitInput, coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Work until I correct you"})
	providerWait, cancelProviderWait := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelProviderWait()
	if err := gate.Wait(providerWait); err != nil {
		t.Fatal("first turn did not reach the provider", err)
	}
	note := coreprotocol.InputRequest{AgentID: created.OrchID, Text: "Exact correction", DraftRevision: 9}
	first := callCore(t, client, "correction", attached.WorkspaceID, coreprotocol.SubmitInput, note)
	var receipt coreprotocol.InputResult
	if err := coreprotocol.Decode(first.Payload, &receipt); err != nil || receipt.Status != workspace.InputAdmissionSteered ||
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
