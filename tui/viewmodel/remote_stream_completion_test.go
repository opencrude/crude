package viewmodel

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/types/recovery"
)

// Exercise the real supervisor, persistence, core publisher, wire installer and
// mounted chats. Recovery settlement precedes the retained terminal done event;
// neither shell may append its content again or require rehydration to fix it.
func TestRemoteRecoveredStreamCompletionMatchesColdResume(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var requests atomic.Int32
	finish := make(chan struct{})
	var release sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprint(w, `{"error":{"message":"temporary provider outage"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, token := range strings.SplitAfter(streamParityAnswer, "\n") {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", token)
			w.(http.Flusher).Flush()
		}
		select {
		case <-finish:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	configure := func(cfg *session.Config) {
		account := cfg.Accounts["test"]
		account.Connection.Dialer = &provider.DialSettings{MaxAttempts: 1}
		cfg.Accounts["test"] = account
	}
	runtime := remoteJourneyRuntimeConfig(t, server.URL, configure)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	t.Cleanup(func() { release.Do(func() { close(finish) }) })
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
				found = found || row.Role == types.RoleAssistant && row.Content == streamParityAnswer
			}
			if !found {
				return false
			}
		}
		return true
	})
	release.Do(func() { close(finish) })
	remoteJourneyUntil(t, shells, func() bool {
		return one.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted &&
			two.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted
	})
	check := func(app *App, agentID string) {
		t.Helper()
		state := app.ws.AgentMgr().Get(agentID).State
		var answers []string
		for _, row := range state.Messages.Messages() {
			if row.Role == types.RoleAssistant {
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
	runtime = remoteJourneyRuntimeConfig(t, server.URL, configure)
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
