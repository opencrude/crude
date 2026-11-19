package chatprojection_test

import (
	"strings"
	"testing"

	"crude/tui/bidi"
	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Inline code in a Hebrew sentence keeps its own order: `./run.sh --all`
// must not lose its "./" to the far side of the code, as plain UAX #9 does
// with neutral characters between right-to-left words.
func TestInlineCodeKeepsItsOrderInRightToLeftChat(t *testing.T) {
	defer bidi.SetPresentation(bidi.Current())
	bidi.SetPresentation(bidi.Presentation{Arrange: true})
	th := theme.DefaultDark()
	rows := renderAssistantLines(t, "\u05d4\u05e8\u05e5 `./run.sh --all` \u05e2\u05db\u05e9\u05d9\u05d5", th, 60)
	tokens := ui.NewTokenSet(th)
	for _, row := range rows {
		if !strings.Contains(row.PlainText(), "run.sh") {
			continue
		}
		buf := surface.NewCellBuffer(60, 1)
		chatprojection.PaintLineAtBackground(buf, 0, 0, 60, row, tokens, 0, 0, cell.NoColor)
		var shown strings.Builder
		for _, c := range buf.Row(0) {
			switch {
			case c.Width == 0:
			case c.Content != "":
				shown.WriteString(c.Content)
			case c.Char == 0:
				shown.WriteByte(' ')
			default:
				shown.WriteRune(c.Char)
			}
		}
		if got := strings.TrimSpace(shown.String()); !strings.Contains(got, "./run.sh --all") {
			t.Fatalf("inline code lost its order: %q", got)
		}
		return
	}
	t.Fatal("no row shows the inline code")
}
