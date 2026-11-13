package shellchat

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestMarkdownDragCopiesSourceWithoutLayoutSpacing(t *testing.T) {
	for _, width := range []int{40, 80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			th := theme.DefaultDark()
			r := chatprojection.NewRenderer(th)
			r.SetSize(width, 40)
			r.SetChatAreaBounds(0, 40)
			r.AppendMessage(types.ChatMessage{InternalID: "copy", Role: types.RoleAssistant, Content: "Run `alpha beta` next."})
			rows, _, _ := r.ExportMessageRows(width)
			s := New(th)
			s.SetMessages(rows, PaintParams{Compose: ComposeParams{InnerW: width, PadLR: th.Spacing.PadLR, Theme: th}})
			flat := flattenMessageLines(rows)
			s.SyncScrollMetrics(len(flat), len(flat))
			s.Layout(ui.LayoutContext{}, ui.Rect{W: width + 2*th.Spacing.PadLR, H: len(flat)})
			line := -1
			start, end := 0, 0
			for y, text := range flat {
				if at := strings.Index(text, "alpha beta"); at >= 0 {
					line = y
					start = rendering.VisibleWidth(text[:at])
					end = start + 10
					break
				}
			}
			if line < 0 {
				t.Fatalf("inline code not painted: %q", flat)
			}
			ctx := ui.EventContext{LocalX: th.Spacing.PadLR + start - 1, LocalY: line}
			s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
			ctx.LocalX = th.Spacing.PadLR + end + 1
			s.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
			if got := s.SelectedText(); got != "alpha beta" {
				t.Fatalf("inline source copy=%q", got)
			}
			if result := s.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft})); result.Cmd == nil {
				t.Fatal("source selection has no copy command")
			}
		})
	}
}

func TestMarkdownCodeDragKeepsTabsBlankLinesAndSoftWraps(t *testing.T) {
	code := "if ready:\n\tprint(\"界 é 👩‍💻\")  \n\n\tpass"
	for _, width := range []int{40, 80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			th := theme.DefaultDark()
			r := chatprojection.NewRenderer(th)
			r.SetSize(width, 40)
			r.SetChatAreaBounds(0, 40)
			r.AppendMessage(types.ChatMessage{InternalID: "code", Role: types.RoleAssistant, Content: "- Code:\n\n  ```python\n  " + strings.ReplaceAll(code, "\n", "\n  ") + "\n  ```"})
			rows, _, _ := r.ExportMessageRows(width)
			s := New(th)
			flat := flattenMessageLines(rows)
			s.SetMessages(rows, PaintParams{SourceID: 1, Generation: 1, Compose: ComposeParams{InnerW: width, PadLR: th.Spacing.PadLR, Theme: th}})
			s.SyncScrollMetrics(len(flat), len(flat))
			s.Layout(ui.LayoutContext{}, ui.Rect{W: width + 2*th.Spacing.PadLR, H: len(flat)})
			first, last := -1, -1
			for y, text := range flat {
				if strings.Contains(text, "if ready:") {
					first = y
				}
				if strings.Contains(text, "pass") {
					last = y
				}
			}
			if first < 0 || last < first {
				t.Fatalf("code not visible: %q", flat)
			}
			ctx := ui.EventContext{LocalX: th.Spacing.PadLR, LocalY: first}
			s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
			ctx.LocalX = width + th.Spacing.PadLR
			ctx.LocalY = last
			s.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
			if got := s.SelectedText(); got != code {
				t.Fatalf("source=%q want %q; rows=%q", got, code, flat)
			}
		})
	}
}

func TestMarkdownWrappedParagraphCopyKeepsSourceNotVisualNewlines(t *testing.T) {
	text := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(40, 40)
	r.SetChatAreaBounds(0, 40)
	r.AppendMessage(types.ChatMessage{InternalID: "paragraph", Role: types.RoleAssistant, Content: text})
	rows, _, _ := r.ExportMessageRows(40)
	flat := flattenMessageLines(rows)
	first, last := -1, -1
	for y, line := range flat {
		if strings.Contains(line, "alpha") {
			first = y
		}
		if strings.Contains(line, "mu") {
			last = y
		}
	}
	if first < 0 || last <= first {
		t.Fatalf("fixture did not wrap: %q", flat)
	}
	if got := extractSelectedText(rows, first, 0, last, 1<<20); got != text {
		t.Fatalf("copy=%q", got)
	}
}

