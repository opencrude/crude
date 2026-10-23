package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func cancelNotices(app *App) []string {
	var out []string
	for _, notice := range app.ws.Status().Notifications(true) {
		if strings.Contains(notice.Summary, "Stopping") || strings.Contains(notice.Summary, "Nothing to stop") {
			out = append(out, notice.Summary)
		}
	}
	return out
}

// An idle session's root agent keeps an uncollapsed pane for its whole life.
// Esc and Cancel Agent must treat it as idle: no stopping boundary that no
// terminal acknowledgement will ever clear, no false "Stopping" notice, and
// no 60 Hz repaint while nothing runs.
func TestCancelOnIdleAgentIsGraceful(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	node := app.ws.AgentMgr().Get("agent")
	if node == nil || node.State == nil {
		t.Fatal("test setup: no agent state")
	}
	if pane := app.ws.PaneMgr().Get("agent"); pane == nil || pane.Done {
		t.Fatal("precondition: the idle root agent keeps an open pane")
	}
	if app.wantsFastTick() {
		t.Fatal("an idle session repaints at frame rate")
	}
	for range 3 {
		_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	}
	if node.State.Cancelling {
		t.Fatal("Esc entered the stopping boundary of an idle agent")
	}
	if notices := cancelNotices(app); len(notices) != 0 {
		t.Fatalf("Esc on an idle session reported %q", notices)
	}
}

// A running turn is stopped once; further presses while it stops are no-ops.
func TestCancelRunningTurnOnceThenQuiet(t *testing.T) {
	app := setupChatInputApp(t, 80, 24, false)
	node := app.ws.AgentMgr().Get("agent")
	node.State.Running = true
	if !app.wantsFastTick() {
		t.Fatal("a running agent must repaint at frame rate")
	}
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if !node.State.Cancelling {
		t.Fatal("Esc did not stop the running turn")
	}
	if !app.wantsFastTick() {
		t.Fatal("a stopping turn must keep animating until acknowledged")
	}
	if app.cancelAgentTurn("agent") {
		t.Fatal("a turn already stopping was cancelled again")
	}
	_, _ = app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if notices := cancelNotices(app); len(notices) != 0 {
		t.Fatalf("repeated Esc while stopping reported %q", notices)
	}
}
