package viewmodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestChildRawReasoningStaysInItsPaneWhileParentHasCompactThinking(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("parent")
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "parent", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "parent-account", ModelID: "parent-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "parent", Type: "thinking", Data: reasoning.Delta{
		Text: "Parent summary", Kind: reasoning.Summary,
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{
		"agent_id": "child", "agent_name": "Researcher", "category": "researcher", "parent_id": "parent", "model": "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "child-account", ModelID: "child-model",
	}})
	trace := strings.Repeat("Raw child investigation across the source.\n", 30)
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{
		Text: trace, Kind: reasoning.InferredRaw,
	}})

	child := app.AgentMgr.Get("child")
	parent := app.AgentMgr.Get("parent")
	if child == nil || app.thinkingPaneID(child.State) == "" {
		t.Fatal("child raw trace did not promote into its own thinking pane")
	}
	pane := app.Get(app.thinkingPaneID(child.State))
	if pane == nil {
		t.Fatal("child thinking pane missing")
	}
	if thought, active := pane.Messages.ActiveThinking(); !active || !strings.Contains(thought.Content, "Raw child investigation") {
		t.Fatal("child thinking pane lost the raw body")
	}
	if app.thinkingPaneID(parent.State) != "" || !strings.Contains(app.AgentActivity("parent").Thinking, "Parent summary") {
		t.Fatal("child raw trace changed the parent's compact reasoning")
	}
	app.switchToAgentView("child")
	app.paintShellFrame()
	if line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); strings.Contains(line, "Raw child investigation") || strings.Contains(line, "Parent summary") {
		t.Fatalf("child raw trace or parent summary leaked into child strip: %q", line)
	}
	app.switchToAgentView("parent")
	app.paintShellFrame()
	if line := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1); !strings.Contains(line, "Parent summary") || strings.Contains(line, "Raw child investigation") {
		t.Fatalf("parent strip lost its own reasoning: %q", line)
	}
}

