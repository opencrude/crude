package chatprojection

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func testRendererWithDualScroll(t *testing.T) (*Renderer, *ui.ScrollModel, *ui.ScrollModel) {
	t.Helper()
	r := NewRenderer(theme.DefaultDark())
	ext := ui.NewScrollModel()
	pane := ui.NewScrollModel()
	pane.SetAutoScroll(false)
	pane.ScrollTo(3)
	r.BindScroll(ext)
	r.BindPaneScroll(pane)
	r.SetChatAreaBounds(0, 4)
	return r, ext, pane
}

func seedScrollableMessages(r *Renderer, count int) {
	now := time.Unix(1, 0)
	messages := make([]types.ChatMessage, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, types.ChatMessage{
			InternalID: "m-" + string(rune('a'+i)),
			Role:       types.RoleAssistant,
			Content:    strings.Repeat("line\n", 6),
			Time:       now,
		})
	}
	r.SyncMessages(messages, 1)
}

func TestRendererMainExportMutatesExtScrollOnly(t *testing.T) {
	r, ext, pane := testRendererWithDualScroll(t)
	seedScrollableMessages(r, 8)
	paneBeforeAuto := pane.AutoScroll()
	paneBeforeOff := pane.Offset()

	r.ScrollToBottom()

	if !ext.AutoScroll() {
		t.Fatal("main ScrollToBottom should arm extScroll")
	}
	if ext.Offset() != ext.MaxOffset() {
		t.Fatalf("extScroll offset = %d, want max %d", ext.Offset(), ext.MaxOffset())
	}
	if pane.AutoScroll() != paneBeforeAuto {
		t.Fatal("ScrollToBottom must not change paneScroll auto flag")
	}
	if pane.Offset() != paneBeforeOff {
		t.Fatalf("ScrollToBottom must not move paneScroll: got %d want %d", pane.Offset(), paneBeforeOff)
	}
}

func TestRendererScrollbarWidthUsesPresentationScrollOwner(t *testing.T) {
	r, ext, pane := testRendererWithDualScroll(t)
	r.SetPaneScrollbarWidth(2)
	pane.SetContent(50)
	pane.SetViewport(3)
	ext.SetContent(3)
	ext.SetViewport(10)
	r.vl.SetViewport(10)
	if got := r.ScrollbarWidth(); got != 2 {
		t.Fatalf("compact overflow reserved main gutter: width=%d want 2", got)
	}
	ext.SetContent(50)
	pane.SetContent(0)
	seedScrollableMessages(r, 8)
	r.vl.EnsureHeights(r.renderItem, 40)
	if got := r.ScrollbarWidth(); got != 2+ui.ScrollbarWidth {
		t.Fatalf("main overflow omitted main gutter: width=%d", got)
	}
	r.BindScroll(nil)
	if got := r.ScrollbarWidth(); got != 2 {
		t.Fatalf("pane-only fitting content reserved gutter: width=%d", got)
	}
	pane.SetContent(50)
	if got := r.ScrollbarWidth(); got != 2+ui.ScrollbarWidth {
		t.Fatalf("pane-only overflow omitted gutter: width=%d", got)
	}
}

func TestFirstSnapshotSettlesNewScrollbarWithoutIdleReflow(t *testing.T) {
	r, ext, _ := testRendererWithDualScroll(t)
	ext.SetAutoScroll(true)
	r.SetSize(70, 24)
	seedScrollableMessages(r, 8)
	first := r.ExportMessageRowsSnapshot(66)
	r.SetSpinFrame(400)
	second := r.ExportMessageRowsSnapshot(66)
	if second.Generation != first.Generation {
		t.Fatal("first idle export invalidated the just-published scrollbar width")
	}
	for _, msg := range r.Messages() {
		if msg.CachedLines == nil {
			t.Fatal("settled snapshot discarded unchanged message cache")
		}
	}
}

