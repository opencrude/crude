package shellchat

import (
	"strings"
	"testing"

	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// Compare visible block edges, not an internal width constant. The shell may
// have additional columns for chrome; those are not part of the document.
func TestMarkdownDocumentReadingEdges(t *testing.T) {
	const body = "English paragraph.\n\n---\n\n## שלום Header\n\nשלום Body"
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SyncMessages([]types.ChatMessage{{InternalID: "edges", Role: types.RoleAssistant, Content: body}}, 1, 1)
	s := New(th)
	host := ui.NewHost(th, s)
	for _, width := range []int{100, 45, 100} {
		buf := paintMarkdownHost(r, s, host, th, width)
		ey, english := findMarkdownHostLine(t, buf, "English paragraph.")
		// The header rule also contains horizontal lines. The document's
		// thematic break is the first such line *after* the English prose.
		var left, right int
		for y := ey + 1; y < buf.Height(); y++ {
			for x := 0; x < buf.Width(); x++ {
				if buf.Get(x, y).Char == '─' {
					if right == 0 {
						left = x
					}
					right = x + 1
				}
			}
			if right > 0 {
				break
			}
		}
		if right == 0 || strings.Index(english, "English") != left {
			t.Fatalf("width %d: English prose and rule do not share a left edge", width)
		}
		for _, label := range []string{"Body", "Header"} {
			_, row := findMarkdownHostLine(t, buf, label)
			if edge := rendering.VisibleWidth(strings.TrimRight(row, " ")); edge != right {
				t.Errorf("width %d: %s ends at %d; rule ends at %d", width, label, edge, right)
			}
		}
	}
}

func TestStreamingRTLParagraphKeepsOrderAndEdgeWhenSettled(t *testing.T) {
	const body = "---\n\nשלום abcdefghijklm ! Example"
	th := theme.DefaultDark()
	s := New(th)
	host := ui.NewHost(th, s)
	var first, continuation string
	for _, live := range []bool{true, false} {
		r := chatprojection.NewRenderer(th)
		msg := types.ChatMessage{InternalID: "wrap", Role: types.RoleAssistant, Content: body}
		r.AppendMessage(msg)
		r.SetStreaming(live)
		buf := paintMarkdownHost(r, s, host, th, 24)
		_, row := findMarkdownHostLine(t, buf, "abcdefghijklm")
		_, next := findMarkdownHostLine(t, buf, "Example")
		if !strings.Contains(row, "abcdefghijklm ! םולש") {
			t.Errorf("live=%v: neutral punctuation lost the following LTR context: %q", live, row)
		}
		if live {
			if !strings.Contains(next, "█Example") {
				t.Errorf("the live cursor is not beside the RTL paragraph's trailing text: %q", next)
			}
			first, continuation = row, strings.ReplaceAll(next, "█", " ")
		} else if first != row || continuation != next {
			t.Errorf("settling a plain paragraph changed its painted text:\n%q\n%q\nbecame\n%q\n%q", first, continuation, row, next)
		}
	}
}

func TestTableHeaderIgnoresIsolatedInlineCode(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.AppendMessage(types.ChatMessage{InternalID: "code-header", Role: types.RoleAssistant,
		Content: "| `API` שלום | Version |\n| --- | --- |\n| First | Second |"})
	s := New(th)
	buf := paintMarkdownHost(r, s, ui.NewHost(th, s), th, 80)
	_, row := findMarkdownHostLine(t, buf, "First")
	if l, r := strings.Index(row, "Second"), strings.Index(row, "First"); l < 0 || r <= l {
		t.Fatalf("inline code determined the table direction: %q", row)
	}
}
