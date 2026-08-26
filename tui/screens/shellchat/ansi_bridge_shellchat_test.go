package shellchat_test

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

// TestShellchatPaintsMarkdownWithoutANSIPath drives a markdown assistant
// message through the shellchat Screen paint pipeline and asserts:
// 1. The painted buffer contains no ANSI escape runes.
// 2. No message row requests the ANSI path (all native rows PreferANSI==false).
func TestShellchatPaintsMarkdownWithoutANSIPath(t *testing.T) {
	th := theme.DefaultDark()
	screen := shellchat.New(th)
	tokens := ui.NewTokenSet(th)

	r := chatprojection.NewRenderer(th)
	r.SetSize(80, 24)
	r.SetChatAreaBounds(0, 24)
	r.SetStreaming(false)
	r.AppendMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: "# Title\n\nSome **bold** and `code`.\n\n```go\nfunc f() { x := 1 }\n```",
		Time:    time.Now(),
	})
	rows, paneMap, _ := r.ExportMessageRows(80)

	// Assert every native row is non-PreferANSI before painting.
	for ri, row := range rows {
		for i, pl := range row.Native {
			if pl.PreferANSI() {
				t.Fatalf("row %d native[%d] PreferANSI — ANSI path must be gone", ri, i)
			}
		}
	}

	compose := shellchat.ComposeParams{
		InnerW:      80,
		PadLR:       th.Spacing.PadLR,
		Theme:       th,
		PaneLineMap: paneMap,
	}
	screen.SetMessages(rows, shellchat.PaintParams{Compose: compose, PaneLineMap: paneMap})
	screen.SetTokens(tokens)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 80, H: 24})

	buf := screen.PaintTestBuffer(80, 24)
	if buf == nil {
		t.Fatal("nil paint buffer")
	}
	for y := 0; y < 24; y++ {
		for x := 0; x < 80; x++ {
			c := buf.Get(x, y)
			if strings.ContainsRune(string(c.Char), '\x1b') {
				t.Fatalf("ANSI escape at (%d,%d) char=%q", x, y, string(c.Char))
			}
		}
	}
}

// TestShellchatPaintsDiffToolBodyNative renders a diff tool message through the
// screen and asserts the painted cells carry the diff addition/deletion colours.
func TestShellchatPaintsDiffToolBodyNative(t *testing.T) {
	th := theme.DefaultDark()
	tokens := ui.NewTokenSet(th)
	addCell := tokens.Color(ui.ColorDiffAddition)
	delCell := tokens.Color(ui.ColorDiffDeletion)

	r := chatprojection.NewRenderer(th)
	r.SetSize(80, 24)
	r.SetChatAreaBounds(0, 24)
	r.SetStreaming(false)
	diff := "```diff\n--- a/main.go\n+++ b/main.go\n@@ -1,2 +1,2 @@\n-func old()\n+func new()\n```"
	r.AppendMessage(types.ChatMessage{
		InternalID: "edit-1",
		Role:       types.RoleTool,
		ToolName:   "edit_file",
		ToolArgs:   "main.go",
		Content:    "Edited main.go\n\n" + diff,
		ToolStatus: "success",
		Time:       time.Now(),
	})
	// Edit/write blocks collapse to title-only by default; expand so the
	// tinted diff body is painted.
	r.ToggleDisclosure("edit-1")
	rows, paneMap, _ := r.ExportMessageRows(80)

	compose := shellchat.ComposeParams{
		InnerW:      80,
		PadLR:       th.Spacing.PadLR,
		Theme:       th,
		PaneLineMap: paneMap,
	}
	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{Compose: compose, PaneLineMap: paneMap})
	screen.SetTokens(tokens)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 80, H: 24})
	buf := screen.PaintTestBuffer(80, 24)

	foundAdd := false
	foundDel := false
	for y := 0; y < 24; y++ {
		for x := 0; x < 80; x++ {
			c := buf.Get(x, y)
			if c.FG.Equal(addCell) {
				foundAdd = true
			}
			if c.FG.Equal(delCell) {
				foundDel = true
			}
		}
	}
	if !foundAdd {
		t.Fatal("no cell painted with diff addition colour")
	}
	if !foundDel {
		t.Fatal("no cell painted with diff deletion colour")
	}
}
