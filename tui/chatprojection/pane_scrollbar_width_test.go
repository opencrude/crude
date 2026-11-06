package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// A thinking pane whose body starts or stops overflowing gains or loses its
// own scrollbar. That column belongs to the pane rows: every other row must
// keep its width and its rendered lines, or the whole transcript reflows each
// time a pane comes and goes while the model thinks.
func TestPaneScrollbarDoesNotReflowTranscript(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(60, 20)
	r.SetChatAreaBounds(0, 20)
	ext := ui.NewScrollModel()
	r.BindScroll(ext)
	paneWidths := []int{}
	r.SetSubPaneContentFn(func(_ string, width, _, _ int, _ bool) []SubPaneLine {
		paneWidths = append(paneWidths, width)
		return []SubPaneLine{{Text: "thinking", PaneInfo: PaneLineInfo{PaneID: "pane-1"}}}
	})
	now := time.Unix(1, 0)
	messages := make([]types.ChatMessage, 0, 13)
	for i := range 12 {
		messages = append(messages, types.ChatMessage{
			InternalID: "m-" + string(rune('a'+i)),
			Role:       types.RoleAssistant,
			Content:    strings.Repeat("a sentence that wraps across the narrow chat column ", 3),
			Time:       now,
		})
	}
	messages = append(messages, types.ChatMessage{InternalID: "pane", Role: types.RoleThinking, SubPaneID: "pane-1", Time: now})
	r.SyncMessages(messages, 1, 0)
	before := r.ExportMessageRowsSnapshot(56)
	if r.ScrollbarWidth() != ui.ScrollbarWidth {
		t.Fatal("fixture must overflow the main transcript")
	}
	versions := map[string]uint64{}
	lines := map[string]string{}
	for _, row := range before.Rows {
		versions[row.ID], lines[row.ID] = row.Version, strings.Join(row.Lines, "\n")
	}
	widthWithout := paneWidths[len(paneWidths)-1]

	for _, paneScrollbar := range []int{ui.ScrollbarWidth, 0, ui.ScrollbarWidth} {
		if !r.SetPaneScrollbarWidth(paneScrollbar) {
			t.Fatalf("pane scrollbar %d was not applied", paneScrollbar)
		}
		after := r.ExportMessageRowsSnapshot(56)
		for _, row := range after.Rows {
			if row.ID == "pane" {
				continue
			}
			if row.Version != versions[row.ID] || strings.Join(row.Lines, "\n") != lines[row.ID] {
				t.Fatalf("pane scrollbar %d re-rendered transcript row %s", paneScrollbar, row.ID)
			}
		}
		if after.ContentHeight != before.ContentHeight {
			t.Fatalf("pane scrollbar %d changed transcript height %d -> %d", paneScrollbar, before.ContentHeight, after.ContentHeight)
		}
		want := widthWithout - paneScrollbar
		if got := paneWidths[len(paneWidths)-1]; got != want {
			t.Fatalf("pane scrollbar %d: pane body width %d, want %d", paneScrollbar, got, want)
		}
	}
}

// Providers stream separator newlines between output items. A hydrated reply
// made only of them is not communication: it must neither paint a blank row
// nor split the reads around it out of their activity group.
func TestWhitespaceReplyDoesNotSplitActivity(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(100, 30)
	r.SetChatAreaBounds(0, 30)
	read := func(id string) types.ChatMessage {
		return types.ChatMessage{InternalID: id, Role: types.RoleTool, ToolName: "read_file", ToolArgs: id + ".go",
			ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone, Content: "package fixture"}
	}
	r.SyncMessages([]types.ChatMessage{
		read("a"),
		{InternalID: "gap", Role: types.RoleAssistant, Content: "\n\n"},
		read("b"),
	}, 1, 0)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || !strings.HasPrefix(rows[0].ID, "activity:") {
		var text []string
		for _, row := range rows {
			text = append(text, row.ID+": "+strings.Join(row.Lines, " / "))
		}
		t.Fatalf("whitespace reply split the reads:\n%s", strings.Join(text, "\n"))
	}
	if got := strings.Join(rows[0].Lines, "\n"); !strings.Contains(got, "Read 2 files") {
		t.Fatalf("reads were not summarized together: %q", got)
	}
}
