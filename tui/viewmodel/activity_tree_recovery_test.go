package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/ui"
	ctypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func TestPromotedThoughtsJoinActivityAcrossRequests(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = width, 40
			app.AddOrchestrator("activity-owner")
			app.AddOrchestrator("other-tab") // completion must target the owner, not the selected tab
			state := app.AgentMgr.Get("activity-owner").State
			renderer := app.subPaneProjections.GetExisting(state.ID).renderer
			renderer.SetSize(width, 40)
			renderer.SetChatAreaBounds(0, 30)
			for round := range 8 {
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "thinking", Data: reasoning.Delta{Text: thinkingLines(40)}})
				if state.ThinkingPaneID == "" {
					t.Fatal("fixture did not exercise real thinking promotion")
				}
				id := fmt.Sprintf("read-%d", round)
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "tool_call", Data: map[string]any{
					"tool_id": id, "internal_id": id, "request_id": id, "name": "read_file", "args": fmt.Sprintf(`{"path":"file-%d.go"}`, round),
				}})
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "tool_result", Data: map[string]any{
					"tool_id": id, "internal_id": id, "name": "read_file", "result": "package fixture",
				}})
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "phase", Data: map[string]any{"name": "waiting_provider", "provider": "Custom"}})
				rows, _, _ := renderer.ExportMessageRows(width)
				if len(rows) != 1 || !strings.HasPrefix(rows[0].ID, "activity:") {
					var text []string
					for _, row := range rows {
						text = append(text, row.Lines...)
					}
					t.Fatalf("round %d left orphan activity:\n%s", round, strings.Join(text, "\n"))
				}
			}
			app.switchToAgentView(state.ID)
			app.paintShellFrame()
			if app.AgentMgr.ActiveID() != state.ID {
				t.Fatal("owner tab did not restore")
			}
			app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "done", Data: map[string]any{"reason": "completed"}})
			var thoughts []string
			for _, message := range state.Messages.Messages() {
				if message.SubPaneID != "" {
					t.Fatalf("completed thinking still owns a pane boundary: %+v", message)
				}
				if message.Role == ctypes.RoleThinking {
					thoughts = append(thoughts, message.InternalID)
				}
			}
			if len(thoughts) != 8 {
				t.Fatalf("lost completed thoughts: %v", thoughts)
			}
			for i := range 3 {
				state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
			}
			app.syncAgentMessages(state.ID, false)
			app.paintShellFrame()
			painted := bufferPlain(app.mainSurf.Buffer())
			for i := 2; i < 8; i++ {
				if !strings.Contains(painted, fmt.Sprintf("file-%d.go", i)) {
					t.Fatalf("recent leaf %d was not painted:\n%s", i, painted)
				}
			}
			db := sessiondb.OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			if err := sessiondb.CreateSessionRow(db, state.ID); err != nil {
				t.Fatal(err)
			}
			for _, snapshot := range state.Messages.Messages() {
				wire := provider.NewTextMessage("assistant", snapshot.Content)
				if err := sessiondb.AppendMessageWithMeta(db, state.ID, wire, 1, sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{snapshot}}); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil {
				t.Fatal(err)
			}
			recovered := chatprojection.NewRenderer(app.Theme)
			recovered.SetSize(width, 40)
			recovered.SetChatAreaBounds(0, 30)
			recovered.SyncMessages(ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(loaded)), 1)
			// Keep geometry identical after the full shell changed the live viewport.
			renderer.SetSize(width, 40)
			renderer.SetChatAreaBounds(0, 30)
			for _, stage := range []string{"compact", "tree", "thought"} {
				left, _, _ := renderer.ExportMessageRows(width)
				right, _, _ := recovered.ExportMessageRows(width)
				if len(left) != 1 || len(right) != 1 {
					t.Fatalf("%s: orphan nodes live=%d recovered=%d", stage, len(left), len(right))
				}
				assertSameRows(t, stage, left[0], right[0])
				if stage == "compact" {
					text := strings.Join(left[0].Lines, "\n")
					for i := range 8 {
						if strings.Contains(text, fmt.Sprintf("file-%d.go", i)) != (i >= 2) {
							t.Fatalf("incorrect recent context:\n%s", text)
						}
					}
					renderer.ToggleDisclosure(left[0].ID)
					recovered.ToggleDisclosure(right[0].ID)
				} else if stage == "tree" {
					if len(left[0].LeafToggles) != 16 {
						t.Fatalf("expansion lost thoughts or tools: %v", left[0].LeafToggles)
					}
					renderer.ToggleDisclosure(thoughts[0])
					recovered.ToggleDisclosure(thoughts[0])
				} else if !strings.Contains(strings.Join(left[0].Lines, "\n"), strings.Repeat("x", 30)) {
					t.Fatal("expanded thought lost its body")
				}
			}
		})
	}
}