func TestRendererDisarmedPanePreservedDuringMainStreaming(t *testing.T) {
	r, ext, pane := testRendererWithDualScroll(t)
	seedScrollableMessages(r, 6)
	pane.SetAutoScroll(false)
	pane.ScrollTo(2)
	paneBefore := pane.Offset()

	ext.SetAutoScroll(true)
	ext.StickToBottom()
	r.SetStreaming(true)
	r.AppendToken("streaming tail\nmore")

	_, _, _ = r.ExportMessageRows(40)
	if pane.Offset() != paneBefore {
		t.Fatalf("main streaming moved paneScroll: got %d want %d", pane.Offset(), paneBefore)
	}
	if pane.AutoScroll() {
		t.Fatal("main streaming re-armed disarmed paneScroll")
	}
	if !ext.AutoScroll() {
		t.Fatal("main streaming should keep extScroll armed")
	}
}

func TestRendererPaneOnlyAppendSticksWhenArmed(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	pane := ui.NewScrollModel()
	pane.SetAutoScroll(true)
	r.BindPaneScroll(pane)
	r.SetChatAreaBounds(0, 3)
	seedScrollableMessages(r, 5)

	r.AppendMessage(types.ChatMessage{
		InternalID: "new",
		Role:       types.RoleAssistant,
		Content:    strings.Repeat("x\n", 8),
		Time:       time.Unix(2, 0),
	})
	_ = r.Render(40)

	if !pane.AutoScroll() {
		t.Fatal("armed pane scroll should stay armed after append")
	}
	if pane.Offset() != pane.MaxOffset() {
		t.Fatalf("armed pane append should stick to bottom: offset=%d max=%d", pane.Offset(), pane.MaxOffset())
	}
}

func TestRendererPaneOnlyDisarmedPreservesOffsetOnAppend(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	pane := ui.NewScrollModel()
	pane.SetAutoScroll(false)
	r.BindPaneScroll(pane)
	r.SetChatAreaBounds(0, 3)
	seedScrollableMessages(r, 5)
	_ = r.Render(40)
	pane.ScrollTo(1)
	before := pane.Offset()

	r.AppendMessage(types.ChatMessage{
		InternalID: "new",
		Role:       types.RoleAssistant,
		Content:    strings.Repeat("y\n", 8),
		Time:       time.Unix(2, 0),
	})
	_ = r.Render(40)

	if pane.AutoScroll() {
		t.Fatal("disarmed pane should stay disarmed after append")
	}
	if pane.Offset() != before {
		t.Fatalf("disarmed pane offset changed on append: got %d want %d", pane.Offset(), before)
	}
}

func TestSubPaneLeadingSpacerIsNotPaneOwned(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 4)
	r.SetSubPaneContentFn(func(_ string, _, _, _ int, _ bool) []SubPaneLine {
		return []SubPaneLine{
			NativeLine(PaintLine{Spans: []RowSpan{{Text: "Thought for 2s", Role: ui.ColorMuted}}}),
		}
	})
	r.AppendMessage(types.ChatMessage{
		InternalID: "thought-pane",
		Role:       types.RoleTool,
		SubPaneID:  "thinking-1",
	})

	rows, paneMap, contentH := r.ExportMessageRows(40)
	if len(rows) != 1 || len(rows[0].Lines) != 2 || contentH != 2 {
		t.Fatalf("collapsed pane rows/content = %+v / %d", rows, contentH)
	}
	if rows[0].Lines[0] != "" || rows[0].Lines[1] != "Thought for 2s" {
		t.Fatalf("collapsed pane lines = %#v", rows[0].Lines)
	}
	if len(paneMap) != 2 || paneMap[1].PaneID != "thinking-1" || paneMap[0].PaneID != "" {
		t.Fatalf("leading spacer must not inherit pane ownership: %+v", paneMap)
	}
}

func TestCollapsedThinkingThenToolIsSingleGap(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(60, 24)
	r.SetChatAreaBounds(0, 6)
	r.SetSubPaneContentFn(func(_ string, _, _, _ int, _ bool) []SubPaneLine {
		return []SubPaneLine{
			NativeLine(PaintLine{Spans: []RowSpan{{Text: "Thought for 2s", Role: ui.ColorMuted}}}),
		}
	})
	r.AppendMessage(types.ChatMessage{
		InternalID: "thought-pane",
		Role:       types.RoleTool,
		SubPaneID:  "thinking-1",
	})
	r.AppendMessage(types.ChatMessage{
		InternalID: "tool-1",
		Role:       types.RoleTool,
		ToolName:   "search_history",
		Content:    "Search History settings deck restore",
	})

	rows, _, _ := r.ExportMessageRows(60)
	if len(rows) != 2 {
		t.Fatalf("expected 2 message rows, got %d", len(rows))
	}
	if len(rows[0].Lines) != 2 || rows[0].Lines[1] == "" {
		t.Fatalf("collapsed pane should end on content: %#v", rows[0].Lines)
	}
	if len(rows[1].Lines) < 2 || rows[1].Lines[0] != "" {
		t.Fatalf("tool must own the single inter-item gap, got %#v", rows[1].Lines)
	}
	if !strings.Contains(rows[1].Lines[1], "Searching history") {
		t.Fatalf("tool content missing after spacer: %q", rows[1].Lines[1])
	}
}

