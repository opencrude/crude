package editor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func newPointerTestEditor(t testing.TB, text string, w, h int) *Editor {
	t.Helper()
	ed := NewPreview(theme.DefaultDark(), "pointer.txt", text, 1)
	ed.readOnly = false
	ed.SetStatusChrome(false)
	ed.Render(surface.NewSurface("pointer-test", w, h, cell.TransparentBlank()), ui.PaintContext{})
	t.Cleanup(ed.Close)
	return ed
}

func TestPointerDragReleaseUsesFinalEndpoint(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "editable", true: "reader"}[readOnly], func(t *testing.T) {
			ed := newPointerTestEditor(t, "alpha beta gamma\nnext line", 40, 8)
			ed.readOnly = readOnly
			x := ed.gutterWidth()
			ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
			ed.HandleEvent(tea.MouseMotionMsg{X: x + 4, Y: 0, Button: tea.MouseLeft})
			cmd := ed.HandleEvent(tea.MouseReleaseMsg{X: x + 10, Y: 0, Button: tea.MouseLeft})
			if ed.cursor != (Position{Line: 0, Col: 10}) {
				t.Fatalf("release caret=%v, want final endpoint (0,10)", ed.cursor)
			}
			if !readOnly && (cmd != nil || ed.SelectedText() != "alpha beta") {
				t.Fatalf("editable release selection=%q, copy command=%v", ed.SelectedText(), cmd != nil)
			}
			if readOnly && (cmd == nil || ed.HasSelection()) {
				t.Fatal("reader release must copy the final range and drop its highlight")
			}
		})
	}
}

func TestPointerCancelledGutterDoesNotPoisonTextDrag(t *testing.T) {
	for _, cancel := range []string{"capture", "selection"} {
		t.Run(cancel, func(t *testing.T) {
			ed := newPointerTestEditor(t, "alpha beta\nsecond line\nthird line", 40, 8)
			ed.HandleEvent(tea.MouseClickMsg{X: ed.gutterGeometry().numberX, Y: 1, Button: tea.MouseLeft})
			if cancel == "capture" {
				ed.CancelPointerCapture()
			} else {
				ed.ClearSelection()
			}
			x := ed.gutterWidth()
			ed.HandleEvent(tea.MouseClickMsg{X: x + 1, Y: 0, Button: tea.MouseLeft})
			ed.HandleEvent(tea.MouseMotionMsg{X: x + 4, Y: 0, Button: tea.MouseLeft})
			if got := ed.SelectedText(); got != "lph" {
				t.Fatalf("fresh text drag after cancelled gutter selected %q, want lph", got)
			}
		})
	}
}

func TestPointerMultiClickDragExtendsWholeUnits(t *testing.T) {
	for _, tc := range []struct {
		name               string
		clicks, endX, endY int
		want               string
	}{
		{"word forward", 2, 13, 0, "beta gamma"},
		{"word backward", 2, 2, 0, "alpha beta"},
		{"line forward", 3, 3, 1, "alpha beta gamma\nsecond line\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ed := newPointerTestEditor(t, "alpha beta gamma\nsecond line\nthird line", 40, 8)
			x := ed.gutterWidth()
			for i := 0; i < tc.clicks; i++ {
				ed.HandleEvent(tea.MouseClickMsg{X: x + 7, Y: 0, Button: tea.MouseLeft})
				if i+1 < tc.clicks {
					ed.HandleEvent(tea.MouseReleaseMsg{X: x + 7, Y: 0, Button: tea.MouseLeft})
				}
			}
			ed.HandleEvent(tea.MouseMotionMsg{X: x + tc.endX, Y: tc.endY, Button: tea.MouseLeft})
			if got := ed.SelectedText(); got != tc.want {
				t.Fatalf("held multi-click drag selected %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPointerWheelReprojectsHeldEndpoint(t *testing.T) {
	ed := newPointerTestEditor(t, strings.Repeat("alpha beta\n", 40), 40, 8)
	x := ed.gutterWidth()
	ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 1, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseMotionMsg{X: x + 5, Y: 2, Button: tea.MouseLeft})
	ed.HandleEvent(tea.MouseWheelMsg{X: x + 5, Y: 2, Button: tea.MouseWheelDown})
	if ed.cursor != (Position{Line: 5, Col: 5}) {
		t.Fatalf("wheel left drag caret at %v, want line now under stationary pointer (5,5)", ed.cursor)
	}
	if got := ed.SelectedText(); got != strings.Repeat("alpha beta\n", 4)+"alpha" {
		t.Fatalf("wheel drag selected %q", got)
	}
}

func TestPointerShiftGutterUsesCurrentSelectionAnchor(t *testing.T) {
	ed := newPointerTestEditor(t, "zero\none\ntwo\nthree\nfour\nfive", 40, 8)
	ed.SetSelection(2, 1, 3, 2)
	ed.HandleEvent(tea.MouseClickMsg{X: ed.gutterGeometry().numberX, Y: 4, Button: tea.MouseLeft, Mod: tea.ModShift})
	if got := ed.SelectedText(); got != "two\nthree\nfour\n" {
		t.Fatalf("shift gutter selected %q, want anchor at selected line two", got)
	}
}

func pointerRows(n int) string {
	var text strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&text, "row %03d alpha beta gamma\n", i)
	}
	return text.String()
}

func pointerTick(t *testing.T, ed *Editor, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("held edge drag did not schedule its next scroll")
	}
	msg := cmd()
	if _, ok := msg.(PointerScrollMsg); !ok {
		t.Fatalf("edge timer returned %T, want pointer scroll", msg)
	}
	return ed.HandleEvent(msg)
}

