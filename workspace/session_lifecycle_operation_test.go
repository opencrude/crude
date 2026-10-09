package workspace

import (
	"context"
	"crude/types"
	"testing"
)

func TestSessionLifecycleWorkerDoesNotPublishCollections(t *testing.T) {
	setTestHome(t)
	ws := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig("http://127.0.0.1:1")
	ws.SetBackendConfig(cfg, t.TempDir())
	ws.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	op, err := ws.PrepareAgentSession("")
	if err != nil {
		t.Fatal(err)
	}
	// Mutating configuration after preparation cannot affect the backend work.
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	category := profile.Categories["default"]
	category.Model = "changed-after-prepare"
	profile.Categories["default"] = category
	result := op.Run(context.Background())
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(ws.Sessions) != 0 || len(ws.SessionsByID) != 0 || ws.AgentMgr.Count() != 0 {
		t.Fatal("worker published workspace state")
	}
	if result.owner.Settings.Effective.Model == "changed-after-prepare" {
		t.Fatal("worker borrowed mutable config")
	}
	first := ws.ApplyAgentSession(result)
	if first.Error != "" {
		t.Fatal(first.Error)
	}
	second := ws.ApplyAgentSession(op.Run(context.Background()))
	if second.Error != "" || second.OrchID != first.OrchID || len(ws.Sessions) != 1 {
		t.Fatal("repeat application duplicated backend")
	}
	op, err = ws.PrepareAgentSession(first.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	warm := ws.ApplyAgentSession(op.Run(context.Background()))
	if warm.Error != "" || warm.OrchID != first.OrchID {
		t.Fatalf("warm open: %+v", warm)
	}
}

func TestSessionLifecycleCancellationDoesNotInstall(t *testing.T) {
	setTestHome(t)
	ws := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(ws.Close)
	ws.SetBackendConfig(historyE2EConfig("http://127.0.0.1:1"), t.TempDir())
	ws.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	op, err := ws.PrepareAgentSession("")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := ws.ApplyAgentSession(op.Run(ctx))
	if result.Error == "" || len(ws.Sessions) != 0 {
		t.Fatal("cancelled lifecycle installed")
	}
}

func TestLifecycleEventGateInstallsEarlyEvidence(t *testing.T) {
	ws := &Workspace{Logger: nopLogger{}, AgentMgr: NewAgentManager(), PaneMgr: NewPaneManager(), SessionsByAgent: map[string]*Session{}}
	node := ws.RegisterAgentRoot("root")
	gate := &lifecycleEventGate{sender: &historyE2ESender{events: make(chan historyE2EEvent, 10)}}
	gate.SendAgentEventWithID("stream_token", "early retained output", "root")
	if len(node.State.Messages.Messages()) != 0 {
		t.Fatal("early evidence mutated inventory before application")
	}
	gate.activate(ws)
	if len(node.State.Messages.Messages()) == 0 {
		t.Fatal("startup evidence dropped")
	}
}
