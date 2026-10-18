package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
)

// Paint expectations are literal display spans, independent of the gesture's
// source-boundary calculation and of isSelected/SelectionRange.
type pointerPaintSpan struct{ line, start, end int }

func requirePointerSelectionPaint(t *testing.T, ed *Editor, want string, spans ...pointerPaintSpan) {
	t.Helper()
	if got := ed.SelectedText(); got != want {
		t.Errorf("selected bytes = %q, want %q", got, want)
	}
	surf := surface.NewSurface("anchor-paint", ed.viewW, ed.viewH, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Focused: true})
	selectedBG := ed.tokens.Color(ui.ColorTextSelection)
	for y := 0; y < ed.bodyHeight(); y++ {
		for x := ed.gutterWidth(); x < ed.viewW-ed.scrollbarColWidth(); x++ {
			col, line := ed.scrollX+x-ed.gutterWidth(), ed.scrollY+y
			selected := false
			for _, span := range spans {
				selected = selected || span.line == line && col >= span.start && col < span.end
			}
			if got := surf.Buffer().Get(x, y).BG == selectedBG; got != selected {
				t.Errorf("paint at source (%d,%d): selection fill = %v, want %v", line, col, got, selected)
			}
		}
	}
}

func TestPointerBackwardDragIncludesPressedSourceUnit(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		press, to        Position
		spans            []pointerPaintSpan
	}{
		{"ASCII", "abcdef", "bcd", Position{Col: 3}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 4}}},
		{"wide leading cell", "ab界Z", "b界", Position{Col: 2}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 4}}},
		{"wide continuation", "ab界Z", "b界", Position{Col: 3}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 4}}},
		{"combining", "abe\u0301Z", "be\u0301", Position{Col: 2}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 3}}},
		{"ZWJ continuation", "ab👩‍💻Z", "b👩‍💻", Position{Col: 3}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 4}}},
		{"tab interior", "ab\tZ", "b\t", Position{Col: 3}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 4}}},
		{"tab stop", "abcd\tZ", "d\t", Position{Col: 6}, Position{Col: 3}, []pointerPaintSpan{{0, 3, 8}}},
		{"LF across lines", "abc\ndef", "bc\nde", Position{Line: 1, Col: 1}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 33}, {1, 0, 2}}},
		{"CRLF across lines", "abc\r\ndef", "bc\r\nde", Position{Line: 1, Col: 1}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 33}, {1, 0, 2}}},
		{"line start", "abc\r\ndef", "bc\r\nd", Position{Line: 1}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 33}, {1, 0, 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := newPointerTestEditor(t, tc.text, 40, 8)
			x := ed.gutterWidth()
			ed.HandleEvent(tea.MouseClickMsg{X: x + tc.press.Col, Y: tc.press.Line, Button: tea.MouseLeft})
			if ed.HasSelection() {
				t.Fatal("plain press must place an insertion caret, not select its character")
			}
			// Reproduce the initial block cursor over the pressed source unit.
			surf := surface.NewSurface("pressed-cursor", 40, 8, cell.TransparentBlank())
			ed.Render(surf, ui.PaintContext{Focused: true})
			if !surf.Buffer().Get(x+ed.cursor.Col, ed.cursor.Line).Attr.Has(cell.AttrReverse) {
				t.Fatal("pressed source unit did not paint the initial block cursor")
			}
			ed.HandleEvent(tea.MouseMotionMsg{X: x + tc.to.Col, Y: tc.to.Line, Button: tea.MouseLeft})
			requirePointerSelectionPaint(t, ed, tc.want, tc.spans...)
			if cmd := ed.HandleEvent(tea.MouseReleaseMsg{X: x + tc.to.Col, Y: tc.to.Line, Button: tea.MouseLeft}); cmd != nil {
				t.Fatal("editable release must not copy")
			}
			requirePointerSelectionPaint(t, ed, tc.want, tc.spans...)
			if ed.IsMouseDragging() || ed.Modified() || ed.Value() != tc.text {
				t.Fatal("release must stop capture and leave source bytes unchanged")
			}
		})
	}
}

