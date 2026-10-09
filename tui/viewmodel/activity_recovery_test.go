package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/ui"
	ctypes "crude/types"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

// Exercise the live agent event entry point against a real persisted DB load,
// not a second renderer initialized with the same in-memory messages.
func TestActivityReadStormSurvivesDBRecoveryAndDisclosure(t *testing.T) {
	state := workspace.NewAgentState("root", nil, nil)
	event := func(typ string, data any) {
		t.Helper()
		state.ApplyEvent(workspace.AgentEvent{Type: typ, Data: data})
	}
	for i, path := range []string{"first.go", "second.go"} {
		id := []string{"read-one", "read-two"}[i]
		internal := "persisted-" + id
		event("tool_call", map[string]any{"tool_id": id, "internal_id": internal, "name": "read_file", "args": `{"path":"` + path + `"}`})
		event("tool_result", map[string]any{"tool_id": id, "internal_id": internal, "name": "read_file", "result": "package fixture"})
	}
	for i := 0; i < 8; i++ {
		if !state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second)) {
			break
		}
	}
	event("tool_result", map[string]any{"tool_id": "progress", "name": "reply", "result": "Inspecting the result."})
	live, _ := chatMessagesFromConversation(state.Messages)
	if len(live) != 3 {
		t.Fatalf("live message count = %d, want 3", len(live))
	}

	db := sessiondb.OpenAndMigrateAt(t.TempDir())
	defer db.Close()
	if err := sessiondb.CreateSessionRow(db, "root"); err != nil {
		t.Fatal(err)
	}
	wire := []provider.ChatMessage{
		{Role: "tool", ToolCallID: "read-one", Content: provider.TextContent("package fixture")},
		{Role: "tool", ToolCallID: "read-two", Content: provider.TextContent("package fixture")},
		{Role: "tool", ToolCallID: "progress", Content: provider.TextContent("Inspecting the result.")},
	}
	metas := []sessiondb.DisplayMeta{
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{InternalID: "persisted-read-one", Role: "tool", ToolID: "read-one", ToolName: "read_file", ToolArgs: `path: first.go`, ToolStatus: "success", ToolPreviewPhase: "done", Content: "package fixture", ToolDuration: live[0].ToolDuration}}},
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{InternalID: "persisted-read-two", Role: "tool", ToolID: "read-two", ToolName: "read_file", ToolArgs: `path: second.go`, ToolStatus: "success", ToolPreviewPhase: "done", Content: "package fixture", ToolDuration: live[1].ToolDuration}}},
		{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{InternalID: "reply:progress", Role: "assistant", Content: "Inspecting the result."}}},
	}
	if _, err := sessiondb.ReplaceMessagesWithMeta(db, "root", wire, []int{1, 1, 1}, metas); err != nil {
		t.Fatal(err)
	}
	loaded, err := sessiondb.LoadMessagesWithMeta(db)
	if err != nil {
		t.Fatal(err)
	}
	recovered := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(loaded))
	liveRenderer, recoveredRenderer := newParityRenderer(), newParityRenderer()
	liveRenderer.SyncMessages(live, 1)
	recoveredRenderer.SyncMessages(recovered, 1)
	assert := func(label string, size int) {
		t.Helper()
		left, _, _ := liveRenderer.ExportMessageRows(200)
		right, _, _ := recoveredRenderer.ExportMessageRows(200)
		if len(left) != size || len(right) != size {
			t.Fatalf("%s: rows live=%d recovered=%d, want %d", label, len(left), len(right), size)
		}
		for i := range left {
			if left[i].ID != right[i].ID || left[i].Toggle != right[i].Toggle {
				t.Fatalf("%s: row identity/disclosure differs at %d: %q/%q", label, i, left[i].ID, right[i].ID)
			}
			if i == size-1 { // Assistant timestamps come from different clocks.
				continue
			}
			assertSameRows(t, label, left[i], right[i])
		}
	}
	assert("collapsed", 2)
	rows, _, _ := liveRenderer.ExportMessageRows(200)
	// Shellchat must route the actual grouped-row click to the renderer's
	// disclosure action; rendering a triangle alone is not auditability.
	th := theme.DefaultDark()
	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 200, PadLR: th.Spacing.PadLR, Theme: th}})
	contentH := 0
	for _, row := range rows {
		contentH += len(row.Lines)
	}
	screen.SyncScrollMetrics(contentH, contentH)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 200 + th.Spacing.PadLR*2, H: contentH})
	ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 3, LocalY: rows[0].ToggleLine}
	_ = screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
	click := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
	if click.Action == nil || click.Action.ID != chatprojection.ChatToggleActionID || click.Action.Payload.(chatprojection.ChatTogglePayload).MessageID != rows[0].ID {
		t.Fatalf("group disclosure click did not target its audit trail: %+v", click.Action)
	}
	if rows[0].ID != "activity:persisted-read-one" || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Read 2 files") || rows[1].ID != "reply:progress" {
		t.Fatalf("recovered activity contract: %+v", rows)
	}
	liveRenderer.ToggleDisclosure(rows[0].ID)
	recoveredRenderer.ToggleDisclosure(rows[0].ID)
	assert("expanded", 2)
	rows, _, _ = liveRenderer.ExportMessageRows(200)
	if rows[0].LeafToggles[2] != "persisted-read-one" || rows[0].LeafToggles[3] != "persisted-read-two" {
		t.Fatalf("expanded audit trail: %+v", rows)
	}
	liveRenderer.ToggleDisclosure("persisted-read-one")
	recoveredRenderer.ToggleDisclosure("persisted-read-one")
	assert("file detail", 2)
	rows, _, _ = liveRenderer.ExportMessageRows(200)
	if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "package fixture") {
		t.Fatalf("expanded read result missing: %+v", rows[0])
	}
}
