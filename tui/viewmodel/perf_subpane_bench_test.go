package viewmodel

import (
	"strconv"
	"testing"

	"crude/tui/ui"
	"crude/types/conversation"
	"crude/workspace"
)

func denseChildPanes(t testing.TB, perAgent int) (*App, []*workspace.SubPane) {
	t.Helper()
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	panes := make([]*workspace.SubPane, 4)
	for i := range panes {
		id := "child-" + strconv.Itoa(i)
		pane := app.ws.PaneMgr().Ensure(id, workspace.PaneAgent, id)
		app.ws.AgentMgr().AddSubAgent(id, "root", id, "model", "crafter", workspace.NewAgentState(id, pane.Messages, app.ws.Logger()))
		app.ensureAgentProjection(id)
		for j := range perAgent {
			msg := conversation.Message{Role: conversation.RoleAssistant, Content: "Read the evidence and check the result."}
			if j%4 == 0 {
				msg = conversation.Message{Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: `{"path":"main.go"}`, ToolStatus: "success", ToolPreviewPhase: "done"}
			}
			pane.Messages.AppendMessage(msg)
		}
		// An active tool animates without changing the retained history.
		pane.Messages.AppendMessage(conversation.Message{Role: conversation.RoleTool, ToolName: "go_vet", ToolStatus: conversation.ToolStatusCalling})
		app.syncAgentMessages(id, false)
		panes[i] = pane
	}
	return app, panes
}

// BenchmarkDenseChildPaneSync measures the tick's existing sync entry point
// over four warm children. It deliberately does not time first hydration.
func BenchmarkDenseChildPaneSync(b *testing.B) {
	app, panes := denseChildPanes(b, 2000)
	b.ReportAllocs()
	for b.Loop() {
		for _, pane := range panes {
			app.syncAgentMessages(pane.ID, false)
		}
	}
}

// BenchmarkDenseChildPanePaint measures the inline compact projection across
// animation frames while no transcript edits have arrived.
func BenchmarkDenseChildPanePaint(b *testing.B) {
	app, panes := denseChildPanes(b, 2000)
	for _, pane := range panes {
		app.subPaneProjections.Render(pane.ID, pane, 80, 8, 0, true)
	}
	b.ReportAllocs()
	for frame := 0; b.Loop(); frame += ui.ActivityBlinkPhaseMillis {
		for _, pane := range panes {
			app.subPaneProjections.Render(pane.ID, pane, 80, 8, frame, true)
		}
	}
}
