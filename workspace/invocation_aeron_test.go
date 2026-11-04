package workspace

import (
	"testing"

	"crude/session"
	"crude/types"
)

// A session opened as Aeron keeps that role after creation, and creating it
// changes neither the shared defaults nor another session.
func TestNewAgentSessionAsRootsSessionInTheRole(t *testing.T) {
	setTestHome(t)
	w := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, nopLogger{})
	t.Cleanup(w.Close)
	w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	cfg := historyE2EConfig("http://127.0.0.1:1")
	prof := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	if _, ok := prof.Categories[session.CategoryAeron]; !ok {
		prof.Categories[session.CategoryAeron] = session.CategoryConfig{Inherit: session.InheritMain}
		cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = prof
	}
	w.SetBackendConfig(cfg, t.TempDir())
	w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})

	created := w.NewAgentSessionAs(session.CategoryAeron)
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	owner := w.SessionsByID[created.SessionID]
	if owner == nil || !SessionIsAeron(owner) {
		t.Fatalf("session is not rooted in Aeron: %+v", owner)
	}
	if got := w.BackendSession(created.SessionID).SettingsSnapshot().SessionMainAgent; got != session.CategoryAeron {
		t.Fatalf("backend role = %q, want %q", got, session.CategoryAeron)
	}
	plain := w.NewAgentSession()
	if plain.Error != "" {
		t.Fatal(plain.Error)
	}
	if SessionIsAeron(w.SessionsByID[plain.SessionID]) {
		t.Fatal("an ordinary session became Aeron")
	}
	if bad := w.NewAgentSessionAs("no-such-role"); bad.Error == "" {
		t.Fatal("an unknown role was accepted")
	}
}

// The workspace keeps exactly one Aeron, and reopening the workspace resumes
// that same saved agent instead of creating another one.
func TestEnsureAeronSessionIsOneAndReopens(t *testing.T) {
	setTestHome(t)
	root := t.TempDir()
	makeWorkspace := func() *Workspace {
		w := NewWorkspace(root, "history-model", "tester", types.Settings{}, nopLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		cfg := historyE2EConfig("http://127.0.0.1:1")
		prof := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
		if _, ok := prof.Categories[session.CategoryAeron]; !ok {
			prof.Categories[session.CategoryAeron] = session.CategoryConfig{Inherit: session.InheritMain}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = prof
		}
		w.SetBackendConfig(cfg, t.TempDir())
		w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
		return w
	}

	w := makeWorkspace()
	first := w.EnsureAeronSession()
	if first.Error != "" {
		t.Fatal(first.Error)
	}
	if !SessionIsAeron(w.SessionsByID[first.SessionID]) {
		t.Fatalf("ensure did not root the session in Aeron: %+v", w.SessionsByID[first.SessionID])
	}
	if !w.YoloForAgent(first.OrchID) {
		t.Fatal("the workspace Aeron did not start in automatic approval mode")
	}
	if got := w.WorkspaceDB.GetSetting(workspaceAeronSessionKey); got != first.SessionID {
		t.Fatalf("remembered Aeron = %q, want %q", got, first.SessionID)
	}
	second := w.EnsureAeronSession()
	if second.Error != "" || second.SessionID != first.SessionID || second.OrchID != first.OrchID {
		t.Fatalf("second ensure created another agent: first=%+v second=%+v", first, second)
	}
	if got := w.BackendSession(first.SessionID).SettingsSnapshot().SessionMainAgent; got != session.CategoryAeron {
		t.Fatalf("saved role = %q, want %q", got, session.CategoryAeron)
	}
	// An idle workspace agent is still a saved identity. Do not add a fake
	// transcript row to keep it alive: it must survive before its first task.
	w.SetYoloForAgent(first.OrchID, false)
	if again := w.EnsureAeronSession(); again.Error != "" || w.YoloForAgent(first.OrchID) {
		t.Fatal("ensuring a live Aeron replaced the user's permission mode")
	}
	w.CloseAllSessions()
	w.Close()

	w = makeWorkspace()
	t.Cleanup(w.Close)
	resumed := w.EnsureAeronSession()
	if resumed.Error != "" {
		t.Fatal(resumed.Error)
	}
	if resumed.SessionID != first.SessionID {
		t.Fatalf("reopen created %q, want saved Aeron %q", resumed.SessionID, first.SessionID)
	}
	if !SessionIsAeron(w.SessionsByID[resumed.SessionID]) {
		t.Fatal("reopened session lost its Aeron role")
	}
	if w.YoloForAgent(resumed.OrchID) {
		t.Fatal("reopening Aeron replaced the saved permission mode")
	}
	if other := w.EnsureAeronSession(); other.SessionID != resumed.SessionID {
		t.Fatalf("reopened workspace made a second Aeron: %+v", other)
	}
}

func TestInvocationSelectionSelectsOnlyMainAgent(t *testing.T) {
	if !(InvocationSelection{MainAgent: "aeron"}).SelectsOnlyMainAgent("aeron") {
		t.Fatal("the bare role selection was not recognized")
	}
	for _, selection := range []InvocationSelection{
		{}, {MainAgent: "other"}, {MainAgent: "aeron", Model: "model"},
		{MainAgent: "aeron", AccountID: "account"}, {MainAgent: "aeron", Instructions: "policy"},
	} {
		if selection.SelectsOnlyMainAgent("aeron") {
			t.Fatalf("accepted an overridden workspace-agent selection: %+v", selection)
		}
	}
}