func TestPointerCharacterAnchorReversesWithoutAccumulating(t *testing.T) {
	ed := newPointerTestEditor(t, "ab界de", 40, 8)
	x := ed.gutterWidth()
	ed.HandleEvent(tea.MouseClickMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	for _, step := range []struct {
		col, end int
		want     string
		spans    []pointerPaintSpan
	}{
		{1, 4, "b界", []pointerPaintSpan{{0, 1, 4}}},
		{5, 2, "界d", []pointerPaintSpan{{0, 2, 5}}},
		{3, 2, "", nil}, // Return within the original wide glyph collapses.
		{0, 4, "ab界", []pointerPaintSpan{{0, 0, 4}}},
		{4, 2, "界", []pointerPaintSpan{{0, 2, 4}}},
		{1, 4, "b界", []pointerPaintSpan{{0, 1, 4}}},
	} {
		ed.HandleEvent(tea.MouseMotionMsg{X: x + step.col, Y: 0, Button: tea.MouseLeft})
		requirePointerSelectionPaint(t, ed, step.want, step.spans...)
		if ed.selAnchor != (Position{Col: step.end}) {
			t.Fatalf("anchor after reversal = %v, want col %d", ed.selAnchor, step.end)
		}
	}
	// The final release position can reverse direction without a motion event.
	ed.HandleEvent(tea.MouseReleaseMsg{X: x + 5, Y: 0, Button: tea.MouseLeft})
	requirePointerSelectionPaint(t, ed, "界d", pointerPaintSpan{0, 2, 5})
	ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 0})
	requirePointerSelectionPaint(t, ed, "界d", pointerPaintSpan{0, 2, 5})
}

func TestPointerCharacterAnchorEmptyAndLineEndRemainCaretBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, text, want string
		press, to        Position
		spans            []pointerPaintSpan
	}{
		{"click only", "abcdef", "", Position{Col: 3}, Position{Col: 3}, nil},
		{"tab same unit", "ab\tZ", "", Position{Col: 3}, Position{Col: 2}, nil},
		{"EOL backward", "abc\r\ndef", "bc", Position{Col: 8}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 3}}},
		{"EOL forward", "abc\r\ndef", "\r\nd", Position{Col: 8}, Position{Line: 1, Col: 1}, []pointerPaintSpan{{0, 3, 33}, {1, 0, 1}}},
		{"empty line", "abc\r\n\r\ndef", "bc\r\n", Position{Line: 1}, Position{Col: 1}, []pointerPaintSpan{{0, 1, 33}}},
		{"empty document", "", "", Position{}, Position{}, nil},
		{"forward excludes target", "abcdef", "de", Position{Col: 3}, Position{Col: 5}, []pointerPaintSpan{{0, 3, 5}}},
		{"forward wide target", "ab界Z", "ab", Position{}, Position{Col: 3}, []pointerPaintSpan{{0, 0, 2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := newPointerTestEditor(t, tc.text, 40, 8)
			x := ed.gutterWidth()
			ed.HandleEvent(tea.MouseClickMsg{X: x + tc.press.Col, Y: tc.press.Line, Button: tea.MouseLeft})
			ed.HandleEvent(tea.MouseReleaseMsg{X: x + tc.to.Col, Y: tc.to.Line, Button: tea.MouseLeft})
			requirePointerSelectionPaint(t, ed, tc.want, tc.spans...)
		})
	}
}

func TestPointerShiftAndKeyboardKeepHalfOpenCaretAnchor(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "caret", true: "existing selection"}[existing], func(t *testing.T) {
			ed := newPointerTestEditor(t, "abcdef", 40, 8)
			ed.cursor = Position{Col: 3}
			if existing {
				ed.SetSelection(0, 3, 0, 5)
				ed.cursor = Position{Col: 5}
			}
			x := ed.gutterWidth()
			ed.HandleEvent(tea.MouseClickMsg{X: x + 1, Y: 0, Button: tea.MouseLeft, Mod: tea.ModShift})
			requirePointerSelectionPaint(t, ed, "bc", pointerPaintSpan{0, 1, 3})
			ed.HandleEvent(tea.MouseMotionMsg{X: x + 5, Y: 0, Button: tea.MouseLeft})
			requirePointerSelectionPaint(t, ed, "de", pointerPaintSpan{0, 3, 5})
			ed.HandleEvent(tea.MouseReleaseMsg{X: x + 1, Y: 0, Button: tea.MouseLeft})
			requirePointerSelectionPaint(t, ed, "bc", pointerPaintSpan{0, 1, 3})
		})
	}
	ed := newPointerTestEditor(t, "ab界Z", 40, 8)
	x := ed.gutterWidth()
	ed.HandleEvent(tea.MouseClickMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseReleaseMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift})
	requirePointerSelectionPaint(t, ed, "b", pointerPaintSpan{0, 1, 2})
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	requirePointerSelectionPaint(t, ed, "")
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	requirePointerSelectionPaint(t, ed, "界", pointerPaintSpan{0, 2, 4})
	if ed.IsMouseDragging() {
		t.Fatal("keyboard selection armed a pointer gesture")
	}
}

