package shellchat_test

import (
	"strings"
	"testing"
	"time"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// TestShellchatPaintsMarkdownWithoutANSIPath drives a markdown assistant
// message through the shellchat Screen paint pipeline and asserts the painted
// buffer contains no ANSI escape runes or hyperlink metadata.
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
		Content: "# Title\n\nSome **bold** and `code`.\n\n[Guide](https://example.test/docs)\n\n| Item | Status |\n| --- | --- |\n| [Library](https://example.test/library) | Ready |\n\n```go\nfunc f() { x := 1 }\n```",
		Time:    time.Now(),
	})
	rows, paneMap, _ := r.ExportMessageRows(80)

	for _, row := range rows {
		for _, pl := range row.Native {
			if text := pl.PlainText(); strings.Contains(text, "id=") || strings.Contains(text, "]8;") {
				t.Fatalf("terminal hyperlink metadata reached a product chat row: %q", text)
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
	var painted strings.Builder
	for y := 0; y < 24; y++ {
		for x := 0; x < 80; x++ {
			c := buf.Get(x, y)
			painted.WriteRune(c.Char)
			if strings.ContainsRune(string(c.Char), '\x1b') {
				t.Fatalf("ANSI escape at (%d,%d) char=%q", x, y, string(c.Char))
			}
		}
	}
	if !strings.Contains(painted.String(), "Ready") || strings.Contains(painted.String(), "]8;") {
		t.Fatal("table content was clipped or hyperlink metadata was painted")
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
	// File mutations paint their tinted diff body by default.
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