func TestPaneThenActivityHasExactlyOneGap(t *testing.T) {
	r := newActivityRenderer()
	r.SetSubPaneContentFn(func(_ string, _, _, _ int, _ bool) []SubPaneLine {
		return []SubPaneLine{NativeLine(buildPlainBodyLine("Finished child"))}
	})
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "child", Role: types.RoleTool, SubPaneID: "child-pane"},
		{InternalID: "empty", Role: types.RoleAssistant},
		{InternalID: "one", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "one.go", ToolStatus: types.ToolStatusSuccess},
		{InternalID: "two", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "two.go", ToolStatus: types.ToolStatusSuccess},
	}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	var lines []string
	for _, row := range rows {
		lines = append(lines, row.Lines...)
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" && strings.TrimSpace(lines[i-1]) == "" {
			t.Fatalf("pane/activity boundary painted duplicate gaps:\n%q", lines)
		}
	}
}

func TestExportMessageRowsRendersEveryDirtyItem(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(48, 3)
	r.SetChatAreaBounds(0, 3)
	for i := 0; i < 10; i++ {
		r.AppendMessage(types.ChatMessage{
			InternalID: fmt.Sprintf("message-%d", i),
			Role:       types.RoleAssistant,
			Content:    fmt.Sprintf("content-%d", i),
		})
	}

	rows, _, _ := r.ExportMessageRows(48)
	if len(rows) != 10 {
		t.Fatalf("exported rows=%d want 10", len(rows))
	}
	for i, row := range rows {
		if got := strings.Join(row.Lines, "\n"); !strings.Contains(got, fmt.Sprintf("content-%d", i)) {
			t.Fatalf("row %d exported stale/blank content: %q", i, got)
		}
	}
}

func TestLaterToolRemovesCursorFromPreviousAssistant(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(60, 8)
	r.SetChatAreaBounds(0, 8)
	r.SetStreaming(true)
	r.SetActiveTyping(true)
	r.AppendMessage(types.ChatMessage{
		InternalID: "assistant",
		Role:       types.RoleAssistant,
		Content:    "I will inspect the current implementation.",
	})

	rows, _, _ := r.ExportMessageRows(60)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "█") {
		t.Fatalf("active assistant lacks cursor: %+v", rows)
	}

	r.UpdateToolCall("tool-1", "tool-message", "read_file", "main.go")
	rows, _, _ = r.ExportMessageRows(60)
	if len(rows) < 2 {
		t.Fatalf("tool append did not produce a later row: %+v", rows)
	}
	if got := strings.Join(rows[0].Lines, "\n"); strings.Contains(got, "█") {
		t.Fatalf("previous assistant retained typing cursor after later tool row: %q", got)
	}
}

func TestSyncAppendRemovesCursorFromPreviousAssistant(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(60, 8)
	r.SetChatAreaBounds(0, 8)
	r.SetStreaming(true)
	r.SetActiveTyping(true)
	r.SyncMessages([]types.ChatMessage{{
		InternalID: "assistant",
		Role:       types.RoleAssistant,
		Content:    "Working now.",
	}}, 1)
	_, _, _ = r.ExportMessageRows(60)

	r.SyncMessages([]types.ChatMessage{
		{InternalID: "assistant", Role: types.RoleAssistant, Content: "Working now."},
		{InternalID: "tool", Role: types.RoleTool, ToolName: "read_file", ToolStatus: "calling"},
	}, 2)
	rows, _, _ := r.ExportMessageRows(60)
	if got := strings.Join(rows[0].Lines, "\n"); strings.Contains(got, "█") {
		t.Fatalf("synced assistant retained typing cursor after append: %q", got)
	}
}
