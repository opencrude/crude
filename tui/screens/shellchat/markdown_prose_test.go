package shellchat

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
)

func TestMarkdownProseUsesParagraphLevelsAfterResize(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	body := "שלום abcdefghijklm ! Example"
	r.SyncMessages([]conversation.ChatMessage{{InternalID: "prose", Role: conversation.RoleAssistant, Content: body}}, 1, 1)
	s := New(th)
	host := ui.NewHost(th, s)
	for _, width := range []int{24, 54, 24} {
		buffer := paintMarkdownHost(r, s, host, th, width)
		y, line := findMarkdownHostLine(t, buffer, "abcdefghijklm")
		if want := "abcdefghijklm ! םולש"; !strings.Contains(line, want) && width == 24 {
			t.Errorf("width %d: wrapped row = %q, want %q", width, line, want)
		}
		lastY, _ := findMarkdownHostLine(t, buffer, "Example")
		if width == 24 && lastY <= y || width == 54 && lastY != y {
			t.Errorf("width %d: first row %d, Example row %d", width, y, lastY)
		}
	}
}

func TestMarkdownProseKeepsHeadingBadgeAndIsolatesListMarker(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		const body = "## שלום Header\n\n12. שלום List\n\n> שלום Quote\n>\n> ```go\n> x := \"שלום\"\n> ```\n\n# שלום Badge"
		r := chatprojection.NewRenderer(th)
		r.SyncMessages([]conversation.ChatMessage{{InternalID: "decorations", Role: conversation.RoleAssistant, Content: body}}, 1, 1)
		s := New(th)
		buffer := paintMarkdownHost(r, s, ui.NewHost(th, s), th, 66)
		for _, tt := range []struct{ word, want string }{
			{"Header", "Header םולש ##"},
			// A decimal marker at the RTL inline-start has its own RTL
			// isolate: the digits stay 12 and the neutral suffix is left.
			{"List", "List םולש .12"},
			{"Quote", "Quote םולש │"},
		} {
			_, got := findMarkdownHostLine(t, buffer, tt.word)
			if got = strings.TrimSpace(got); got != tt.want {
				t.Errorf("%s: got %q, want %q", tt.word, got, tt.want)
			}
		}
		y, badge := findMarkdownHostLine(t, buffer, "Badge")
		first := rendering.VisibleWidth(badge[:strings.Index(badge, "Badge")])
		last := first + rendering.VisibleWidth("Badge םולש") - 1
		ink := buffer.Get(first, y).BG
		if ink.IsDefault() || !buffer.Get(first-1, y).BG.Equal(ink) || !buffer.Get(last+1, y).BG.Equal(ink) {
			t.Errorf("heading badge lost decorated margins: %q", badge)
		}
	}
}

func TestMarkdownQuoteKeepsOneRailAcrossMixedContentAndResize(t *testing.T) {
	const body = "> `source` שלום Rail\n>\n> English (text)!\n>\n> > Inner left.\n>\n> שלום Middle\n>\n> > שלום Nested\n>\n> ```go\n> x := \"שלום\"\n> ```"
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		r := chatprojection.NewRenderer(th)
		r.SyncMessages([]conversation.ChatMessage{{InternalID: "quote", Role: conversation.RoleAssistant, Content: body}}, 1, 1)
		s := New(th)
		host := ui.NewHost(th, s)
		for _, width := range []int{32, 60, 32} {
			buffer := paintMarkdownHost(r, s, host, th, width)
			y, first := findMarkdownHostLine(t, buffer, "Rail")
			right := markdownColumn(t, first, "│", true)
			englishY, english := findMarkdownHostLine(t, buffer, "English (text)!")
			if right <= markdownColumn(t, first, "Rail", false) || markdownColumn(t, english, "│", true) != right {
				t.Errorf("width %d: RTL rail must stay right across directions: %q / %q", width, first, english)
			}
			if y+1 >= englishY || buffer.Get(right, y+1).Char != '│' {
				t.Errorf("width %d: quote-only blank row lost its rail", width)
			}
			_, inner := findMarkdownHostLine(t, buffer, "Inner left.")
			left := markdownColumn(t, inner, "│", false)
			if left >= markdownColumn(t, inner, "Inner", false) || markdownColumn(t, inner, "│", true) != right {
				t.Errorf("width %d: nested LTR quote did not keep both reading edges: %q", width, inner)
			}
			_, nested := findMarkdownHostLine(t, buffer, "Nested")
			if markdownColumn(t, nested, "│", false) != right-2 || markdownColumn(t, nested, "│", true) != right {
				t.Errorf("width %d: nested RTL quote did not keep its inset: %q", width, nested)
			}
			_, code := findMarkdownHostLine(t, buffer, `x := "םולש"`)
			if markdownColumn(t, code, "x :=", false) != markdownColumn(t, english, "English", false) ||
				markdownColumn(t, code, "│", true) != right {
				t.Errorf("width %d: fenced code lost its LTR grid or the enclosing RTL rail: %q", width, code)
			}
		}
	}
}

