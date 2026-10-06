package workspace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"crude/collaboration"
	"crude/provider"
	ttypes "crude/types"
)

func TestWorkspaceSwitchLauncherStartsAndResumesSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Saved workspace conversation.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	rootA, rootB := t.TempDir(), t.TempDir()
	cfg := historyE2EConfig(server.URL)
	ws := NewWorkspace(rootA, "history-model", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetBackendConfig(cfg, home)
	ws.SetAppDefaults(ttypes.AppSettings{
		Accounts:     ws.AllAccounts(),
		LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"},
	})
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	ws.SetEventSender(sender)

	sessionIDs := make(map[string]string)
	start := func(root string, resume bool) {
		t.Helper()
		callsBeforeStartup := providerCalls.Load()
		var result *NewSessionResult
		if resume {
			result = ws.ResumeSessionFromLauncher(sessionIDs[root])
		} else {
			result = ws.NewSessionFromLauncher()
		}
		if result.Error != "" || result.OrchID == "" {
			t.Fatalf("launcher startup in %q (resume=%t): %+v", root, resume, result)
		}
		frontend := ws.ActiveSession
		if frontend == nil || frontend.backend == nil || frontend.AgentID != result.OrchID {
			t.Fatal("launcher did not activate the new session's registered agent")
		}
		if resume && frontend.SessionID != sessionIDs[root] {
			t.Fatal("resume changed the saved session identity")
		}
		sessionIDs[root] = frontend.SessionID
		if frontend.backend.WorkDir() != root ||
			frontend.SessionDir != filepath.Join(ws.WorkspaceDir, "sessions", frontend.SessionID) {
			t.Fatal("session is not rooted in the selected workspace")
		}
		if frontend.backend.CollaborationStore() != ws.Collaboration ||
			frontend.backend.MemoryStore() != ws.WorkspaceDB.Memory {
			t.Fatal("session retained another workspace's persistence services")
		}
		registered, err := ws.Collaboration.Agent(t.Context(), result.OrchID)
		if err != nil {
			t.Fatalf("inspect launcher agent: %v", err)
		}
		if registered.SessionID != frontend.SessionID ||
			ws.Collaboration.WorkspaceID() != canonicalizePath(root) ||
			registered.Presence != collaboration.RuntimeAttached {
			t.Fatalf("agent registered to the wrong workspace or lifetime: %+v", registered)
		}
		if err := frontend.DB.Ping(); err != nil {
			t.Fatalf("active session database is not usable: %v", err)
		}
		if providerCalls.Load() != callsBeforeStartup {
			t.Fatal("passive startup called the provider")
		}
		prompt := "Keep this conversation in " + root
		if !resume {
			// Empty sessions are deliberately pruned on close. Save a real turn
			// through the backend before testing passive resume and persistence.
			if !frontend.backend.SubmitInput(result.OrchID, provider.PlainInput(prompt)) {
				t.Fatal("new session could not accept its first turn")
			}
			waitHistoryAgentDone(t, sender, result.OrchID)
		}
		var savedPrompt, savedAnswer bool
		for _, message := range frontend.backend.MessagesWithMeta() {
			savedPrompt = savedPrompt || message.Msg.Role == "user" && message.Msg.ContentString() == prompt
			savedAnswer = savedAnswer || message.Msg.Role == "assistant" && message.Msg.ContentString() == "Saved workspace conversation."
		}
		if !savedPrompt || !savedAnswer {
			t.Fatal("saved conversation was lost or mixed with another workspace")
		}
	}

	start(rootA, false)
	for index, root := range []string{rootB, rootA, rootB} {
		oldDB, oldStore := ws.WorkspaceDB, ws.Collaboration
		oldSession := ws.ActiveSession
		if err := ws.SwitchProject(root); err != nil {
			t.Fatal(err)
		}
		// Go through the same launcher action that failed in the product, before
		// checking implementation details, so the regression fails for that error.
		start(root, index > 0)
		if ws.Collaboration == oldStore || ws.Collaboration.WorkspaceID() != canonicalizePath(root) {
			t.Fatal("workspace switch retained the previous collaboration service")
		}
		if err := oldDB.DB.Ping(); err == nil {
			t.Fatal("workspace switch left the previous workspace database open")
		}
		if err := oldSession.DB.Ping(); err == nil {
			t.Fatal("workspace switch left the previous session database open")
		}
		if ws.SessionsByID[oldSession.SessionID] != nil && oldSession.SessionID != ws.ActiveSession.SessionID {
			t.Fatal("previous workspace's session remained in the live session index")
		}
	}
	if calls := providerCalls.Load(); calls != 2 {
		t.Fatalf("provider calls = %d, want only the two explicitly submitted turns", calls)
	}
}
