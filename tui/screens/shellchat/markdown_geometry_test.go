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

const bidiTableMarkdown = `فارسی را می` + "\u200c" + `خوانیم.

---

| نام | API | توضیح |
| --- | :---: | --- |
| آغاز | x│y | |
| שלום !` + "`./run.sh --all`" + ` ادامه | a.go | |
| | keep | סוף |

| 12 | ID | سمت |
| --- | --- | --- |
| سلام | LTR | אבג |

جملهٔ آخر.
`

func TestChatMarkdownBidiGeometry(t *testing.T) {
	for _, width := range []int{24, 40, 100} {
		for _, live := range []bool{true, false} {
			t.Run(fmt.Sprintf("%d/live=%t", width, live), func(t *testing.T) {
				th := theme.DefaultDark()
				r := chatprojection.NewRenderer(th)
				r.SetSize(width, 40)
				r.SetChatAreaBounds(0, 40)
				msg := types.ChatMessage{InternalID: "markdown-bidi", Role: types.RoleAssistant}
				if live {
					cut := strings.Index(bidiTableMarkdown, "| שלום")
					msg.Content = bidiTableMarkdown[:cut]
					r.AppendMessage(msg)
					r.SetStreaming(true)
					r.ExportMessageRows(width) // Publish a completed prefix before the final update.
					r.AppendToken(bidiTableMarkdown[cut:])
				} else {
					msg.Content = bidiTableMarkdown
					r.SyncMessages([]types.ChatMessage{msg}, 1, 1)
				}
				rows, _, height := r.ExportMessageRows(width)
				p := ComposeParams{InnerW: width - 2*th.Spacing.PadLR, PadLR: th.Spacing.PadLR, Theme: th}
				s, host := newShellChatHost(t, rows, p, height)
				buffer := surface.NewCellBuffer(width, height)
				host.Paint(buffer)
				var visual []string
				for y := 0; y < height; y++ {
					var line strings.Builder
					for _, c := range buffer.Row(y) {
						if c.Width == 0 {
							continue
						}
						glyph := c.Content
						if glyph == "" {
							glyph = string(c.Char)
						}
						line.WriteString(glyph)
					}
					visual = append(visual, line.String())
					t.Logf("%02d |%s|", y, line.String())
				}
				find := func(marker string) (int, int) {
					for y, line := range visual {
						if at := strings.Index(line, marker); at >= 0 {
							return y, rendering.VisibleWidth(line[:at])
						}
					}
					t.Errorf("missing visible marker %q", marker)
					return -1, -1
				}

				header, api := find("API")
				firstRow, first := find("ﺯﺎﻏﺁ") // آغاز, in visual order
				lastRow, last := find("ףוס")
				if last >= api || api >= first {
					t.Errorf("RTL table cells must keep source column one on the right; left=%d API=%d right=%d", last, api, first)
				}
				_, id := find("ID")
				_, ltr := find("LTR")
				_, hebrew := find("גבא")
				if id > ltr || ltr >= hebrew {
					t.Errorf("the second table must use its own first strong header character: ID=%d LTR=%d Hebrew=%d", id, ltr, hebrew)
				}
				// The thematic break and the table occupy the same Markdown
				// block width, not the margins reserved for chat chrome.
				blockWidth := width - 2*th.Spacing.PadLR
				rule := strings.Repeat("─", blockWidth)
				if header >= 0 && !strings.Contains(strings.Join(visual[1:header], "\n"), rule) {
					t.Error("thematic break must fill the content area and stay out of bidi")
				}
				if header >= 0 && lastRow > header {
					var rails []int
					for col := 0; col < width; col++ {
						if buffer.Get(col, header+1).Char == '┼' {
							rails = append(rails, col)
						}
					}
					if len(rails) != 2 {
						t.Errorf("header rule must join both column dividers: %v", rails)
					}
					for y := header + 2; y <= lastRow; y++ {
						for _, rail := range rails {
							if got := buffer.Get(rail, y).Char; got != '│' {
								t.Errorf("divider moved at row %d, column %d: %q", y, rail, got)
							}
						}
					}
					want := "نام\tAPI\tتوضیح\nآغاز\tx│y\t\nשלום !./run.sh --all ادامه\ta.go\t\n\tkeep\tסוף"
					if got := extractSelectedText(rows, header, 0, lastRow, width); got != want {
						t.Errorf("whole table source = %q, want %q", got, want)
					}
				}
				literalRow, literal := find("x│y")
				if firstRow >= 0 && literalRow == firstRow {
					host.Handle(tea.MouseClickMsg{X: first + 3, Y: firstRow, Button: tea.MouseLeft})
					host.Handle(tea.MouseMotionMsg{X: literal + 3, Y: firstRow, Button: tea.MouseLeft})
					if got := s.SelectedText(); got != "آغاز\tx│y" {
						t.Errorf("across reversed columns got %q, want semantic source order", got)
					}
					host.Paint(buffer)
					_, selected := ui.NewTokenSet(th).TextSelectionColors(cell.NoColor)
					if !buffer.Get(literal+1, firstRow).BG.Equal(selected) {
						t.Error("literal separator in cell is source text")
					}
					for x := literal + 3; x < first; x++ {
						if buffer.Get(x, firstRow).Char == '│' && buffer.Get(x, firstRow).BG.Equal(selected) {
							t.Error("selection should not paint the structural divider")
						}
					}
					host.Handle(tea.MouseReleaseMsg{X: literal + 3, Y: firstRow, Button: tea.MouseLeft})
				}
				codeRow, codeCol := find("םולש")
				if codeRow >= 0 && width == 100 && !strings.Contains(visual[codeRow], "./run.sh --all") {
					t.Error("inline code in RTL table cell lost its LTR order")
				}
				if codeCol >= 0 {
					copied := ""
					s.SetClipboardCopy(func(value string) tea.Cmd { return func() tea.Msg { copied = value; return nil } })
					host.Handle(tea.MouseClickMsg{X: codeCol + 3, Y: codeRow, Button: tea.MouseLeft})
					host.Handle(tea.MouseMotionMsg{X: codeCol - 1, Y: codeRow, Button: tea.MouseLeft})
					if got := s.SelectedText(); got != "שלום" {
						t.Errorf("RTL drag copies %q, want שלום", got)
					}
					host.Paint(buffer)
					_, selected := ui.NewTokenSet(th).TextSelectionColors(cell.NoColor)
					for x := codeCol; x < codeCol+4; x++ {
						if !buffer.Get(x, codeRow).BG.Equal(selected) {
							t.Errorf("selection ink missed visible RTL cell %d", x)
						}
					}
					if result := host.Handle(tea.MouseReleaseMsg{X: codeCol - 1, Y: codeRow, Button: tea.MouseLeft}); result.Cmd != nil {
						result.Cmd()
					}
					if copied != "שלום" {
						t.Errorf("clipboard source = %q, want שלום", copied)
					}
					s.OpenFind()
					s.SetFindQuery("שלום", FindOptions{})
					s.observeFind(0, false)
					host.Paint(buffer)
					match := ui.NewTokenSet(th).FindMatchFill(true)
					for x := codeCol; x < codeCol+4; x++ {
						if !buffer.Get(x, codeRow).BG.Equal(match) {
							t.Errorf("find ink missed visible cell %d", x)
						}
					}
					s.CloseFind()
				}
				linkRow, linkCol := find("a.go")
				if linkRow >= 0 {
					host.Handle(tea.MouseClickMsg{X: linkCol + 2, Y: linkRow, Button: tea.MouseLeft})
					result := host.Handle(tea.MouseReleaseMsg{X: linkCol + 2, Y: linkRow, Button: tea.MouseLeft})
					if result.Action == nil || result.Action.ID != chatprojection.ChatFileOpenActionID {
						t.Errorf("link at visual position did not open: %+v", result.Action)
					}
				}
			})
		}
	}
}