func TestMarkdownListsKeepMarkerSideAndHangingIndentAfterResize(t *testing.T) {
	const body = `12. שלום First 1234567890 אבג
13. English (item)!

    Loose paragraph.

    - שלום Nested
`
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		r := chatprojection.NewRenderer(th)
		r.SyncMessages([]conversation.ChatMessage{{InternalID: "list", Role: conversation.RoleAssistant, Content: body}}, 1, 1)
		s := New(th)
		host := ui.NewHost(th, s)
		for _, width := range []int{24, 54, 24} {
			buffer := paintMarkdownHost(r, s, host, th, width)
			firstY, first := findMarkdownHostLine(t, buffer, "First")
			xMarker := markdownColumn(t, first, ".12", false)
			wrapY, wrap := findMarkdownHostLine(t, buffer, "1234567890")
			if width == 24 && (wrapY <= firstY || rendering.VisibleWidth(strings.TrimRight(wrap, " ")) != xMarker-1) {
				t.Errorf("width %d: continuation crosses the hanging marker gutter: %q / %q", width, first, wrap)
			}
			y, english := findMarkdownHostLine(t, buffer, "English (item)!")
			if markdownColumn(t, english, ".13", false) != xMarker {
				t.Errorf("width %d: adjacent LTR item moved its marker: %q", width, english)
			}
			_, loose := findMarkdownHostLine(t, buffer, "Loose")
			x := markdownColumn(t, english, "English", false)
			if markdownColumn(t, loose, "Loose", false) != x || x >= xMarker {
				t.Errorf("width %d: LTR item and loose continuation differ: %q / %q", width, english, loose)
			}
			_, nested := findMarkdownHostLine(t, buffer, "Nested")
			if bullet := markdownColumn(t, nested, "•", false); bullet != xMarker-4 {
				t.Errorf("width %d: nested marker lost the parent gutter and nested indent: %q", width, nested)
			}
			if end := x + len("English (item)!"); end < xMarker {
				host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
				host.Handle(tea.MouseMotionMsg{X: end, Y: y, Button: tea.MouseLeft})
				if got := s.SelectedText(); got != "English (item)!" {
					t.Errorf("width %d: dragging the item text copied %q", width, got)
				}
				host.Paint(buffer)
				_, selection := ui.NewTokenSet(th).TextSelectionColors(cell.NoColor)
				if !buffer.Get(x+1, y).BG.Equal(selection) || buffer.Get(xMarker, y).BG.Equal(selection) {
					t.Error("item selection did not stay in the text box")
				}
				host.Handle(tea.MouseReleaseMsg{X: end, Y: y, Button: tea.MouseLeft})
			}
		}
	}
}

func TestMarkdownLTRListDoesNotFlipForRTLItem(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SyncMessages([]conversation.ChatMessage{{InternalID: "list", Role: conversation.RoleAssistant, Content: "- [x] First\n- [ ] שלום Last"}}, 1, 1)
	s := New(th)
	buffer := paintMarkdownHost(r, s, ui.NewHost(th, s), th, 66)
	_, first := findMarkdownHostLine(t, buffer, "First")
	_, last := findMarkdownHostLine(t, buffer, "Last םולש")
	if markdownColumn(t, first, "[✓]", false) != markdownColumn(t, last, "[ ]", false) {
		t.Errorf("markers must stay on the LTR list edge: %q / %q", first, last)
	}
}

func markdownColumn(t *testing.T, line, snippet string, last bool) int {
	t.Helper()
	index := strings.Index(line, snippet)
	if last {
		index = strings.LastIndex(line, snippet)
	}
	if index < 0 {
		t.Fatalf("missing %q in %q", snippet, line)
	}
	return rendering.VisibleWidth(line[:index])
}

