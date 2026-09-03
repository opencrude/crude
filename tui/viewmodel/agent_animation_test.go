package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/ui"
	"crude/workspace"
)

func TestAgentWindowDynamicRowsFollowFrameAndSourceDependencies(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("agent")
	node := app.AgentMgr.Get("agent")
	node.State.Messages.AppendMessage(workspace.Message{InternalID: "static", Role: workspace.MessageRoleAssistant, Content: "static body"})
	node.State.Messages.UpdateTurnStatus(workspace.TurnStatus{Phase: "preparing", Progress: 10, UploadedBytes: 10, TotalBytes: 100})
	app.syncAgentMessages("agent", false)
	app.SpinFrame = 0
	_ = app.View()
	renderer := app.activeMessageList()
	first := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW)
	statusIndex := -1
	for index, row := range first.Rows {
		if strings.Contains(strings.Join(row.Lines, " "), "Getting next turn ready") {
			statusIndex = index
			break
		}
	}
	if statusIndex < 0 {
		t.Fatal("lifecycle row missing")
	}
	if !strings.Contains(strings.Join(first.Rows[statusIndex].Lines, " "), "●") {
		t.Fatal("filled animation phase missing")
	}
	app.SpinFrame = ui.ActivityBlinkPhaseMillis
	_ = app.View()
	second := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW)
	if second.Generation == first.Generation || !strings.Contains(strings.Join(second.Rows[statusIndex].Lines, " "), "○") {
		t.Fatalf("hollow animation did not reach agent window: %+v", second.Rows[statusIndex].Lines)
	}
	node.State.Messages.UpdateTurnStatus(workspace.TurnStatus{Phase: "preparing", Progress: 75, UploadedBytes: 75, TotalBytes: 100})
	app.syncAgentMessages("agent", false)
	_ = app.View()
	third := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW)
	plain := strings.Join(third.Rows[statusIndex].Lines, " ")
	if !strings.Contains(plain, "75%") || third.Generation == second.Generation {
		t.Fatalf("in-place metric update=%q generations %d/%d", plain, second.Generation, third.Generation)
	}
}
