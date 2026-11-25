package workspace

import (
	"path/filepath"
	"testing"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/internal/providersim"
	"crude/types/setting"
)

func TestWorkspaceSwitchLauncherStartsAndResumesSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		return providersim.Text("Saved workspace conversation.")
	})

	rootA, rootB := t.TempDir(), t.TempDir()
	cfg := historyE2EConfig(server.BaseURL(providersim.Chat))
	ws := NewWorkspace(rootA, "history-model", "tester", setting.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetBackendConfig(cfg, home)
	ws.SetAppDefaults(setting.App{
		Accounts:     ws.AllAccounts(),
		LocalHistory: setting.LocalHistory{Enabled: "off"},
	})
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 1024)}
	ws.SetEventSender(sender)

	sessionIDs := make(map[string]string)
	var lastOpened *Session
	start := func(root string, resume bool) {
		t.Helper()
		callsBeforeStartup := len(server.Requests())
		var result *NewSessionResult
		if resume {
			result = ws.OpenAgentSession(sessionIDs[root])
		} else {
			result = ws.NewAgentSession()
		}
		if result.Error != "" || result.OrchID == "" {
			t.Fatalf("launcher startup in %q (resume=%t): %+v", root, resume, result)
		}
		frontend := ws.SessionsByID[result.SessionID]
		lastOpened = frontend
		if frontend == nil || frontend.backend == nil || frontend.AgentID != result.OrchID {
			t.Fatal("session opening did not return the exact registered owner")
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
			t.Fatalf("opened session database is not usable: %v", err)
		}
		if len(server.Requests()) != callsBeforeStartup {
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
		oldSession := lastOpened
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
		if ws.SessionsByID[oldSession.SessionID] != nil && oldSession.SessionID != lastOpened.SessionID {
			t.Fatal("previous workspace's session remained in the live session index")
		}
	}
	if calls := len(server.Requests()); calls != 2 {
		t.Fatalf("provider calls = %d, want only the two explicitly submitted turns", calls)
	}
}
