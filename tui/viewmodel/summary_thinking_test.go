package viewmodel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	ctypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestSummaryThinkingStaysCompactAndPreservesFullTrainOnResume(t *testing.T) {
	for _, width := range []int{80, 160} {
		for _, kind := range []reasoning.Kind{reasoning.Summary, reasoning.Provisional} {
			app := newFullTestApp(t)
			app.width, app.height = width, 40
			app.AddOrchestrator("summary")
			state := app.ws.AgentMgr().Get("summary").State
			deltas := []reasoning.Delta{{Text: "**First approach**\n\nInspect the ownership and geometry.", Kind: kind, BlockID: "first"}}
			for i := range 20 {
				deltas = append(deltas, reasoning.Delta{Text: "Another brief update inspects ownership and geometry.", Kind: kind, BlockID: fmt.Sprintf("update-%d", i)})
			}
			deltas = append(deltas,
				reasoning.Delta{Text: "**Revised approach** uses `stable IDs`", Kind: kind, BlockID: "second"},
				reasoning.Delta{Text: " and preserves evidence.", Kind: kind, BlockID: "second"},
			)
			for _, delta := range deltas {
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "summary", Type: "thinking", Data: delta})
				app.paintShellFrame()
				if app.thinkingPaneID(state) != "" {
					t.Fatal("summary promoted into a pane")
				}
			}
			renderer := app.activeMessageList()
			rows := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Rows
			if len(rows) != 0 {
				t.Fatalf("orphan active thinking header=%+v", rows)
			}
			compact := shellBufferRow(app, app.chatRect.Y+app.chatRect.H-1)
			if !strings.Contains(compact, "Revised approach") || strings.Contains(compact, "First approach") || strings.Contains(compact, "**") || strings.Contains(compact, "`") {
				t.Fatalf("compact markdown=%q", compact)
			}
			if kind == reasoning.Provisional && !strings.Contains(compact, "Thinking ▸ ·") {
				t.Fatal("first request was falsely labelled a confirmed summary")
			}
			id := state.Messages.Messages()[0].InternalID
			generation := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Generation
			for frame := 0; frame < 1000; frame += 100 {
				renderer.SetSpinFrame(frame)
				if next := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW); next.Generation != generation {
					t.Fatal("summary animation re-rendered Markdown")
				}
			}
			renderer.ToggleLastDisclosure()
			expanded := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Rows
			if len(expanded) != 1 || !strings.Contains(strings.Join(expanded[0].Lines, "\n"), "First approach") || len(expanded[0].Lines) < 10 {
				t.Fatal("expansion lost earlier reasoning")
			}
			renderer.ToggleLastDisclosure()
			if collapsed := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Rows; len(collapsed) != 0 {
				t.Fatalf("keyboard collapse left an orphan header: %+v", collapsed)
			}
			renderer.ToggleDisclosure(id)
			if kind == reasoning.Provisional {
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "summary", Type: "reasoning_classified", Data: reasoning.InferredSummary})
				if changed := state.Messages.Messages()[0]; changed.InternalID != id || changed.ThinkingDisplay.Kind != reasoning.InferredSummary {
					t.Fatalf("settling a provisional request moved its canonical thought: %+v", changed)
				}
			}
			app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "summary", Type: "done", Data: map[string]any{"reason": "completed"}})
			snapshot := state.Messages.Messages()[0]
			if snapshot.ThinkingActive || !snapshot.ThinkingDisplay.Kind.IsSummary() {
				t.Fatalf("settled metadata=%+v", snapshot)
			}
			db := sessiondb.OpenAndMigrateAt(t.TempDir())
			if err := sessiondb.CreateSessionRow(db, "summary"); err != nil {
				t.Fatal(err)
			}
			if err := sessiondb.AppendMessageWithMeta(db, "summary", provider.NewTextMessage("assistant", ""), 1, sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{snapshot}}); err != nil {
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
			if len(views) != 1 || !reflect.DeepEqual(views[0].ThinkingDisplay, snapshot.ThinkingDisplay) || views[0].Content != snapshot.Content {
				t.Fatal("resume lost reasoning kind, boundaries, or text")
			}
			resumed := chatprojection.NewRenderer(app.Theme)
			resumed.SetSize(width, 40)
			resumed.SetChatAreaBounds(0, 30)
			resumed.SyncMessages(ChatMessagesFromViews(views), 1, 0)
			got := resumed.ExportMessageRowsSnapshot(width).Rows
			if len(got[0].Lines) != 2 || got[0].Native[1].TextTide {
				t.Fatal("resumed summary expanded or animated without an active turn")
			}
		}
	}
}

func TestThinkingStripClickOpensAndClosesHiddenLiveTrace(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 100, 36
	app.AddOrchestrator("thinking")
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "thinking", Type: "thinking", Data: reasoning.Delta{
		Text: "**Inspecting first**\n\n**Checking second**", Kind: reasoning.Provisional,
	}})
	app.paintShellFrame()
	renderer := app.activeMessageList()
	id := app.ws.AgentMgr().Get("thinking").State.Messages.Messages()[0].InternalID
	if rows := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Rows; len(rows) != 0 {
		t.Fatalf("collapsed live thought left a stray node: %+v", rows)
	}
	x, y := app.chatRect.X+app.Theme.Spacing.PadLR+5, app.chatRect.Y+app.chatRect.H-1
	if line := shellBufferRow(app, y); !strings.Contains(line, "Thinking ▸ · Checking second") {
		t.Fatalf("strip did not own the compact thought: %q", line)
	}
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	app.paintShellFrame()
	rows := renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Rows
	if !renderer.DisclosureExpanded(id) || len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Inspecting first") {
		t.Fatalf("strip click did not expose full train: %+v", rows)
	}
	if line := shellBufferRow(app, y); !strings.Contains(line, "Thinking ▾") {
		t.Fatalf("strip did not acknowledge disclosure: %q", line)
	}
	_, _ = app.dispatchMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	app.paintShellFrame()
	if renderer.DisclosureExpanded(id) || len(renderer.ExportMessageRowsSnapshot(app.chatReflowInnerW).Rows) != 0 {
		t.Fatal("closing via strip left a standalone thinking header")
	}
}

func TestThinkingTideDirectionFollowsOwningConnection(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("first")
	app.AddOrchestrator("other")
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "first", Type: "turn_started", Data: ctypes.TurnIdentity{Generation: 1, ProviderID: "gateway", AccountID: "account", ModelID: "model"}})
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "first", Type: "thinking", Data: reasoning.Delta{Text: "Provisional thinking", Kind: reasoning.Provisional}})
	if app.ws.AgentActivity("first").TideForward {
		t.Fatal("unclassified thinking moved forward before evidence")
	}
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "first", Type: "reasoning_classified", Data: reasoning.InferredSummary})
	if !app.ws.AgentActivity("first").TideForward || app.ws.AgentActivity("other").TideForward {
		t.Fatal("settled direction leaked to another agent or failed to settle")
	}
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "first", Type: "stream_token", Data: "Answer"})
	if !app.ws.AgentActivity("first").TideForward {
		t.Fatal("forward tide stopped at the end of thinking")
	}
	app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "first", Type: "turn_started", Data: ctypes.TurnIdentity{Generation: 2, ProviderID: "other-gateway", AccountID: "account", ModelID: "model"}})
	if app.ws.AgentActivity("first").TideForward {
		t.Fatal("changed serving connection retained stale tide classification")
	}
}
