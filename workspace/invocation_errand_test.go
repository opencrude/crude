package workspace

import (
	"testing"

	"crude/agent/collaboration"
	"crude/agent/session"
	"crude/types"
)

// A session opened as Errand keeps that role after creation, and creating it
// changes neither the shared defaults nor another session.
func TestNewAgentSessionAsRootsSessionInTheRole(t *testing.T) {
	setTestHome(t)
	w := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig("http://127.0.0.1:1")
	prof := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	if _, ok := prof.Categories[session.CategoryErrand]; !ok {
		prof.Categories[session.CategoryErrand] = session.CategoryConfig{Inherit: session.InheritMain}
		cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = prof
	}
	w.SetBackendConfig(cfg, t.TempDir())
	w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})

	created := w.NewAgentSessionAs(session.CategoryErrand)
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	owner := w.SessionsByID[created.SessionID]
	if owner == nil || !SessionIsErrand(owner) {
		t.Fatalf("session is not rooted in Errand: %+v", owner)
	}
	if got := w.BackendSession(created.SessionID).SettingsSnapshot().SessionMainAgent; got != session.CategoryErrand {
		t.Fatalf("backend role = %q, want %q", got, session.CategoryErrand)
	}
	plain := w.NewAgentSession()
	if plain.Error != "" {
		t.Fatal(plain.Error)
	}
	if SessionIsErrand(w.SessionsByID[plain.SessionID]) {
		t.Fatal("an ordinary session became Errand")
	}
	if bad := w.NewAgentSessionAs("no-such-role"); bad.Error == "" {
		t.Fatal("an unknown role was accepted")
	}
}

// The workspace keeps exactly one Errand, and reopening the workspace resumes
// that same saved agent instead of creating another one.
func TestEnsureErrandSessionIsOneAndReopens(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	makeWorkspace := func() *Workspace {
		w := NewWorkspace(root, "history-model", "tester", types.Settings{}, nopLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		cfg := historyE2EConfig("http://127.0.0.1:1")
		prof := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
		if _, ok := prof.Categories[session.CategoryErrand]; !ok {
			prof.Categories[session.CategoryErrand] = session.CategoryConfig{Inherit: session.InheritMain}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = prof
		}
		w.SetBackendConfig(cfg, t.TempDir())
		w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
		return w
	}

	w := makeWorkspace()
	first := w.EnsureErrandSession()
	if first.Error != "" {
		t.Fatal(first.Error)
	}
	if !SessionIsErrand(w.SessionsByID[first.SessionID]) {
		t.Fatalf("ensure did not root the session in Errand: %+v", w.SessionsByID[first.SessionID])
	}
	if !w.YoloForAgent(first.OrchID) {
		t.Fatal("the workspace Errand did not start in automatic approval mode")
	}
	if got := w.WorkspaceDB.GetSetting(workspaceErrandSessionKey); got != first.SessionID {
		t.Fatalf("remembered Errand = %q, want %q", got, first.SessionID)
	}
	// Other agents find Errand with agent_list query "errand": the registry
	// lists the root under its role, not as an ordinary default agent.
	if w.Collaboration == nil {
		t.Fatal("test workspace has no collaboration registry")
	}
	{
		agents, err := w.Collaboration.ListAgents(t.Context(), collaboration.AgentQuery{Query: "errand"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, listed := range agents {
			if listed.ID == first.OrchID {
				found = listed.Category == session.CategoryErrand
			}
		}
		if !found {
			t.Fatalf("workspace Errand %q is not listed with category %q: %+v", first.OrchID, session.CategoryErrand, agents)
		}
	}
	second := w.EnsureErrandSession()
	if second.Error != "" || second.SessionID != first.SessionID || second.OrchID != first.OrchID {
		t.Fatalf("second ensure created another agent: first=%+v second=%+v", first, second)
	}
	if got := w.BackendSession(first.SessionID).SettingsSnapshot().SessionMainAgent; got != session.CategoryErrand {
		t.Fatalf("saved role = %q, want %q", got, session.CategoryErrand)
	}
	// An idle workspace agent is still a saved identity. Do not add a fake
	// transcript row to keep it alive: it must survive before its first task.
	w.SetYoloForAgent(first.OrchID, false)
	if again := w.EnsureErrandSession(); again.Error != "" || w.YoloForAgent(first.OrchID) {
		t.Fatal("ensuring a live Errand replaced the user's permission mode")
	}
	w.CloseAllSessions()
	w.Close()

	w = makeWorkspace()
	t.Cleanup(w.Close)
	resumed := w.EnsureErrandSession()
	if resumed.Error != "" {
		t.Fatal(resumed.Error)
	}
	if resumed.SessionID != first.SessionID {
		t.Fatalf("reopen created %q, want saved Errand %q", resumed.SessionID, first.SessionID)
	}
	if !SessionIsErrand(w.SessionsByID[resumed.SessionID]) {
		t.Fatal("reopened session lost its Errand role")
	}
	if w.YoloForAgent(resumed.OrchID) {
		t.Fatal("reopening Errand replaced the saved permission mode")
	}
	if other := w.EnsureErrandSession(); other.SessionID != resumed.SessionID {
		t.Fatalf("reopened workspace made a second Errand: %+v", other)
	}
}

func TestInvocationSelectionSelectsOnlyMainAgent(t *testing.T) {
	if !(InvocationSelection{MainAgent: "errand"}).SelectsOnlyMainAgent("errand") {
		t.Fatal("the bare role selection was not recognized")
	}
	for _, selection := range []InvocationSelection{
		{}, {MainAgent: "other"}, {MainAgent: "errand", Model: "model"},
		{MainAgent: "errand", AccountID: "account"}, {MainAgent: "errand", Instructions: "policy"},
	} {
		if selection.SelectsOnlyMainAgent("errand") {
			t.Fatalf("accepted an overridden workspace-agent selection: %+v", selection)
		}
	}
}
