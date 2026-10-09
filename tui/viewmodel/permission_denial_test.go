package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/types"
	coretypes "crude/types"
)

func TestPermissionDenyImmediatelyClearsApprovalWithoutWaitingForBackend(t *testing.T) {
	app, alpha, beta, channels := newBackendPromptFamilyApp(t)
	const draft = "Keep this draft exactly.\n  Including whitespace."
	for _, size := range []struct{ w, h int }{{160, 40}, {80, 24}} {
		for _, route := range []string{"dock-key", "dock-click", "dock-tab-close", "modal-key"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size.w, size.h, route), func(t *testing.T) {
				app.width, app.height = size.w, size.h
				app.switchToAgentView(alpha)
				app.Input.SetInput(draft)
				if route == "modal-key" {
					app.switchToAgentView(beta)
					app.Input.SetInput(draft)
				}
				emitTestPermission(app, "child-a", "denied-block")
				app.paintShellFrame()
				status, _ := app.ws.Status().Agent("child-a")
				if !status.NeedsAction || status.Phase != coretypes.AgentApproval {
					t.Fatal("fixture did not reproduce a pending approval")
				}
				request := app.agentPrompts[0]
				switch route {
				case "dock-click":
					var cmd tea.Cmd
					for y := range app.height {
						line := shellBufferRow(app, y)
						if index := strings.Index(line, "Deny"); index >= 0 {
							_, cmd = app.dispatchMouseClick(tea.MouseClickMsg{
								X: rendering.VisibleWidth(line[:index]), Y: y, Button: tea.MouseLeft,
							})
							break
						}
					}
					if cmd == nil {
						t.Fatal("painted Deny button did not dispatch")
					}
					app.Update(cmd())
				case "dock-key":
					_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: 'n', Text: "n"})
					if cmd == nil {
						t.Fatal("Deny key did not dispatch")
					}
					app.Update(cmd())
				case "dock-tab-close":
					app.closeAgentPromptTab(request.panelID())
				case "modal-key":
					_, cmd := app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
					if cmd != nil {
						app.Update(cmd())
					}
				}
				select {
				case response := <-channels["child-a"]:
					if response.RequestID != "denied-block" || response.Value != "deny" {
						t.Fatalf("wrong backend response: %+v", response)
					}
				default:
					t.Fatal("denial did not reach the exact backend")
				}
				if app.ws.AgentMgr().Get("child-a").State.PendingBlockID != "" {
					t.Error("accepted denial left the approval pending until a backend event")
				}
				// No backend event has arrived yet. Immediate and settled frames,
				// including a return to the owner tab, must not revive approval.
				for _, owner := range []string{"child-a", alpha, "child-a"} {
					app.switchToAgentView(owner)
					app.syncAgentStatusInventory()
					app.paintShellFrame()
					status, _ = app.ws.Status().Agent("child-a")
					if status.NeedsAction || status.Phase == coretypes.AgentApproval {
						t.Errorf("Deny resurfaced approval: %+v", status)
					}
					if strip := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); strings.Contains(strip, "Needs approval") {
						t.Errorf("painted stale approval: %q", strip)
					}
				}
				if len(app.agentPrompts) != 0 || app.OverlayState.IsBlocking() || request.projection.deck.IsOpen() {
					t.Error("Deny retained its blocking UI")
				}
				// Return to the composer that owned the draft.
				if route == "modal-key" {
					app.switchToAgentView(beta)
				}
				if app.Input.Value() != draft {
					t.Errorf("Deny changed the draft: %q", app.Input.Value())
				}
				emitTestPermission(app, "child-a", "new-block")
				app.handleAgentEvent(types.AgentEventMsg{AgentID: "child-a", Type: "blocking_complete", Data: map[string]string{"block_id": "denied-block"}})
				if app.ws.AgentMgr().Get("child-a").State.PendingBlockID != "new-block" || len(app.agentPrompts) != 1 {
					t.Error("late denial completion removed a newer request")
				}
				app.resolvePromptConfirmation(app.agentPrompts[0].confirmation, "deny")
				<-channels["child-a"]
			})
		}
	}
}
