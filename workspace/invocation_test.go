package workspace

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"crude/provider"
	"crude/session"
	"crude/types"
)

func TestInvocationConfigCapturesAndIsolatesCredentials(t *testing.T) {
	base := historyE2EConfig("http://127.0.0.1:1")
	main := base.GetActiveDefaultCategory()
	account := base.Accounts[main.AccountID]
	base.AccountManager = provider.NewAccountManager(base.Accounts, nil)
	selection := InvocationSelection{AccountID: account.ID, ProviderID: account.ProviderID, Model: "invocation-model", APIKey: "write-only-secret", Instructions: "additional instructions"}
	cfg, err := invocationConfig(base, selection)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.GetActiveDefaultCategory()
	if got.Model != "invocation-model" || !strings.Contains(got.Instructions, selection.Instructions) || got.AccountID == main.AccountID {
		t.Fatalf("selection not isolated: %+v", got)
	}
	bound, ok := cfg.AccountManager.Account(got.AccountID)
	if !ok || bound.APIKey != selection.APIKey {
		t.Fatal("saved manager overrode ephemeral key")
	}
	if base.GetActiveDefaultCategory().Model != main.Model {
		t.Fatal("changed shared profile")
	}
	if _, ok := base.AccountManager.Account(got.AccountID); ok {
		t.Fatal("published ephemeral account globally")
	}
	snapshot, _ := json.Marshal(session.ConfigToSettings(cfg))
	if strings.Contains(string(snapshot), selection.APIKey) {
		t.Fatal("key entered settings read model")
	}
	for _, input := range []InvocationSelection{{APIKey: "secret"}, {AccountID: "missing"}, {AccountID: account.ID, ProviderID: "wrong"}, {AccountID: "new", APIKey: "secret", ProviderID: "unknown"}} {
		if _, err := invocationConfig(base, input); err == nil {
			t.Fatalf("invalid selection accepted: %+v", input)
		}
	}
	fresh, err := invocationConfig(base, InvocationSelection{AccountID: "new", APIKey: "secret", ProviderID: "openai", Model: "model"})
	if err != nil || fresh.GetActiveDefaultCategory().AccountID == "new" {
		t.Fatalf("new ephemeral account: %v", err)
	}
}

func TestInvocationLifecycleSelectionAndLiveRefusal(t *testing.T) {
	setTestHome(t)
	w := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	w.SetBackendConfig(historyE2EConfig("http://127.0.0.1:1"), t.TempDir())
	w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	op, err := w.PrepareInvocationSession("", InvocationSelection{Model: "chosen-model", Instructions: "extra"})
	if err != nil {
		t.Fatal(err)
	}
	result := op.Run(t.Context())
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	created := w.ApplyAgentSession(result)
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	if w.BackendSession(created.SessionID).ModelName() != "chosen-model" {
		t.Fatal("new session ignored selector")
	}
	if w.sessionCfg.GetActiveDefaultCategory().Model == "chosen-model" {
		t.Fatal("invocation changed peer defaults")
	}
	if _, err := w.PrepareInvocationSession(created.SessionID, InvocationSelection{Model: "other"}); err == nil {
		t.Fatal("mutated already-live session")
	}
	warm, err := w.PrepareInvocationSession(created.SessionID, InvocationSelection{})
	if err != nil {
		t.Fatal(err)
	}
	opened := w.ApplyAgentSession(warm.Run(t.Context()))
	if opened.OrchID != created.OrchID || opened.Error != "" {
		t.Fatalf("warm reuse: %+v", opened)
	}
}

func TestInvocationColdResumeSelection(t *testing.T) {
	setTestHome(t)
	root, home := t.TempDir(), os.Getenv("HOME")
	makeWorkspace := func() *Workspace {
		w := NewWorkspace(root, "history-model", "tester", types.Settings{}, nopLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		w.SetBackendConfig(historyE2EConfig("http://127.0.0.1:1"), home)
		w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
		return w
	}
	w := makeWorkspace()
	op, err := w.PrepareInvocationSession("", InvocationSelection{})
	if err != nil {
		t.Fatal(err)
	}
	created := w.ApplyAgentSession(op.Run(t.Context()))
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	if err := w.BackendSession(created.SessionID).AppendWorkspaceHistoryEvent("retained fixture history", "retained fixture history"); err != nil {
		t.Fatal(err)
	}
	w.CloseAllSessions()
	w.Close()
	w = makeWorkspace()
	t.Cleanup(w.Close)
	op, err = w.PrepareInvocationSession(created.SessionID, InvocationSelection{Model: "resumed-choice", Instructions: "cold resume extra policy"})
	if err != nil {
		t.Fatal(err)
	}
	result := op.Run(t.Context())
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	opened := w.ApplyAgentSession(result)
	if opened.Error != "" || w.BackendSession(created.SessionID).ModelName() != "resumed-choice" {
		t.Fatalf("cold selector ignored: %+v", opened)
	}
	if again := op.Run(t.Context()); again.agentID != result.agentID {
		t.Fatal("duplicate lifecycle owner")
	}
	if got := w.BackendSession(created.SessionID).SettingsSnapshot().Agents.Categories[session.CategoryDefault].Instructions; !strings.Contains(got, "cold resume extra policy") {
		t.Fatalf("cold resume lost additive instructions: %q", got)
	}
}

func TestInvocationColdResumeValidatesRetainedProvider(t *testing.T) {
	setTestHome(t)
	root, home := t.TempDir(), os.Getenv("HOME")
	cfg := historyE2EConfig("http://127.0.0.1:1")
	main := cfg.GetActiveDefaultCategory()
	other := cfg.Accounts[main.AccountID]
	other.ID, other.ProviderID = "retained-account", "retained-provider"
	cfg.Accounts[other.ID] = other
	makeWorkspace := func() *Workspace {
		w := NewWorkspace(root, "history-model", "tester", types.Settings{}, nopLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		w.SetBackendConfig(cfg, home)
		w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
		return w
	}
	w := makeWorkspace()
	op, err := w.PrepareInvocationSession("", InvocationSelection{AccountID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	created := w.ApplyAgentSession(op.Run(t.Context()))
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	if err := w.BackendSession(created.SessionID).AppendWorkspaceHistoryEvent("provider retention fixture", "provider retention fixture"); err != nil {
		t.Fatal(err)
	}
	w.CloseAllSessions()
	w.Close()
	w = makeWorkspace()
	t.Cleanup(w.Close)
	op, err = w.PrepareInvocationSession(created.SessionID, InvocationSelection{ProviderID: cfg.Accounts[main.AccountID].ProviderID})
	if err != nil {
		t.Fatal(err)
	}
	if result := op.Run(t.Context()); result.Err == nil || !strings.Contains(result.Err.Error(), "does not match --provider") {
		t.Fatalf("workspace provider accepted over retained account: %v", result.Err)
	}
	op, err = w.PrepareInvocationSession(created.SessionID, InvocationSelection{ProviderID: other.ProviderID})
	if err != nil {
		t.Fatal(err)
	}
	if result := op.Run(t.Context()); result.Err != nil {
		t.Fatal(result.Err)
	} else {
		result.owner.backend.Close()
	}
}
