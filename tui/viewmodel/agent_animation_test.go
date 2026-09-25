package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestActivityStripShowsUploadThenOwningThinkingInsteadOfPrompt(t *testing.T) {
	for _, width := range []int{80, 160} {
		app := newFullTestApp(t)
		app.width, app.height = width, 40
		app.AddOrchestrator("first")
		app.AgentMgr.Get("first").State.Messages.AppendMessage(workspace.Message{Role: types.RoleUser, Content: "check grep without echoing my prompt"})
		app.paintShellFrame()
		activityY := app.chatRect.Y + app.chatRect.H - 1

		app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "phase", Data: map[string]any{
			"name": "preparing", "request_id": "request-1", "progress": 42,
			"uploaded_bytes": int64(43008), "total_bytes": int64(102400),
		}})
		app.paintShellFrame()
		line := shellBufferRow(app, activityY)
		if !strings.Contains(line, "↑ 42 KB / 100 KB · 42%") || strings.Contains(line, "check grep") {
			t.Fatalf("width=%d upload strip=%q", width, line)
		}
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "thinking", Data: reasoning.Delta{
			Text: "**Assessing line lengths**", Kind: reasoning.Summary, BlockID: "first",
		}})
		app.paintShellFrame()
		line = shellBufferRow(app, activityY)
		if !strings.Contains(line, "Assessing line lengths") || strings.Contains(line, "**") || strings.Contains(line, "check grep") || strings.Contains(line, "42 KB") {
			t.Fatalf("width=%d first thought=%q", width, line)
		}
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "phase", Data: map[string]any{"name": "preparing", "request_id": "request-1", "progress": 90, "uploaded_bytes": int64(90000), "total_bytes": int64(102400)}})
		app.paintShellFrame()
		if got := shellBufferRow(app, activityY); !strings.Contains(got, "Assessing line lengths") {
			t.Fatalf("late upload callback replaced reasoning: %s", got)
		}
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "thinking", Data: reasoning.Delta{
			Text: "**Checking hidden files**", Kind: reasoning.Summary, BlockID: "second",
		}})
		app.paintShellFrame()
		line = shellBufferRow(app, activityY)
		if !strings.Contains(line, "Checking hidden files") || strings.Contains(line, "Assessing line lengths") {
			t.Fatalf("width=%d newer thought=%q", width, line)
		}
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "stream_token", Data: "Answer"})
		app.paintShellFrame()
		if line = shellBufferRow(app, activityY); strings.Contains(line, "Checking hidden files") || strings.Contains(line, "check grep") {
			t.Fatalf("width=%d stale thought after response=%q", width, line)
		}
	}
}

func TestActivityStripDoesNotCompactRawOrUnclassifiedThinking(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("first")
	app.AddOrchestrator("second")
	app.paintShellFrame()
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "thinking", Data: reasoning.Delta{
		Text: strings.Repeat("**Investigating grep**\n\n", 28), Kind: reasoning.Raw,
	}})
	if app.AgentMgr.Get("first").State.ThinkingPaneID == "" {
		t.Fatal("fixture did not promote the raw thinking pane")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "thinking", Data: reasoning.Delta{
		Text: "**Clarifying hidden file behavior**", Kind: reasoning.Raw,
	}})
	app.paintShellFrame()
	y := app.chatRect.Y + app.chatRect.H - 1
	if line := shellBufferRow(app, y); strings.Contains(line, "Clarifying hidden file behavior") {
		t.Fatalf("background agent leaked into selected strip: %q", line)
	}
	app.switchToAgentView("first")
	app.paintShellFrame()
	line := shellBufferRow(app, y)
	if strings.Contains(line, "Clarifying hidden file behavior") || strings.Contains(line, "Investigating grep") {
		t.Fatalf("raw thinking incorrectly received summary treatment: %q", line)
	}
	if pane := app.PaneMgr.Get(app.AgentMgr.Get("first").State.ThinkingPaneID); pane == nil || !strings.Contains(pane.Messages.Messages()[0].Content, "Investigating grep") {
		t.Fatal("full thinking train was lost from the promoted pane")
	}
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "first", Type: "thinking", Data: reasoning.Delta{Text: " unknown gateway trace"}})
	app.paintShellFrame()
	if line = shellBufferRow(app, y); strings.Contains(line, "unknown gateway trace") {
		t.Fatalf("unclassified thinking incorrectly received summary treatment: %q", line)
	}
}

func TestAgentActivityStripHasStableGeometryAndPaintOnlyTide(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("agent")
	state := app.AgentMgr.Get("agent").State
	state.Messages.AppendMessage(workspace.Message{InternalID: "static", Role: workspace.MessageRoleAssistant, Content: "static body"})
	state.Messages.AppendReasoning(reasoning.Delta{Text: "Inspecting the actual state", Kind: reasoning.Summary})
	state.Running = true
	state.TurnIdentity.Generation = 3
	app.syncAgentMessages("agent", false)
	status := types.AgentStatusSnapshot{Ref: types.ResourceRef{Kind: types.ResourceAgent, ID: "agent"}, Phase: types.AgentThinking, Detail: "Reasoning"}
	app.Status.SetAgent(status)
	app.SpinFrame = 0
	app.paintShellFrame()
	renderer := app.activeMessageList()
	before := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW)
	y := app.chatRect.Y + app.chatRect.H - 1
	line := shellBufferRow(app, y)
	if !strings.Contains(line, "Inspecting the actual state") || strings.Contains(line, "Turn 3") {
		t.Fatalf("strip=%q", line)
	}
	var colors []cell.Color
	for x := app.chatRect.X; x < app.width; x++ {
		colors = append(colors, app.mainSurf.Buffer().Get(x, y).FG)
	}
	app.SpinFrame = 700
	app.paintShellFrame()
	after := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW)
	if after.Generation != before.Generation || shellBufferRow(app, y) != line {
		t.Fatal("paint-only animation changed transcript or text geometry")
	}
	changed := false
	for x := app.chatRect.X; x < app.width; x++ {
		changed = changed || colors[x-app.chatRect.X] != app.mainSurf.Buffer().Get(x, y).FG
	}
	if !changed {
		t.Fatal("light tide did not change painted ink")
	}
	viewport := app.chatScreen.ScrollModel().Viewport()
	for _, phase := range []types.AgentPhase{types.AgentTool, types.AgentWaiting, types.AgentCancelled, types.AgentIdle} {
		status.Phase, status.Detail = phase, string(phase)
		app.Status.SetAgent(status)
		state.Running = phase != types.AgentCancelled && phase != types.AgentIdle
		app.paintShellFrame()
		if app.chatScreen.ScrollModel().Viewport() != viewport || renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).ContentHeight != before.ContentHeight {
			t.Fatalf("phase %s moved the page", phase)
		}
		if !strings.Contains(shellBufferRow(app, y), string(phase)) {
			t.Fatalf("phase %s missing from stable strip", phase)
		}
	}
}