func TestMarkdownDragKeepsTheSelectedSourceAcrossReflow(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(40, 40)
	r.SetChatAreaBounds(0, 40)
	source := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu"
	r.AppendMessage(types.ChatMessage{InternalID: "reflow", Role: types.RoleAssistant, Content: source})
	rows, _, _ := r.ExportMessageRows(40)
	flat := flattenMessageLines(rows)
	s := New(th)
	paint := PaintParams{SourceID: 7, Generation: 1, Compose: ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th}}
	s.SetMessages(rows, paint)
	s.SyncScrollMetrics(len(flat), len(flat))
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 44, H: len(flat)})
	first, last := -1, -1
	for y, line := range flat {
		if strings.Contains(line, "alpha") {
			first = y
		}
		if strings.Contains(line, "mu") {
			last = y
		}
	}
	ctx := ui.EventContext{LocalX: th.Spacing.PadLR, LocalY: first}
	s.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	ctx.LocalX = 40 + th.Spacing.PadLR
	ctx.LocalY = last
	s.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
	if got := s.SelectedText(); got != source {
		t.Fatalf("before resize=%q", got)
	}
	r.SetSize(100, 40)
	next, _, _ := r.ExportMessageRows(100)
	paint.Generation = 2
	paint.Compose.InnerW = 100
	s.SetMessages(next, paint)
	if got := s.SelectedText(); got != source {
		t.Fatalf("resize changed copied source=%q", got)
	}
	// A different conversation cannot reuse a local source-block number.
	paint.SourceID = 8
	paint.Generation = 1
	s.SetMessages(next, paint)
	if s.HasActiveSelection() {
		t.Fatal("conversation switch retained source selection")
	}
}

func TestMarkdownSelectionUsesSemanticBreaksAndLiteralCode(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"styled prose", "Hello **bold** and `a  b`.", "Hello bold and a  b."},
		{"paragraph break", "First paragraph.\n\nSecond paragraph.", "First paragraph.\n\nSecond paragraph."},
		{"source newline", "First line.\nSecond line.", "First line.\nSecond line."},
		{"list", "- `alpha beta`\n- `gamma delta`", "alpha beta\ngamma delta"},
		{"definition blocks", "First term\n: First meaning.\n\nSecond term\n: Second meaning.", "First term\nFirst meaning.\nSecond term\nSecond meaning."},
		{"table", "| Name | Value |\n| --- | --- |\n| one | `a  b` |\n| two | `c d` |", "Name\tValue\none\ta  b\ntwo\tc d"},
		{"literal code", "```text\na\\*b &lt;tag&gt;\n\tvalue  \n```", "a\\*b &lt;tag&gt;\n\tvalue  "},
		{"CRLF code", "```text\r\nfirst  \r\n\r\n\tlast\r\n```", "first  \r\n\r\n\tlast"},
		{"wide long line", "```text\n" + strings.Repeat("界é👩‍💻", 30) + "\n```", strings.Repeat("界é👩‍💻", 30)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, width := range []int{40, 80, 160} {
				th := theme.DefaultDark()
				r := chatprojection.NewRenderer(th)
				r.SetSize(width, 100)
				r.SetChatAreaBounds(0, 100)
				r.AppendMessage(types.ChatMessage{InternalID: "semantic", Role: types.RoleAssistant, Content: tc.input})
				rows, _, _ := r.ExportMessageRows(width)
				first, last, global := -1, -1, 0
				for _, row := range rows {
					for _, line := range row.Native {
						if line.SourceAware {
							if first < 0 {
								first = global
							}
							last = global
						}
						global++
					}
				}
				if first < 0 {
					t.Fatal("no source-aware body")
				}
				if got := extractSelectedText(rows, first, 0, last, 1<<20); got != tc.want {
					t.Fatalf("width %d source=%q want=%q rows=%q", width, got, tc.want, flattenMessageLines(rows))
				}
			}
		})
	}
}

