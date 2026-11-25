package ui

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"strings"
	"testing"
)

func TestTextAreaWrapsChipAsOneRow(t *testing.T) {
	const label = "[Pasted text #1 +20 lines]"
	for _, tc := range []struct {
		name, prefix string
		width        int
		chip         string
	}{
		{"fits", "prefix words ", 28, label}, {"too wide", "prefix ", 10, "[Pasted t…"}, {"RTL", "שלום ", 28, label},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := len([]rune(tc.prefix))
			area := newArea(tc.prefix + label + " suffix")
			area.Layout(LayoutContext{}, Rect{W: tc.width, H: 8}) // warm the ordinary wrap cache
			area.SetAtomicRanges([]AtomicRange{{Start: start, End: start + len(label)}})
			area.SetCursorFlat(start)
			buf := surface.NewCellBuffer(tc.width+8, 8)
			area.Paint(buf, 0, 0, PaintContext{Focused: true})
			row := rowText(buf, 1)
			x := strings.Index(row, tc.chip)
			if x < 0 || x+len([]rune(tc.chip)) > tc.width {
				t.Fatalf("row 1 = %q, want a single %q chip inside %d cells", row, tc.chip, tc.width)
			}
			if area.TotalVisualLines() != 3 || area.CursorVisualLine() != 1 {
				t.Fatalf("rows=%d, caret row=%d, want 3 and 1", area.TotalVisualLines(), area.CursorVisualLine())
			}
			for i := range []rune(tc.chip) {
				if !buf.Get(x+i, 1).Attr.Has(cell.AttrBold) || area.MouseToFlatPos(x+i, 1) != start {
					t.Fatalf("chip cell %d not one styled hit: bold=%v, hit=%d, want %d", i, buf.Get(x+i, 1).Attr.Has(cell.AttrBold), area.MouseToFlatPos(x+i, 1), start)
				}
			}
			if got := reversedCellX(buf, 1); got != x {
				t.Fatalf("chip caret is at %d, want %d", got, x)
			}
			if c := buf.Get(tc.width, 1); c.Char != 0 && c.Char != ' ' {
				t.Fatalf("chip overpainted adjacent control: %+v", c)
			}
			if tc.name != "RTL" {
				area.SetCursorFlat(3)
				press(area, tea.KeyDown, 0)
				if got := area.CursorFlat(); got != start {
					t.Fatalf("Down landed inside chip at %d, want %d", got, start)
				}
				press(area, tea.KeyRight, tea.ModShift)
				if got := area.SelectedText(); got != label {
					t.Fatalf("Shift+Right selected %q, want whole label", got)
				}
			}
		})
	}
}

func TestTextAreaChipWordDeletionAndUndo(t *testing.T) {
	const label = "[file one]"
	a := newArea("keep " + label + " tail")
	a.SetAtomicRanges([]AtomicRange{{Start: 5, End: 5 + len(label)}})
	a.SetCursorFlat(5)
	press(a, tea.KeyDelete, tea.ModCtrl)
	if got := a.Value(); got != "keep  tail" {
		t.Fatalf("word delete split chip: %q", got)
	}
	press(a, 'z', tea.ModCtrl)
	buf := paintArea(t, a, 24, 2)
	if a.Value() != "keep "+label+" tail" || !buf.Get(5, 0).Attr.Has(cell.AttrBold) {
		t.Fatalf("Undo did not restore chip: %q", rowText(buf, 0))
	}
	a.SetCursorFlat(0)
	a.PasteText("ab")
	buf = paintArea(t, a, 24, 2)
	if buf.Get(5, 0).Attr.Has(cell.AttrBold) || !buf.Get(7+len(label)-1, 0).Attr.Has(cell.AttrBold) {
		t.Fatalf("paste shifted text but not style: %q", rowText(buf, 0))
	}
}

// Without objects the area must present the shared word wrap byte for byte,
// including combining sequences and repeated spaces omitted at soft breaks.
func TestTextAreaPlainRowsMatchWordWrap(t *testing.T) {
	for _, text := range []string{"alpha   alpha trailing ", "   longunbrokenword tail", "界 éé 👩‍💻 word"} {
		for _, width := range []int{5, 8, 14} {
			want := rendering.WordWrap(text, width)
			a := newArea(text)
			buf := paintArea(t, a, width, len(want))
			if a.TotalVisualLines() != len(want) {
				t.Fatalf("%q: rows=%d, want %d", text, a.TotalVisualLines(), len(want))
			}
			for y, row := range want {
				var got strings.Builder
				for _, c := range buf.Row(y) {
					if c.Width == 0 {
						continue
					}
					if c.Content != "" {
						got.WriteString(c.Content)
					} else if c.Char != 0 {
						got.WriteRune(c.Char)
					} else {
						got.WriteByte(' ')
					}
				}
				padded := row + strings.Repeat(" ", width-rendering.VisibleWidth(row))
				if got.String() != padded {
					t.Fatalf("%q width %d row %d: %q, want %q", text, width, y, got.String(), padded)
				}
			}
		}
	}
}

func TestTextAreaAdjacentWordsStopAtChip(t *testing.T) {
	const text = "pre[ref]post"
	a := newArea(text)
	a.SetAtomicRanges([]AtomicRange{{Start: 3, End: 8}})
	a.SetCursorFlat(0)
	for _, want := range []int{3, 8, 12} {
		press(a, tea.KeyRight, tea.ModCtrl)
		if a.CursorFlat() != want {
			t.Fatalf("word right reached %d, want %d", a.CursorFlat(), want)
		}
	}
	for _, want := range []int{8, 3, 0} {
		press(a, tea.KeyLeft, tea.ModCtrl)
		if a.CursorFlat() != want {
			t.Fatalf("word left reached %d, want %d", a.CursorFlat(), want)
		}
	}
	a.SetCursorFlat(3)
	press(a, tea.KeyDelete, tea.ModCtrl)
	if a.Value() != "prepost" {
		t.Fatalf("word delete removed neighboring prose: %q", a.Value())
	}
}