func TestPromotedThinkingSettlesOnEveryLifecyclePath(t *testing.T) {
	for _, event := range []struct {
		name        string
		data        any
		interrupted bool
	}{
		{"tool_progress", map[string]any{"tool_id": "read", "name": "read_file"}, false},
		{"tool_result", map[string]any{"tool_id": "read", "name": "read_file", "result": "file"}, false},
		{"permission_resolved", map[string]any{}, false},
		{"done", map[string]any{"reason": "completed"}, false},
		{"error", "provider disconnected", true},
		{"cancel", nil, true},
	} {
		t.Run(event.name, func(t *testing.T) {
			app := newFullTestApp(t)
			app.AddOrchestrator("parent")
			app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "child", Type: "start", Data: map[string]string{"parent_id": "parent", "agent_name": "Child"}})
			state := app.AgentMgr.Get("child").State
			app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{Text: thinkingLines(40)}})
			paneID := state.ThinkingPaneID
			if paneID == "" {
				t.Fatal("nested thought not promoted")
			}
			if event.name == "cancel" {
				app.cancelAgentTurn("child")
			} else {
				app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "child", Type: event.name, Data: event.data})
			}
			if state.ThinkingPaneID != "" || app.PaneMgr.Has(paneID) || app.subPaneProjections.Has(paneID) {
				t.Fatal("settled nested thought left a pane boundary")
			}
			thought := state.Messages.Messages()[0]
			if thought.Role != ctypes.RoleThinking || thought.ThinkingActive || thought.ThinkingInterrupted != event.interrupted || thought.Content != thinkingLines(40) {
				t.Fatalf("incorrect restored thought: %+v", thought)
			}
		})
	}
}

func TestCompletedPaneAndActivityShareOneSpacer(t *testing.T) {
	app := newFullTestApp(t)
	app.AddOrchestrator("root")
	child := app.PaneMgr.GetOrCreate("finished-child", workspace.PaneAgent, "Finished child")
	app.subPaneProjections.Get(child.ID, child.Type)
	child.Collapse()
	state := app.AgentMgr.Get("root").State
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleTool, SubPaneID: child.ID})
	state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant})
	for _, id := range []string{"one", "two"} {
		state.Messages.AppendMessage(workspace.Message{InternalID: id, Role: ctypes.RoleTool, ToolName: "read_file", ToolArgs: id + ".go", ToolStatus: ctypes.ToolStatusSuccess})
	}
	app.syncAgentMessages("root", false)
	for _, width := range []int{160, 80, 160} {
		app.width = width
		app.paintShellFrame()
		childY, activityY := -1, -1
		for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
			row := shellBufferRow(app, y)
			if strings.Contains(row, "Finished child") {
				childY = y
			}
			if strings.Contains(row, "Read 2 files") {
				activityY = y
			}
		}
		if childY < 0 || activityY != childY+2 {
			t.Fatalf("width %d: pane/activity gap is not one painted row: child=%d activity=%d\n%s", width, childY, activityY, bufferPlain(app.mainSurf.Buffer()))
		}
	}
}