func TestPointerStationaryEdgeScrollsAndKeepsSelection(t *testing.T) {
	for _, kind := range []string{"text", "gutter"} {
		t.Run(kind, func(t *testing.T) {
			ed := newPointerTestEditor(t, pointerRows(100), 40, 8)
			x := ed.gutterWidth() + 4
			if kind == "gutter" {
				x = ed.gutterGeometry().numberX
			}
			ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 2, Button: tea.MouseLeft})
			cmd := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 10, Button: tea.MouseLeft})
			for i := 0; i < 3; i++ {
				cmd = pointerTick(t, ed, cmd)
				if ed.scrollY != (i+1)*2 {
					t.Fatalf("stationary edge scroll after %d ticks=%d, want %d", i+1, ed.scrollY, (i+1)*2)
				}
			}
			if kind == "text" && ed.cursor != (Position{Line: 13, Col: 4}) {
				t.Fatalf("scrolled pointer caret=%v, want bottom-visible line (13,4)", ed.cursor)
			}
			if kind == "text" {
				var selected strings.Builder
				selected.WriteString("002 alpha beta gamma\n")
				for i := 3; i < 13; i++ {
					fmt.Fprintf(&selected, "row %03d alpha beta gamma\n", i)
				}
				selected.WriteString("row ")
				if ed.SelectedText() != selected.String() {
					t.Fatalf("stationary drag selected %q, want literal rows 2 through 13", ed.SelectedText())
				}
			} else if ed.cursor != (Position{Line: 14}) || !strings.HasPrefix(ed.SelectedText(), "row 002") || !strings.HasSuffix(ed.SelectedText(), "row 013 alpha beta gamma\n") {
				t.Fatalf("gutter scroll lost whole-line endpoints: caret=%v selection=%q", ed.cursor, ed.SelectedText())
			}
			late := cmd()
			ed.HandleEvent(tea.MouseReleaseMsg{X: x, Y: 10, Button: tea.MouseLeft})
			before, selected := ed.scrollY, ed.SelectedText()
			if next := ed.HandleEvent(late); next != nil || ed.scrollY != before || ed.SelectedText() != selected {
				t.Fatal("late timer after release changed retained selection or scrolled")
			}
			if ed.IsMouseDragging() || !ed.HasSelection() || ed.Modified() {
				t.Fatal("release must keep editable selection, end dragging and leave bytes clean")
			}
		})
	}
}