func TestMarkdownNeutralTableHeaderUsesEnclosingDirection(t *testing.T) {
	const body = `שלום is a different block.

| 123 | 789 |
| --- | --- |
| שם | Outside |

> | 456 | 987 |
> | --- | --- |
> | First | Second |
> | End | |
>
> שלום is the enclosing direction.
`
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SyncMessages([]conversation.ChatMessage{{InternalID: "neutral-header", Role: conversation.RoleAssistant, Content: body}}, 1, 1)
	s := New(th)
	host := ui.NewHost(th, s)
	buffer := paintMarkdownHost(r, s, host, th, 78)
	for _, tt := range []struct{ left, right string }{{"123", "789"}, {"987", "456"}, {"Second", "First"}} {
		_, got := findMarkdownHostLine(t, buffer, tt.left)
		if left, right := strings.Index(got, tt.left), strings.Index(got, tt.right); right < left {
			t.Errorf("expected %q before %q, got %q", tt.left, tt.right, got)
		}
	}
	_, prose := findMarkdownHostLine(t, buffer, "enclosing direction")
	right := markdownColumn(t, prose, "│", true)
	y, head := findMarkdownHostLine(t, buffer, "987")
	_, child := findMarkdownHostLine(t, buffer, "Second")
	if markdownColumn(t, head, "│", true) != right || markdownColumn(t, child, "│", true) != right ||
		buffer.Get(right, y+1).Char != '│' {
		t.Errorf("the quote's rail must stay at the same column across table header, rule, and body: %q / %q", head, child)
	}
	start := markdownColumn(t, head, "456", false)
	lastY, _ := findMarkdownHostLine(t, buffer, "End")
	// Source column 2 is on the physical left, even when it has no text.
	empty := th.Spacing.PadLR + 1
	host.Handle(tea.MouseClickMsg{X: start, Y: y, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: empty, Y: lastY, Button: tea.MouseLeft})
	if got, want := s.SelectedText(), "456\t987\nFirst\tSecond\nEnd\t"; got != want {
		t.Errorf("selection through the empty cell = %q, want %q", got, want)
	}
	host.Handle(tea.MouseReleaseMsg{X: empty, Y: lastY, Button: tea.MouseLeft})
}

func BenchmarkMarkdownContainerArrangement(b *testing.B) {
	for _, fixture := range []struct{ name, item string }{
		{"ASCII", "> English quote with several words.\n>\n> - English item that wraps with more words.\n>   - Nested item.\n\n"},
		{"Mixed", "> שלום quote with several words.\n>\n> - שלום item that wraps with more words.\n>   - שלום Nested.\n\n"},
	} {
		markdown := strings.Repeat(fixture.item, 80)
		theTheme := theme.DefaultDark()
		const width = 40
		md := rendering.MakeMdRenderer(width, theTheme)
		tokens := ui.NewTokenSet(theTheme)
		build := func(b *testing.B) []chatprojection.PaintLine {
			p, err := md.RenderMapped(markdown)
			if err != nil {
				b.Fatal(err)
			}
			return chatprojection.BridgeSourceRows(p, tokens, "bench", false)
		}
		arrange := func(rows []chatprojection.PaintLine) {
			for _, row := range rows {
				chatprojection.ArrangedLine(row, width)
			}
		}
		b.Run(fixture.name+"/cold", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				arrange(build(b))
			}
		})
		b.Run(fixture.name+"/warm", func(b *testing.B) {
			rows := build(b)
			arrange(rows)
			b.ReportAllocs()
			for b.Loop() {
				arrange(rows)
			}
		})
	}
}

func paintMarkdownHost(r *chatprojection.Renderer, s *Screen, host *ui.Host, th *theme.Theme, width int) *surface.CellBuffer {
	const height = 24
	r.SetSize(width, height)
	r.SetChatAreaBounds(0, height)
	rows, _, total := r.ExportMessageRows(width)
	pad := th.Spacing.PadLR
	compose := ComposeParams{InnerW: width - 2*pad, PadLR: pad, Theme: th}
	s.SetMessages(rows, PaintParams{Compose: compose})
	s.SyncScrollMetrics(height, total)
	host.Resize(width, height)
	host.Layout()
	buffer := surface.NewCellBuffer(width, height)
	host.Paint(buffer)
	return buffer
}

func findMarkdownHostLine(t *testing.T, buffer *surface.CellBuffer, snippet string) (int, string) {
	t.Helper()
	for y := 0; y < buffer.Height(); y++ {
		var text strings.Builder
		for _, c := range buffer.Row(y) {
			switch {
			case c.Width == 0:
			case c.Content != "":
				text.WriteString(c.Content)
			case c.Char != 0:
				text.WriteRune(c.Char)
			default:
				text.WriteByte(' ')
			}
		}
		if strings.Contains(text.String(), snippet) {
			return y, text.String()
		}
	}
	t.Fatalf("missing visible Markdown text %q", snippet)
	return 0, ""
}