// Real session snapshots omit empty assistant envelopes. The live conversation
// still has them between provider calls; both must paint the same joined group
// and permit a click on a redacted command leaf to reveal the full command.
func TestActivityTreeCommandClickAndEmptyEnvelopeRecovery(t *testing.T) {
	const command = "echo alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega"
	conv := workspace.NewConversation()
	conv.AppendMessage(workspace.Message{InternalID: "reply:progress", Role: "assistant", Content: "Checking.", Time: time.Unix(1, 0)})
	conv.AppendMessage(workspace.Message{InternalID: "empty", Role: "assistant", Time: time.Unix(2, 0)})
	conv.AppendMessage(workspace.Message{InternalID: "read", Role: "tool", ToolName: "read_file", ToolArgs: "one.go", ToolStatus: "success", Content: "file", Time: time.Unix(3, 0)})
	conv.AppendMessage(workspace.Message{InternalID: "command", Role: "tool", ToolName: "execute_command", ToolCommand: command, ToolStatus: "success", Content: "PASS", Time: time.Unix(4, 0)})
	live, _ := chatMessagesFromConversation(conv)

	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "root"); err != nil {
		t.Fatal(err)
	}
	wire := []provider.ChatMessage{
		{Role: "tool", ToolCallID: "progress", Content: provider.TextContent("Checking.")},
		{Role: "assistant"},
		{Role: "tool", ToolCallID: "read", Content: provider.TextContent("file")},
		{Role: "tool", ToolCallID: "command", Content: provider.TextContent("PASS")},
	}
	metas := []sessiondb.DisplayMeta{
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{InternalID: "reply:progress", Role: "assistant", Content: "Checking.", Time: time.Unix(1, 0)}}},
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion},
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{InternalID: "read", Role: "tool", ToolName: "read_file", ToolArgs: "one.go", ToolStatus: "success", Content: "file", Time: time.Unix(3, 0)}}},
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{InternalID: "command", Role: "tool", ToolName: "execute_command", ToolCommand: command, ToolStatus: "success", Content: "PASS", Time: time.Unix(4, 0)}}},
	}
	if _, err := sessiondb.ReplaceMessagesWithMeta(db, "root", wire, []int{1, 1, 1, 1}, metas); err != nil {
		t.Fatal(err)
	}
	loaded, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	recovered := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(loaded))
	left, right := chatprojection.NewRenderer(theme.DefaultDark()), chatprojection.NewRenderer(theme.DefaultDark())
	for _, r := range []*chatprojection.Renderer{left, right} {
		r.SetSize(42, 28)
		r.SetChatAreaBounds(0, 26)
	}
	left.SyncMessages(live, 1)
	right.SyncMessages(recovered, 1)
	check := func(stage string) []chatprojection.MessageRow {
		t.Helper()
		l, _, _ := left.ExportMessageRows(42)
		r, _, _ := right.ExportMessageRows(42)
		if len(l) != 2 || len(r) != 2 || l[1].ID != "activity:read" || r[1].ID != l[1].ID {
			t.Fatalf("%s: live/recovered shape: %v / %v", stage, l, r)
		}
		assertSameRows(t, stage, l[1], r[1])
		return l
	}
	rows := check("collapsed")
	if len(rows[1].LeafToggles) < 8 || strings.Contains(strings.Join(rows[1].Lines, "\n"), "more command lines") || !strings.Contains(strings.Join(rows[1].Lines, "\n"), "omega") {
		t.Fatalf("command preview missing: %v", rows[1])
	}
	th := theme.DefaultDark()
	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 42, PadLR: th.Spacing.PadLR, Theme: th}})
	contentH := len(rows[0].Lines) + len(rows[1].Lines)
	screen.SyncScrollMetrics(contentH, contentH)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 42 + th.Spacing.PadLR*2, H: contentH})
	leafY := len(rows[0].Lines) + 3
	ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 2, LocalY: leafY}
	_ = screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
	click := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
	if click.Action == nil || click.Action.ID != chatprojection.ChatToggleActionID || click.Action.Payload.(chatprojection.ChatTogglePayload).MessageID != "command" {
		t.Fatalf("command leaf click missed its target: %+v", click.Action)
	}
	left.ToggleDisclosure("command")
	right.ToggleDisclosure("command")
	rows = check("leaf expanded")
	if !strings.Contains(strings.Join(rows[1].Lines, "\n"), "omega") || strings.Contains(strings.Join(rows[1].Lines, "\n"), "more command lines") {
		t.Fatalf("expanded leaf still redacted: %v", rows[1].Lines)
	}
	left.ToggleDisclosure("activity:read")
	right.ToggleDisclosure("activity:read")
	l, _, _ := left.ExportMessageRows(42)
	r, _, _ := right.ExportMessageRows(42)
	if len(l) != 2 || len(r) != 2 || l[1].ID != "activity:read" || r[1].ID != "activity:read" {
		t.Fatalf("expanded audit trail differs: %v / %v", l, r)
	}
	assertSameRows(t, "audit child", l[1], r[1])
}

func TestCompactToolStyleAndBlinkUseSharedTitles(t *testing.T) {
	reg := newSubPaneProjectionRegistry(theme.DefaultDark())
	sp := workspace.NewSubPane("child", workspace.PaneAgent, "child")
	p := reg.Get(sp.ID, sp.Type)
	messages := []ctypes.ChatMessage{{InternalID: "tool", Role: ctypes.RoleTool, ToolName: "read_file", ToolArgs: "main.go", ToolStatus: ctypes.ToolStatusCalling, ToolPreviewPhase: ctypes.ToolPreviewStreaming}}
	p.renderer.SyncMessages(messages, 1)
	reg.renderAgentCompact(sp.ID, sp, sp.View(), 48, 6, 0, false)
	if !strings.Contains(p.compactToolLines[0].PlainText(), "●") {
		t.Fatal("active compact tool has no shared activity dot")
	}
	reg.renderAgentCompact(sp.ID, sp, sp.View(), 48, 6, ui.ActivityBlinkPhaseMillis, false)
	if !strings.Contains(p.compactToolLines[0].PlainText(), "○") {
		t.Fatal("compact cache froze the active dot")
	}
	if p.compactToolLines[0].Native.Spans[2].Role != ui.ColorToolTitle || p.compactToolLines[0].Native.Spans[3].Role != ui.ColorToolOperand {
		t.Fatal("compact list bypassed semantic title roles")
	}
	messages[0].ToolStatus = ctypes.ToolStatusSuccess
	messages[0].ToolPreviewPhase = ctypes.ToolPreviewDone
	p.renderer.SyncMessages(messages, 2)
	reg.renderAgentCompact(sp.ID, sp, sp.View(), 48, 6, 0, false)
	cached := &p.compactToolLines[0]
	reg.renderAgentCompact(sp.ID, sp, sp.View(), 48, 6, ui.ActivityBlinkPhaseMillis, false)
	if &p.compactToolLines[0] != cached {
		t.Fatal("completed compact tools recomputed on animation tick")
	}
}