func TestPointerScrollUpHorizontalAndBounds(t *testing.T) {
	t.Run("upward reversal", func(t *testing.T) {
		ed := newPointerTestEditor(t, pointerRows(50), 40, 8)
		ed.scrollY = 20
		x := ed.gutterWidth() + 4
		ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 3, Button: tea.MouseLeft})
		cmd := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: -3, Button: tea.MouseLeft})
		pointerTick(t, ed, cmd)
		if ed.scrollY != 18 || ed.cursor != (Position{Line: 18, Col: 4}) {
			t.Fatalf("upward drag scroll=%d caret=%v", ed.scrollY, ed.cursor)
		}
		if ed.selAnchor != (Position{Line: 23, Col: 5}) {
			t.Fatalf("backward anchor must include originally pressed 0: %v", ed.selAnchor)
		}
		requirePointerSelectionPaint(t, ed, "018 alpha beta gamma\nrow 019 alpha beta gamma\nrow 020 alpha beta gamma\nrow 021 alpha beta gamma\nrow 022 alpha beta gamma\nrow 0",
			pointerPaintSpan{18, 4, 33}, pointerPaintSpan{19, 0, 33}, pointerPaintSpan{20, 0, 33},
			pointerPaintSpan{21, 0, 33}, pointerPaintSpan{22, 0, 33}, pointerPaintSpan{23, 0, 5})
	})
	t.Run("horizontal scrollbar crossing", func(t *testing.T) {
		ed := newPointerTestEditor(t, strings.Repeat("abcdefghij", 20), 40, 8)
		x := ed.gutterWidth()
		ed.HandleEvent(tea.MouseClickMsg{X: x + 3, Y: 2, Button: tea.MouseLeft})
		cmd := ed.HandleEvent(tea.MouseMotionMsg{X: 45, Y: 7, Button: tea.MouseLeft})
		pointerTick(t, ed, cmd)
		if ed.scrollX <= 0 || ed.scrollY != 0 || ed.IsScrollbarDragging() || ed.cursor.Col <= 33 {
			t.Fatalf("crossing chrome lost selection/axis: x=%d y=%d caret=%v", ed.scrollX, ed.scrollY, ed.cursor)
		}
	})
	t.Run("top and bottom stop timers", func(t *testing.T) {
		ed := newPointerTestEditor(t, pointerRows(20), 40, 8)
		x := ed.gutterWidth() + 4
		ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 2, Button: tea.MouseLeft})
		if cmd := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: -20, Button: tea.MouseLeft}); cmd != nil || ed.scrollY != 0 {
			t.Fatal("top boundary scheduled runaway timer")
		}
		cmd := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 20, Button: tea.MouseLeft})
		for n := 0; cmd != nil && n < 8; n++ {
			cmd = pointerTick(t, ed, cmd)
		}
		if cmd != nil || ed.scrollY != 13 {
			t.Fatalf("bottom didn't stop, scroll=%d timer=%v", ed.scrollY, cmd != nil)
		}
	})
}

func TestPointerLateTickCannotRearmNewGestureOrOwner(t *testing.T) {
	for _, stop := range []string{"cancel", "reenter", "resize", "close", "new press", "keyboard"} {
		t.Run(stop, func(t *testing.T) {
			ed := newPointerTestEditor(t, pointerRows(60), 40, 8)
			x := ed.gutterWidth() + 4
			ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 2, Button: tea.MouseLeft})
			cmd := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 10, Button: tea.MouseLeft})
			late := cmd()
			switch stop {
			case "cancel":
				ed.CancelPointerCapture()
			case "reenter":
				ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 4, Button: tea.MouseLeft})
			case "resize":
				ed.Render(surface.NewSurface("resized", 30, 10, cell.TransparentBlank()), ui.PaintContext{})
			case "close":
				ed.Close()
			case "new press":
				ed.HandleEvent(tea.MouseClickMsg{X: x + 1, Y: 3, Button: tea.MouseLeft})
			case "keyboard":
				ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyLeft})
			}
			before := ed.scrollY
			if cmd := ed.HandleEvent(late); cmd != nil || before != ed.scrollY {
				t.Fatal("stale tick moved/rearmed changed gesture")
			}
			other := newPointerTestEditor(t, pointerRows(60), 40, 8)
			other.HandleEvent(tea.MouseClickMsg{X: x, Y: 2, Button: tea.MouseLeft})
			other.HandleEvent(tea.MouseMotionMsg{X: x, Y: 10, Button: tea.MouseLeft})
			if cmd := other.HandleEvent(late); cmd != nil || other.scrollY != 0 {
				t.Fatal("tick mutated another editor")
			}
		})
	}
}