func TestResponsesVerboseSummaryPromotesForRootAndChildWithoutReclassification(t *testing.T) {
	for _, width := range []int{80, 160} {
		for _, child := range []bool{false, true} {
			for _, terminal := range []string{"completed", "cancelled", "failed"} {
				t.Run(fmt.Sprintf("%d/child=%t/%s", width, child, terminal), func(t *testing.T) {
					app := newFullTestApp(t)
					app.width, app.height = width, 40
					app.AddOrchestrator("parent")
					owner := "parent"
					if child {
						app.handleAgentEvent(types.AgentEventMsg{AgentID: "parent", Type: "thinking", Data: reasoning.Delta{Text: "Parent stays compact", Kind: reasoning.Summary}})
						owner = "researcher"
						app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "sub_agent_started", Data: map[string]string{
							"agent_id": owner, "agent_name": "Asha", "category": "researcher", "parent_id": "parent", "model": "opaque-served-model",
						}})
					}
					app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "turn_started", Data: types.TurnIdentity{
						Generation: 1, ProviderID: "gateway", AccountID: "owner-account", ModelID: "opaque-served-model",
					}})
					// Background children must promote before their tab is selected.
					app.switchToAgentView("parent")
					app.paintShellFrame()
					state := app.AgentMgr.Get(owner).State
					decoder := (&provider.OpenAIResponsesStandard{}).NewStreamDecoder()
					stream := func(text string) {
						t.Helper()
						wire, err := json.Marshal(map[string]any{"type": "response.reasoning_summary_text.delta", "output_index": 0, "summary_index": 0, "delta": text})
						if err != nil {
							t.Fatal(err)
						}
						update, err := decoder.Decode(provider.StreamEvent{Data: wire})
						if err != nil {
							t.Fatal(err)
						}
						for _, delta := range update.Reasoning {
							app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "thinking", Data: delta})
						}
					}
					stream("**Inspecting ownership**\n\n")
					if app.thinkingPaneID(state) != "" {
						t.Fatal("short summary unexpectedly promoted")
					}
					thoughtIndex := len(state.Messages.Messages()) - 1
					thoughtID := state.Messages.Messages()[thoughtIndex].InternalID
					paragraph := "Inspect each operation, preserve the exact owner and check the persisted evidence before deciding what changes are needed.\n\n"
					trace := "**Inspecting ownership**\n\n" + strings.Repeat(paragraph, 45)
					for range 45 {
						stream(paragraph)
					}
					paneID := app.thinkingPaneID(state)
					if paneID == "" {
						t.Fatal("gateway's long summary_text stream remained confined to the activity strip")
					}
					stream("MORE-EVIDENCE")
					trace += "MORE-EVIDENCE"
					pane := app.Get(paneID)
					thought, ok := pane.Messages.ActiveThinking()
					if !ok || thought.InternalID != thoughtID || thought.Content != trace || thought.ThinkingDisplay.Kind != reasoning.Summary {
						t.Fatal("promotion changed source classification, identity or stream content")
					}
					app.switchToAgentView(owner)
					app.paintShellFrame()
					gutter := app.subPaneProjections.ForOwner(owner).PaneGutterState(paneID, pane)
					if !gutter.Visible || gutter.Scroll == nil || gutter.Scroll.MaxOffset() <= 0 {
						t.Fatal("promoted summary has no scrolling body")
					}
					painted := false
					for row, info := range app.chatScreen.ViewportPaneLineMap() {
						if info.PaneID == paneID && strings.Contains(shellBufferRow(app, app.chatRect.Y+row), "MORE-EVIDENCE") {
							painted = true
						}
					}
					if !painted {
						t.Fatal("latest streamed content is not painted in the thinking pane")
					}
					if strings.Contains(shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1), "MORE-EVIDENCE") {
						t.Fatal("promoted body duplicated in activity strip")
					}
					if child && app.thinkingPaneID(app.AgentMgr.Get("parent").State) != "" {
						t.Fatal("child changed parent's presentation")
					}
					projection := app.subPaneProjections.ForOwner(owner).Get(paneID, workspace.PaneThinking).renderer
					generation := projection.ExportMessageRowsSnapshot(width).Generation
					for frame := 0; frame < 1200; frame += 400 {
						projection.SetSpinFrame(frame)
						if projection.ExportMessageRowsSnapshot(width).Generation != generation {
							t.Fatal("pane header animation reflowed the unchanged summary body")
						}
					}
					if terminal == "failed" {
						app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "error", Data: "connection interrupted"})
					} else {
						app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "done", Data: map[string]any{"reason": terminal}})
					}
					if app.PaneMgr.Has(paneID) || app.subPaneProjections.Has(paneID) || app.thinkingPaneID(state) != "" {
						t.Fatal("settled thought left a competing pane owner")
					}
					thought = state.Messages.Messages()[thoughtIndex]
					if thought.Content != trace || thought.InternalID != thoughtID || thought.ThinkingDisplay.Kind != reasoning.Summary || thought.ThinkingActive {
						t.Fatal("settled transcript lost provider evidence")
					}
					db := sessiondb.OpenAndMigrateAt(t.TempDir())
					if err := sessiondb.CreateSessionRow(db, owner); err != nil {
						t.Fatal(err)
					}
					if err := sessiondb.AppendMessageWithMeta(db, owner, provider.NewTextMessage("assistant", ""), 1, sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{thought}}); err != nil {
						t.Fatal(err)
					}
					stored, err := sessiondb.LoadMessagesWithMeta(db)
					if err != nil {
						t.Fatal(err)
					}
					if err := db.Close(); err != nil {
						t.Fatal(err)
					}
					views := workspace.ConvertMessagesForDisplay(stored)
					resumed := chatprojection.NewRenderer(app.Theme)
					resumed.SetSize(width, 40)
					resumed.SetChatAreaBounds(0, 30)
					resumed.SyncMessages(ChatMessagesFromViews(views), 1)
					resumed.ToggleDisclosure(thought.InternalID)
					rows := resumed.ExportMessageRowsSnapshot(width).Rows
					visible := ""
					for _, row := range rows {
						visible += strings.Join(row.Lines, "\n")
					}
					if !strings.Contains(visible, "MORE-EVIDENCE") || views[0].ThinkingDisplay.Kind != reasoning.Summary {
						t.Fatalf("resume/disclosure lost the full classified trace: kind=%q rows=%d text=%q", views[0].ThinkingDisplay.Kind, len(rows), visible)
					}
					app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "turn_started", Data: types.TurnIdentity{Generation: 2, ProviderID: "another-wire", AccountID: "new-account", ModelID: "new-model"}})
					app.handleAgentEvent(types.AgentEventMsg{AgentID: owner, Type: "thinking", Data: reasoning.Delta{Text: "Short next block", Kind: reasoning.Summary, BlockID: "new-block"}})
					// The session stays latched after it promoted one block, so a
					// later short thought opens in its own pane instead of restarting
					// inline. Only a settings change clears that choice.
					if app.thinkingPaneID(state) == "" {
						t.Fatal("session latch did not carry into the next turn")
					}
				})
			}
		}
	}
}

func TestChildMeasuredRawReasoningPromotesAfterUsageArrives(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 40
	app.AddOrchestrator("parent")
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{
		"agent_id": "child", "agent_name": "Researcher", "parent_id": "parent", "model": "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "turn_started", Data: types.TurnIdentity{
		Generation: 1, ProviderID: "gateway", AccountID: "child-account", ModelID: "child-model",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{
		Text: strings.Repeat("unlabelled child trace.\n", 25), Kind: reasoning.Provisional,
	}})
	child := app.AgentMgr.Get("child")
	if child == nil || app.thinkingPaneID(child.State) == "" {
		t.Fatal("child trace did not promote through the shared overflow rule")
	}
	if pane := app.Get(app.thinkingPaneID(child.State)); pane == nil || !strings.Contains(pane.Messages.Messages()[0].Content, "unlabelled child trace") {
		t.Fatal("child trace was lost during promotion")
	}
}