func TestPointerBackwardAnchorTracksPeerDelta(t *testing.T) {
	ed, snapshot := projectionEditor(t, "abcdef")
	ed.SetStatusChrome(false)
	ed.Render(surface.NewSurface("peer-anchor", 40, 8, cell.TransparentBlank()), ui.PaintContext{})
	x := ed.gutterWidth()
	ed.HandleEvent(tea.MouseClickMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	change := document.Change{DocumentID: snapshot.ID, BaseRevision: 1, Owner: document.Owner{ClientID: "peer", Epoch: 1}, Edits: []document.Edit{{Start: 0, End: 0, Text: "XY"}}}
	snapshot.Text, snapshot.Revision = []byte("XYabcdef"), 2
	if err := ed.ApplyDocumentDelta(change, snapshot); err != nil {
		t.Fatal(err)
	}
	ed.HandleEvent(tea.MouseMotionMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	requirePointerSelectionPaint(t, ed, "bcd", pointerPaintSpan{0, 3, 6})
}

func TestPointerWholeUnitAnchorsKeepReversing(t *testing.T) {
	t.Run("word", func(t *testing.T) {
		ed := newPointerTestEditor(t, "alpha beta gamma", 40, 8)
		x := ed.gutterWidth()
		for i := 0; i < 2; i++ {
			ed.HandleEvent(tea.MouseClickMsg{X: x + 7, Y: 0, Button: tea.MouseLeft})
			if i == 0 {
				ed.HandleEvent(tea.MouseReleaseMsg{X: x + 7, Y: 0, Button: tea.MouseLeft})
			}
		}
		for _, step := range []struct {
			col, start, end int
			want            string
		}{{2, 0, 10, "alpha beta"}, {13, 6, 16, "beta gamma"}, {7, 6, 10, "beta"}, {2, 0, 10, "alpha beta"}} {
			ed.HandleEvent(tea.MouseMotionMsg{X: x + step.col, Y: 0, Button: tea.MouseLeft})
			requirePointerSelectionPaint(t, ed, step.want, pointerPaintSpan{0, step.start, step.end})
		}
	})
	for _, gutter := range []bool{false, true} {
		t.Run(map[bool]string{false: "triple click line", true: "gutter line"}[gutter], func(t *testing.T) {
			ed := newPointerTestEditor(t, "zero\r\none\r\ntwo\r\nthree", 40, 8)
			x := ed.gutterWidth() + 1
			clicks := 3
			if gutter {
				x, clicks = ed.gutterGeometry().numberX, 1
			}
			for i := 0; i < clicks; i++ {
				ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft})
				if i+1 < clicks {
					ed.HandleEvent(tea.MouseReleaseMsg{X: x, Y: 1, Button: tea.MouseLeft})
				}
			}
			for _, step := range []struct {
				line int
				want string
				span []pointerPaintSpan
			}{
				{0, "zero\r\none\r\n", []pointerPaintSpan{{0, 0, 33}, {1, 0, 33}}},
				{2, "one\r\ntwo\r\n", []pointerPaintSpan{{1, 0, 33}, {2, 0, 33}}},
				{1, "one\r\n", []pointerPaintSpan{{1, 0, 33}}},
				{0, "zero\r\none\r\n", []pointerPaintSpan{{0, 0, 33}, {1, 0, 33}}},
			} {
				ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: step.line, Button: tea.MouseLeft})
				requirePointerSelectionPaint(t, ed, step.want, step.span...)
			}
		})
	}
}

func TestPointerBackwardReaderClearsOnReleaseAndIgnoresOtherButton(t *testing.T) {
	ed := newPointerTestEditor(t, "abcdef", 40, 8)
	ed.readOnly = true
	x := ed.gutterWidth()
	ed.HandleEvent(tea.MouseClickMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseMotionMsg{X: x + 1, Y: 0, Button: tea.MouseLeft})
	requirePointerSelectionPaint(t, ed, "bcd", pointerPaintSpan{0, 1, 4})
	if cmd := ed.HandleEvent(tea.MouseReleaseMsg{X: x, Y: 0, Button: tea.MouseRight}); cmd != nil || !ed.IsMouseDragging() {
		t.Fatal("unrelated button release must not end/copy the left-button gesture")
	}
	requirePointerSelectionPaint(t, ed, "bcd", pointerPaintSpan{0, 1, 4})
	// Do not execute the OS clipboard command in a unit test.
	if cmd := ed.HandleEvent(tea.MouseReleaseMsg{X: x + 1, Y: 0, Button: tea.MouseLeft}); cmd == nil || ed.IsMouseDragging() {
		t.Fatal("reader release must issue copy and stop the gesture")
	}
	requirePointerSelectionPaint(t, ed, "")
}