func TestPointerTimerDoesNotMultiplyWithMotion(t *testing.T) {
	ed := newPointerTestEditor(t, pointerRows(100), 40, 8)
	x := ed.gutterWidth() + 4
	ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 2, Button: tea.MouseLeft})
	cmd := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 10, Button: tea.MouseLeft})
	for i := 0; i < 100; i++ {
		if next := ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 10, Button: tea.MouseLeft}); next != nil {
			t.Fatal("motion scheduled duplicate timer")
		}
	}
	if ed.scrollY != 0 {
		t.Fatal("scroll speed depends on motion delivery rate")
	}
	msg := cmd()
	if next := ed.HandleEvent(msg); next == nil {
		t.Fatal("first tick stopped unexpectedly")
	}
	if ed.scrollY != 2 {
		t.Fatalf("one timer scroll=%d, want 2", ed.scrollY)
	}
	if next := ed.HandleEvent(msg); next != nil || ed.scrollY != 2 {
		t.Fatal("duplicate tick advanced the current gesture")
	}
}

func TestPointerWordDragAnchorsFollowPeerDelta(t *testing.T) {
	ed, s := projectionEditor(t, "first line\nalpha beta gamma\nlast line\n")
	ed.SetStatusChrome(false)
	ed.Render(surface.NewSurface("peer", 40, 8, cell.TransparentBlank()), ui.PaintContext{})
	x := ed.gutterWidth()
	for i := 0; i < 2; i++ {
		ed.HandleEvent(tea.MouseClickMsg{X: x + 7, Y: 1, Button: tea.MouseLeft})
	}
	c := document.Change{DocumentID: s.ID, BaseRevision: 1, Owner: document.Owner{ClientID: "peer", Epoch: 1}, Edits: []document.Edit{{Start: 0, End: 0, Text: "peer added\n"}}}
	s.Text = []byte("peer added\nfirst line\nalpha beta gamma\nlast line\n")
	s.Revision = 2
	if err := ed.ApplyDocumentDelta(c, s); err != nil {
		t.Fatal(err)
	}
	ed.HandleEvent(tea.MouseMotionMsg{X: x + 13, Y: 1, Button: tea.MouseLeft})
	if got := ed.SelectedText(); got != "beta gamma" {
		t.Fatalf("peer delta detached original word anchor: %q", got)
	}
}

func TestPointerWordSelectionKeepsUnicodeClusters(t *testing.T) {
	ed := newPointerTestEditor(t, "cafe\u0301 👩‍💻 good\n", 40, 8)
	x := ed.gutterWidth()
	for i := 0; i < 2; i++ {
		ed.HandleEvent(tea.MouseClickMsg{X: x + 3, Y: 0, Button: tea.MouseLeft})
	}
	if got := ed.SelectedText(); got != "cafe\u0301" {
		t.Fatalf("accented word selection=%q", got)
	}
	ed.HandleEvent(tea.MouseMotionMsg{X: x + 6, Y: 0, Button: tea.MouseLeft})
	if got := ed.SelectedText(); got != "cafe\u0301 👩‍💻" {
		t.Fatalf("cluster word drag=%q", got)
	}
}

// Mutation is measured independently from painting. Both document sizes keep
// their width/source projections warm, as during a real stationary hold.
func BenchmarkPointerScrollMutation(b *testing.B) {
	for _, lines := range []int{200, 100000} {
		b.Run(fmt.Sprintf("lines_%d", lines), func(b *testing.B) {
			ed := newPointerTestEditor(b, strings.Repeat("alpha beta gamma\n", lines), 120, 40)
			x := ed.gutterWidth() + 5
			ed.HandleEvent(tea.MouseClickMsg{X: x, Y: 2, Button: tea.MouseLeft})
			ed.HandleEvent(tea.MouseMotionMsg{X: x, Y: 42, Button: tea.MouseLeft})
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ed.drag.y = 42
				if i%200 >= 100 {
					ed.drag.y = -3
				}
				ed.drag.tickPending = true
				ed.handlePointerScroll(PointerScrollMsg{owner: ed, epoch: ed.dragEpoch})
			}
		})
	}
}
