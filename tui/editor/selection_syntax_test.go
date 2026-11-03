package editor

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestSelectionKeepsDistinctReadableSyntaxAndExactText(t *testing.T) {
	const source = "var greeting = \"hello 界\"\n"
	for _, th := range theme.AllPresets() {
		t.Run(th.Name, func(t *testing.T) {
			ed := loadEditorText(t, "selection.go", source)
			ed.SetTheme(th)
			settleSyntax(t, ed)
			// Keep the insertion cursor on the empty final line, not on the
			// keyword whose selected syntax ink the test inspects.
			ed.SetCursorPosition(1, 0)
			before := paintEditor(ed, 80, 8)
			x := ed.gutterWidth()
			stringCol := strings.Index(source, "hello")
			keywordBefore, stringBefore := before.Get(x, 0), before.Get(x+stringCol, 0)
			if keywordBefore.FG == stringBefore.FG {
				t.Fatal("fixture did not produce distinct keyword and string ink")
			}
			ed.HandleEvent(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			if ed.SelectedText() != source {
				t.Fatalf("select all changed source selection: %q", ed.SelectedText())
			}
			painted := paintEditor(ed, 80, 8)
			keyword, str := painted.Get(x, 0), painted.Get(x+stringCol, 0)
			bg := ed.tokens.Color(ui.ColorTextSelection)
			if keyword.Char != 'v' || str.Char != 'h' || keyword.BG != bg || str.BG != bg {
				t.Fatalf("selection did not cover source: keyword=%+v string=%+v", keyword, str)
			}
			// Tiny RGB differences near white/black were technically distinct
			// but visually erased syntax in the native selection.
			difference := func(a, b uint8) int {
				d := int(a) - int(b)
				if d < 0 {
					return -d
				}
				return d
			}
			distance := difference(keyword.FG.R, str.FG.R) + difference(keyword.FG.G, str.FG.G) + difference(keyword.FG.B, str.FG.B)
			if distance < 80 || cell.ContrastRatio(keyword.FG, bg) < 4.5 || cell.ContrastRatio(str.FG, bg) < 4.5 {
				t.Fatalf("syntax was flattened or unreadable: keyword=%+v string=%+v", keyword, str)
			}
			if cell.ContrastRatio(stringBefore.FG, bg) >= 4.5 && str.FG != stringBefore.FG {
				t.Fatal("already-readable syntax color was unnecessarily changed")
			}
			if ed.Value() != source {
				t.Fatal("selection mutated source")
			}
			ed.ClearSelection()
			after := paintEditor(ed, 80, 8)
			if after.Get(x, 0).FG != keywordBefore.FG || after.Get(x+stringCol, 0).FG != stringBefore.FG {
				t.Fatal("clearing selection did not restore the original syntax ink")
			}
		})
	}
}
