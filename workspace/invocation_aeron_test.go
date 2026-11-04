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
