package workspace

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"crude/types"
)

func TestSessionForAgentReturnsExactSettingsOwner(t *testing.T) {
	one := &Session{
		SessionID: "one", AgentID: "agent-one",
		Settings: NewSessionSettingsState(types.Settings{Model: "model-one"}, types.Settings{}, nil),
	}
	two := &Session{
		SessionID: "two", AgentID: "agent-two",
		Settings: NewSessionSettingsState(types.Settings{Model: "model-two"}, types.Settings{}, nil),
	}
	ws := &Workspace{
		Sessions:        []*Session{one, two},
		SessionsByAgent: map[string]*Session{"agent-one": one, "agent-two": two},
		AgentMgr:        NewAgentManager(),
	}
	ws.AgentMgr.AddOrchestrator("agent-one", &AgentState{})
	ws.AgentMgr.AddOrchestrator("agent-two", &AgentState{})
	ws.AgentMgr.AddSubAgent("child-one", "agent-one", "Child", "model", "crafter", &AgentState{})
	ws.AgentMgr.AddSubAgent("child-two", "agent-two", "Child", "model", "crafter", &AgentState{})
	for _, check := range []struct {
		id    string
		owner *Session
	}{{"agent-two", two}, {"child-one", one}, {"agent-one", one}, {"child-two", two}, {"unknown", nil}, {"", nil}} {
		if got := ws.SessionForAgent(check.id); got != check.owner {
			t.Fatalf("SessionForAgent(%q) = %p, want %p", check.id, got, check.owner)
		}
	}
	if one.Settings.Effective.Model != "model-one" || two.Settings.Effective.Model != "model-two" {
		t.Fatal("owner inspection changed unrelated settings")
	}
}

func TestOpenAgentSessionRetainsRuntimeWithoutImplicitOwner(t *testing.T) {
	setTestHome(t)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ws := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	ws.SetBackendConfig(historyE2EConfig(server.URL), t.TempDir())
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	ws.SetEventSender(sender)
	first := ws.NewAgentSession()
	if first.Error != "" || first.SessionID == "" || first.OrchID == "" {
		t.Fatalf("first session: %+v", first)
	}
	owner := ws.SessionsByID[first.SessionID]
	backend := ws.BackendSession(first.SessionID)
	second := ws.NewAgentSession()
	if second.Error != "" || second.SessionID == first.SessionID || second.OrchID == first.OrchID {
		t.Fatalf("distinct session: first=%+v second=%+v", first, second)
	}
	other := ws.SessionsByID[second.SessionID]
	otherBackend := ws.BackendSession(second.SessionID)
	if owner == nil || backend == nil || other == nil || otherBackend == nil || backend == otherBackend {
		t.Fatal("new session did not retain distinct exact owners")
	}
	for range 2 {
		opened := ws.OpenAgentSession(first.SessionID)
		if opened.Error != "" || opened.SessionID != first.SessionID || opened.OrchID != first.OrchID ||
			ws.SessionsByID[first.SessionID] != owner || ws.BackendSession(first.SessionID) != backend ||
			ws.SessionForAgent(first.OrchID) != owner || ws.SessionForAgent(second.OrchID) != other ||
			ws.BackendSession(second.SessionID) != otherBackend || len(ws.Sessions) != 2 {
			t.Fatalf("opening drifted runtime ownership: %+v", opened)
		}
	}
	for _, missing := range []string{"", "missing-session"} {
		if ws.BackendSession(missing) != nil {
			t.Fatalf("backend lookup for %q borrowed another owner", missing)
		}
		if runtime, err := ws.StartOrchestrator(missing, sender); err == nil || runtime != nil {
			t.Fatalf("starting missing owner %q returned %v, %v", missing, runtime, err)
		}
	}
	if failed := ws.OpenAgentSession("missing-session"); failed.Error == "" {
		t.Fatal("opening missing owner succeeded")
	}
	if len(ws.Sessions) != 2 || ws.SessionForAgent(first.OrchID) != owner || ws.SessionForAgent(second.OrchID) != other {
		t.Fatal("failed exact-owner operation changed existing sessions")
	}
	if calls.Load() != 0 {
		t.Fatal("session creation/opening replayed provider work")
	}
}
