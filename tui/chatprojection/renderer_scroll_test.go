package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
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

func TestCollapsedSubPaneSeparatorIsNotPaneOwned(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 4)
	r.SetSubPaneContentFn(func(_ string, _, _, _ int) []SubPaneLine {
		return []SubPaneLine{
			NativeLine(PaintLine{Spans: []RowSpan{{Text: "Thought for 2s", Role: ui.ColorMuted}}}),
			SeparatorLine(),
		}
	})
	r.AppendMessage(types.ChatMessage{
		InternalID: "thought-pane",
		Role:       types.RoleTool,
		SubPaneID:  "thinking-1",
	})

	rows, paneMap, contentH := r.ExportMessageRows(40)
	if len(rows) != 1 || len(rows[0].Lines) != 3 || contentH != 3 {
		t.Fatalf("collapsed pane rows/content = %+v / %d", rows, contentH)
	}
	if rows[0].Lines[0] != "" || rows[0].Lines[1] != "Thought for 2s" || rows[0].Lines[2] != "" {
		t.Fatalf("collapsed pane lines = %#v", rows[0].Lines)
	}
	if len(paneMap) != 3 || paneMap[1].PaneID != "thinking-1" || paneMap[0].PaneID != "" || paneMap[2].PaneID != "" {
		t.Fatalf("separator must not inherit pane ownership: %+v", paneMap)
	}
}

func TestCollapsedThinkingThenToolIsSingleGap(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetChatAreaBounds(0, 6)
	r.SetSubPaneContentFn(func(_ string, _, _, _ int) []SubPaneLine {
		return []SubPaneLine{
			NativeLine(PaintLine{Spans: []RowSpan{{Text: "Thought for 2s", Role: ui.ColorMuted}}}),
			SeparatorLine(),
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
	// rows[0] is the collapsed pane: leading blank, label, trailing separator
	// blank. That trailing blank is the ONE row separating it from the tool.
	if len(rows[0].Lines) != 3 || rows[0].Lines[2] != "" {
		t.Fatalf("collapsed pane should end on a separator blank: %#v", rows[0].Lines)
	}
	// The tool must start immediately after that separator — no extra leading
	// spacer, so exactly one blank row sits between the thought and the tool.
	if len(rows[1].Lines) == 0 || strings.TrimSpace(rows[1].Lines[0]) == "" {
		t.Fatalf("tool message should start right after the pane's separator, got %#v", rows[1].Lines)
	}
	if !strings.Contains(rows[1].Lines[0], "Search History") {
		t.Fatalf("tool first line should carry the tool content, got %q", rows[1].Lines[0])
	}
}
