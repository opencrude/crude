package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Enter acknowledges a submission in the chat status strip while the core's
// receipt is pending; the composer never carries status text of its own.
func TestPendingSubmissionShowsSendingInChatStatusStrip(t *testing.T) {
	for _, width := range []int{80, 160} {
		app := newFullTestApp(t)
		app.width, app.height = width, 30
		agentID := addBackendTestOrchestrator(t, app)
		app.ActiveSession = app.ws.SessionsByAgent()[agentID]
		app.paintShellFrame()
		app.Input.SetInput("please continue")
		app.setFocusInput()
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if cmd == nil || !app.inputAdmissionPending(agentID) {
			t.Fatalf("width=%d: Enter did not start an off-Update admission", width)
		}
		app.paintShellFrame()
		strip := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
		if !strings.Contains(strip, "Sending") {
			t.Fatalf("width=%d: chat status strip does not show the pending send: %q", width, strip)
		}
		for y := app.inputRect.Y; y < app.inputRect.Y+app.inputRect.H; y++ {
			if row := shellBufferRow(app, y); strings.Contains(row, "Sending") {
				t.Fatalf("width=%d: composer row %d carries status text: %q", width, y, row)
			}
		}
		runAppCommand(t, app, cmd)
		app.paintShellFrame()
		if app.inputAdmissionPending(agentID) {
			t.Fatalf("width=%d: receipt did not settle the pending admission", width)
		}
		if strip := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); strings.Contains(strip, "Sending") {
			t.Fatalf("width=%d: settled admission still shows Sending: %q", width, strip)
		}
	}
}
