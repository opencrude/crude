package viewmodel

import (
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/providersim"
	"crude/types/conversation"
	"crude/types/recovery"
	"crude/types/statuscenter"
)

// Exercise the real supervisor, persistence, core publisher, wire installer and
// mounted chats. Recovery settlement precedes the retained terminal done event;
// neither shell may append its content again or require rehydration to fix it.
func TestRemoteRecoveredStreamCompletionMatchesColdResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	gate := providersim.NewGate()
	chunks := strings.SplitAfter(streamParityAnswer, "\n")
	server := providersim.New(t)
	server.Respond(func(providersim.Request) providersim.Turn {
		if requests.Add(1) == 1 {
			return providersim.HTTPError(providersim.Overload).WithFaultMessage("temporary provider outage")
		}
		// Hold the finish event after the role and all text events were sent.
		return providersim.Turn{}.WithTextDeltas(chunks...).WithoutUsage().WithGateAfter(1+len(chunks), gate)
	})
	configure := func(cfg *session.Config) {
		account := cfg.Accounts["test"]
		account.Connection.Dialer = &provider.DialSettings{MaxAttempts: 1}
		cfg.Accounts["test"] = account
	}
	runtime := remoteJourneyRuntimeConfig(t, server.BaseURL(providersim.Chat), configure)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(gate.Release)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool { return two.app.AgentView.ActiveID() == id })
	for _, shell := range shells {
		shell.app.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
		shell.app.Tree.Visible = false
	}
	one.app.Input.SetInput("Report the exact streaming result.")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, shells, func() bool {
		for _, shell := range shells {
			state := shell.app.ws.AgentMgr().Get(id).State
			if !state.Streaming {
				return false
			}
			found := false
			for _, row := range state.Messages.Messages() {
				found = found || row.Role == conversation.RoleAssistant && row.Content == streamParityAnswer
			}
			if !found {
				return false
			}
		}
		return true
	})
	gate.Release()
	remoteJourneyUntil(t, shells, func() bool {
		return one.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted &&
			two.app.ws.AgentActivity(id).Status.Phase == statuscenter.AgentCompleted
	})
	check := func(app *App, agentID string) {
		t.Helper()
		state := app.ws.AgentMgr().Get(agentID).State
		var answers []string
		for _, row := range state.Messages.Messages() {
			if row.Role == conversation.RoleAssistant {
				answers = append(answers, row.Content)
			}
		}
		if len(answers) != 1 || answers[0] != streamParityAnswer {
			t.Fatalf("live/cold response was duplicated or lost: %q", answers)
		}
		app.forceFrame()
		app.View()
		assertStreamParityRows(t, app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW), streamParityAnswer)
		if strings.Count(chatAreaText(app), "What changed") != 1 || strings.Count(chatAreaText(app), "ECHO") != 1 {
			t.Fatalf("mounted chat repeats or drops the response: %q", chatAreaText(app))
		}
	}
	for _, shell := range shells {
		check(shell.app, id)
		if status := shell.app.ws.AgentMgr().Get(id).State.RecoveryStatus(); status.State != recovery.Recovered || status.Attempt == 0 {
			t.Fatalf("journey did not settle real recovery: %+v", status)
		}
	}
	sessionID := one.app.ActiveSession.SessionID
	for _, shell := range shells {
		shell.app.StopAsyncIO()
		if err := shell.app.CloseWorkspaceAttachment(); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	runtime = remoteJourneyRuntimeConfig(t, server.BaseURL(providersim.Chat), configure)
	resumed := newRemoteJourneyShell(t, runtime, root)
	runAppCommand(t, resumed.app, resumed.app.launcherResumeSession(sessionID, nil))
	remoteJourneyUntil(t, []remoteJourneyShell{resumed}, func() bool { return resumed.app.AgentView.ActiveID() != "" })
	resumed.app.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	resumed.app.Tree.Visible = false
	check(resumed.app, resumed.app.AgentView.ActiveID())
	if requests.Load() != 2 {
		t.Fatalf("completion/resume replayed provider work: %d requests", requests.Load())
	}
}