func TestMarkdownPartialSelectionAfterExpandedTabCopiesOnlyCrossedSource(t *testing.T) {
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(80, 40)
	r.SetChatAreaBounds(0, 40)
	r.AppendMessage(types.ChatMessage{InternalID: "partial", Role: types.RoleAssistant, Content: "```text\n\talpha beta\n```"})
	rows, _, _ := r.ExportMessageRows(80)
	for y, text := range flattenMessageLines(rows) {
		if at := strings.Index(text, "beta"); at >= 0 {
			start := rendering.VisibleWidth(text[:at])
			s, host := newShellChatHost(t, rows, ComposeParams{InnerW: 80, PadLR: th.Spacing.PadLR, Theme: th}, len(flattenMessageLines(rows)))
			host.Handle(tea.MouseClickMsg{X: start + 4 + th.Spacing.PadLR, Y: y, Button: tea.MouseLeft})
			host.Handle(tea.MouseMotionMsg{X: start + th.Spacing.PadLR, Y: y, Button: tea.MouseLeft})
			if got := s.SelectedText(); got != "beta" {
				t.Fatalf("backward partial selection=%q", got)
			}
			return
		}
	}
	t.Fatal("code suffix not visible")
}

func TestMarkdownDragWithinWrappedTableCellCopiesItsSourceNotNeighbors(t *testing.T) {
	const code = "alpha beta gamma delta epsilon"
	th := theme.DefaultDark()
	r := chatprojection.NewRenderer(th)
	r.SetSize(40, 40)
	r.SetChatAreaBounds(0, 40)
	r.AppendMessage(types.ChatMessage{InternalID: "cell", Role: types.RoleAssistant, Content: "| Command | Note |\n| --- | --- |\n| `" + code + "` | other cell |"})
	rows, _, _ := r.ExportMessageRows(40)
	flat := flattenMessageLines(rows)
	first, last, start, end := -1, -1, 0, 0
	for y, line := range flat {
		if at := strings.Index(line, "alpha"); at >= 0 {
			first = y
			start = rendering.VisibleWidth(line[:at])
		}
		if at := strings.Index(line, "epsilon"); at >= 0 {
			last = y
			end = rendering.VisibleWidth(line[:at]) + len("epsilon")
		}
	}
	if first < 0 || last <= first {
		t.Fatalf("cell did not wrap: %q", flat)
	}
	s, host := newShellChatHost(t, rows, ComposeParams{InnerW: 40, PadLR: th.Spacing.PadLR, Theme: th}, len(flat))
	host.Handle(tea.MouseClickMsg{X: start + th.Spacing.PadLR, Y: first, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: end + th.Spacing.PadLR, Y: last, Button: tea.MouseLeft})
	if got := s.SelectedText(); got != code {
		t.Fatalf("cell source selection=%q want=%q; rows=%q", got, code, flat)
	}
	buffer := surface.NewCellBuffer(44, len(flat))
	host.Paint(buffer)
	_, selectedBg := ui.NewTokenSet(th).TextSelectionColors(cell.NoColor)
	other := rendering.VisibleWidth(flat[first][:strings.Index(flat[first], "other")]) + th.Spacing.PadLR
	if buffer.Get(start+th.Spacing.PadLR, first).BG != selectedBg || buffer.Get(other, first).BG == selectedBg {
		t.Fatal("source selection highlight included a neighboring table cell")
	}
	copied := ""
	s.SetClipboardCopy(func(text string) tea.Cmd { return func() tea.Msg { copied = text; return nil } })
	result := host.Handle(tea.MouseReleaseMsg{X: end + th.Spacing.PadLR, Y: last, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("source selection did not issue a copy command")
	}
	result.Cmd()
	if copied != code {
		t.Fatalf("released table copy=%q", copied)
	}
}
