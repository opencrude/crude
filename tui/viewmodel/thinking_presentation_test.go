package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/ui"
	"crude/tui/window"
	"crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestThinkingTwoReadersShareOnlyCanonicalState(t *testing.T) {
	narrow, wide := newFullTestApp(t), newFullTestApp(t)
	state := workspace.NewAgentState("shared", nil, nil)
	for _, app := range []*App{narrow, wide} {
		app.AgentMgr.AddOrchestrator(state.ID, state)
		app.PaneMgr.GetOrCreate(state.ID, workspace.PaneAgent, "Shared").Messages = state.Messages
		app.subPaneProjections.Get(state.ID, workspace.PaneAgent)
	}
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleUser, Content: "prior history", Time: time.Now()})
	state.ApplyEvent(workspace.AgentEvent{AgentID: state.ID, Type: "thinking", Data: reasoning.Delta{Text: strings.Repeat("word ", 40), Kind: reasoning.Raw}})
	thought, _ := state.Messages.ActiveThinking()
	version := state.Messages.Version()
	narrow.reconcileThinkingPresentation(state, 20)
	wide.reconcileThinkingPresentation(state, 200)
	if narrow.thinkingPaneID(state) == "" || wide.thinkingPaneID(state) != "" {
		t.Fatal("independent narrow/wide readers did not make independent decisions")
	}
	if state.Messages.Version() != version || state.Messages.Messages()[1].Content != thought.Content || state.Messages.Messages()[1].SubPaneID != "" {
		t.Fatal("promotion mutated canonical evidence")
	}
	paneID := narrow.thinkingPaneID(state)
	if narrow.Get(paneID).Messages != state.Messages || wide.Get(paneID) != nil {
		t.Fatal("reader shared a local pane or created a second semantic store")
	}
	for _, app := range []*App{narrow, wide} {
		app.syncAgentMessages(state.ID, false)
	}
	if wide.subPaneProjections.GetExisting(state.ID).renderer.Messages()[1].Role != types.RoleThinking {
		t.Fatal("narrow reader changed wide reader's rows")
	}
	state.ApplyEvent(workspace.AgentEvent{AgentID: state.ID, Type: "thinking", Data: reasoning.Delta{Text: "LATEST-EVIDENCE", Kind: reasoning.Summary}})
	version = state.Messages.Version()
	for _, app := range []*App{narrow, wide} {
		app.syncAgentMessages(state.ID, false)
	}
	lines := narrow.renderSubPaneContentForApp(paneID, 20, 3, 0, true)
	if !subPanePlainContains(lines, "EVIDENCE") || narrow.subPaneProjections.ScrollModel(paneID).MaxOffset() <= 0 {
		t.Fatal("stream continuation/late classification did not render with a scrollable body")
	}
	if state.Messages.Version() != version {
		t.Fatal("rendering changed canonical version")
	}
	narrow.dropThinkingPresentation(state)
	if got := narrow.subPaneProjections.GetExisting(state.ID).renderer.Messages()[1]; got.Role != types.RoleThinking || !strings.Contains(got.Content, "LATEST-EVIDENCE") {
		t.Fatal("drop without source change did not restore latest canonical thought")
	}
	state.ApplyEvent(workspace.AgentEvent{AgentID: state.ID, Type: "stream_token", Data: "reply"})
	for _, app := range []*App{narrow, wide} {
		app.reconcileThinkingPresentation(state, 200)
		app.syncAgentMessages(state.ID, false)
		if app.thinkingPaneID(state) != "" {
			t.Fatal("completion retained a temporary pane")
		}
	}
	if state.Messages.Messages()[1].ThinkingActive || !strings.Contains(state.Messages.Messages()[1].Content, "LATEST-EVIDENCE") {
		t.Fatal("completion lost evidence")
	}
}

func TestThinkingGeometryUsesExactOwnerAndRetainsHiddenWidth(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("background")
	app.AddOrchestrator("selected")
	panes := app.ensureMainPanes()
	background, selected := &mainPaneView{}, &mainPaneView{}
	panes.agentViews["background"], panes.agentViews["selected"] = background, selected
	panes.groups = map[window.GroupID]*mainPaneGroup{1: {view: background, chat: ui.Rect{W: 40}}, 2: {view: selected, chat: ui.Rect{W: 180}}}
	_, expected := boundedContentGeometry(40, app.Theme.Spacing.PadLR)
	if app.thinkingOwnerWidth("background") != expected {
		t.Fatal("background owner borrowed selected split width")
	}
	panes.groups[1].view = nil
	if app.thinkingOwnerWidth("background") != expected {
		t.Fatal("hidden owner lost last-known width")
	}
	child := workspace.NewAgentState("nested", nil, nil)
	app.AgentMgr.AddSubAgent("nested", "background", "Child", "", "researcher", child)
	if app.thinkingOwnerWidth("nested") != expected {
		t.Fatal("nested owner did not use parent embedding geometry")
	}
}

func TestThinkingLatchUsesExactSessionOrSyntheticRoot(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("first")
	app.AddOrchestrator("second")
	first, second := app.AgentMgr.Get("first").State, app.AgentMgr.Get("second").State
	owner := workspace.NewSession("owned", t.TempDir(), nil, nil)
	app.SessionsByAgent = map[string]*workspace.Session{"first": owner}
	app.ActiveSession = workspace.NewSession("unrelated", t.TempDir(), nil, nil)
	first.Messages.AppendThinking(strings.Repeat("long reasoning\n", 30))
	app.reconcileThinkingPresentation(first, 40)
	if !app.thinkingLatch[owner] || app.thinkingLatch[app.ActiveSession] || app.thinkingLatch[nil] {
		t.Fatal("latch used selected/nil session")
	}
	second.Messages.AppendThinking("brief")
	app.reconcileThinkingPresentation(second, 200)
	if app.thinkingPaneID(second) != "" {
		t.Fatal("unrelated synthetic root inherited session latch")
	}
	second.Messages.AppendThinking(strings.Repeat("long reasoning\n", 30))
	app.reconcileThinkingPresentation(second, 40)
	child := workspace.NewAgentState("child", nil, nil)
	app.AgentMgr.AddSubAgent("child", "second", "Child", "", "researcher", child)
	child.Messages.AppendThinking("brief")
	app.reconcileThinkingPresentation(child, 200)
	if app.thinkingPaneID(child) == "" {
		t.Fatal("synthetic child did not inherit exact root latch")
	}
}

func TestThinkingCompletedSyntheticPanePreservesSummary(t *testing.T) {
	app := newFullTestApp(t)
	pane := app.PaneMgr.GetOrCreate("synthetic", workspace.PaneThinking, "Thinking")
	pane.TokenCount = 5200
	pane.Collapse()
	lines := app.subPaneProjections.Render(pane.ID, pane, 80, 5, 0, false)
	if len(lines) != 1 || !subPanePlainContains(lines, "Thought") || !subPanePlainContains(lines, "5.2K") {
		t.Fatal("completed synthetic thinking lost existing summary output")
	}
}
