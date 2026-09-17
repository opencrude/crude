package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
)

func TestTextField_TypeAndSubmit(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)
	f := NewTextField("q", ts)
	f.SetPlaceholder("search…")
	f.Layout(LayoutContext{}, Rect{W: 20, H: 1})

	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'b', Text: "b"}))
	if f.Value() != "ab" {
		t.Fatal(f.Value())
	}
	res := f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
	if res.Action == nil || res.Action.ID != "input.submit" {
		t.Fatalf("%+v", res.Action)
	}
	if res.Action.Payload != "ab" {
		t.Fatal(res.Action.Payload)
	}
}

func TestTextFieldMouseDragSelectionUsesPointerCapture(t *testing.T) {
	f := NewTextField("q", nil)
	f.SetValue("abcdef")
	f.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	var copied []string
	f.SetClipboardCopy(func(text string) tea.Cmd {
		return func() tea.Msg {
			copied = append(copied, text)
			return nil
		}
	})
	clicked := f.Handle(EventContext{LocalX: 1}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	if clicked.Action == nil || clicked.Action.ID != "ui.capture" || !f.IsSelecting() {
		t.Fatalf("click did not capture selection: %+v", clicked)
	}
	f.Handle(EventContext{LocalX: 4}, MouseMotionEvent(tea.MouseMotionMsg{}))
	if got := f.SelectedText(); got != "bcd" {
		t.Fatalf("selected text=%q", got)
	}
	res := f.Handle(EventContext{}, MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft}))
	if f.IsSelecting() || f.HasSelection() {
		t.Fatal("release kept the drag selection")
	}
	if res.Cmd == nil {
		t.Fatal("release returned no clipboard command")
	}
	if len(copied) != 0 {
		t.Fatalf("clipboard write ran synchronously: copied=%q", copied)
	}
	_ = res.Cmd()
	if len(copied) != 1 || copied[0] != "bcd" {
		t.Fatalf("copied=%q want [bcd]", copied)
	}
}

// A press that never drags must not copy: it only ends the drag, so a
// shift-click or a plain click on an existing keyboard selection keeps it.
func TestTextFieldClickWithoutDragKeepsSelection(t *testing.T) {
	f := NewTextField("q", nil)
	f.SetValue("abcdef")
	f.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	f.Edit().SetCursor(1, false)
	f.Edit().SetCursor(4, true)
	f.Handle(EventContext{LocalX: 2}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, Mod: 0}))
	// Shift-click re-extends from the anchor instead of collapsing it.
	res := f.Handle(EventContext{}, MouseReleaseEvent(tea.MouseReleaseMsg{Button: tea.MouseLeft}))
	if res.Cmd != nil {
		t.Fatal("click without drag returned a clipboard command")
	}
}

func TestFocusedEmptyTextFieldOverlaysBlockCursorWithoutShiftingPlaceholder(t *testing.T) {
	tokens := NewTokenSet(theme.DefaultDark())
	f := NewTextField("q", tokens)
	f.SetPlaceholder("Filter")
	f.cursor.MarkActivity()
	f.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	buf := surface.NewCellBuffer(10, 1)
	f.Paint(buf, 0, 0, PaintContext{Focused: true})
	if buf.Get(0, 0).Char != 'F' || buf.Get(1, 0).Char != 'i' {
		t.Fatalf("focused placeholder cells=%q %q", buf.Get(0, 0).Char, buf.Get(1, 0).Char)
	}
	if !buf.Get(0, 0).Attr.Has(cell.AttrReverse) {
		t.Fatalf("placeholder cursor attrs=%v want reverse block", buf.Get(0, 0).Attr)
	}
}

func TestTextFieldBlockCursorPreservesOccupiedGlyphAndEndGeometry(t *testing.T) {
	f := NewTextField("q", NewTokenSet(theme.DefaultDark()))
	f.SetValue("abc")
	f.Edit().SetCursor(1, false)
	f.cursor.MarkActivity()
	f.Layout(LayoutContext{}, Rect{W: 8, H: 1})
	buf := surface.NewCellBuffer(8, 1)
	f.Paint(buf, 0, 0, PaintContext{Focused: true})
	if c := buf.Get(1, 0); c.Char != 'b' || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("occupied cursor cell=%+v want preserved b with reverse", c)
	}
	if buf.Get(0, 0).Char != 'a' || buf.Get(2, 0).Char != 'c' {
		t.Fatal("cursor changed occupied text geometry")
	}

	f.Edit().MoveEnd(false)
	buf = surface.NewCellBuffer(8, 1)
	f.Paint(buf, 0, 0, PaintContext{Focused: true})
	if c := buf.Get(3, 0); c.Char != ' ' || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("end cursor cell=%+v want reverse blank overlay", c)
	}
	if buf.Get(0, 0).Char != 'a' || buf.Get(2, 0).Char != 'c' {
		t.Fatal("end cursor consumed text width")
	}
}

func TestTextField_PasteAndUnicode(t *testing.T) {
	f := NewTextField("q", nil)
	f.Layout(LayoutContext{}, Rect{W: 30, H: 1})
	f.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: "日本語test"}))
	if f.Value() != "日本語test" {
		t.Fatal(f.Value())
	}
	// backspace removes one rune
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyBackspace}))
	if f.Value() != "日本語tes" {
		t.Fatal(f.Value())
	}
}

func TestTextField_MousePositionsCursor(t *testing.T) {
	f := NewTextField("q", nil)
	f.SetValue("hello")
	f.Layout(LayoutContext{}, Rect{W: 20, H: 1})
	f.Handle(EventContext{LocalX: 2, LocalY: 0, Focused: true}, MouseClickEvent(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft}))
	if f.Edit().Cursor() != 2 {
		t.Fatalf("cur=%d", f.Edit().Cursor())
	}
}

func TestTextField_PaintPlaceholderAndValue(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)
	f := NewTextField("q", ts)
	f.SetPrompt(">")
	f.SetPlaceholder("type here")
	f.Layout(LayoutContext{}, Rect{W: 20, H: 1})
	buf := surface.NewCellBuffer(20, 1)
	f.Paint(buf, 0, 0, PaintContext{Theme: th, Focused: true})
	// prompt at 0
	if buf.Get(0, 0).Char != '>' {
		t.Fatalf("prompt %c", buf.Get(0, 0).Char)
	}
	// type and repaint
	f.SetValue("xy")
	buf.Clear()
	f.Paint(buf, 0, 0, PaintContext{Theme: th, Focused: true})
	if buf.Get(1, 0).Char != 'x' {
		t.Fatalf("value %c", buf.Get(1, 0).Char)
	}
}

func TestTextField_PromptEndPaintsGlyphAfterValue(t *testing.T) {
	th := theme.DefaultDark()
	ts := NewTokenSet(th)
	f := NewTextField("q", ts)
	f.SetPrompt(" ⌕")
	f.SetPromptPlacement(PromptEnd)
	f.SetValue("xy")
	f.Layout(LayoutContext{}, Rect{W: 20, H: 1})
	buf := surface.NewCellBuffer(20, 1)
	f.Paint(buf, 0, 0, PaintContext{Theme: th, Focused: true})
	if buf.Get(0, 0).Char != 'x' || buf.Get(1, 0).Char != 'y' {
		t.Fatalf("value should start at column 0, got %q%q", buf.Get(0, 0).Char, buf.Get(1, 0).Char)
	}
	if buf.Get(18, 0).Char != ' ' || buf.Get(19, 0).Char != '⌕' {
		t.Fatalf("trailing prompt got %q%q", buf.Get(18, 0).Char, buf.Get(19, 0).Char)
	}
}

func TestTextField_ReadOnlyDisabled(t *testing.T) {
	f := NewTextField("q", nil)
	f.SetValue("x")
	f.SetReadOnly(true)
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	if f.Value() != "x" {
		t.Fatal("readonly")
	}
	f.SetReadOnly(false)
	f.SetDisabled(true)
	res := f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Text: "a"}))
	if res.Handled {
		t.Fatal("disabled ignores")
	}
}

func TestTextField_CtrlASelectAll(t *testing.T) {
	f := NewTextField("q", nil)
	f.SetValue("hello")
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}))
	if !f.Edit().HasSelection() || f.Edit().SelectedText() != "hello" {
		t.Fatal(f.Edit().SelectedText())
	}
}

func TestTextFieldMatureEditingContract(t *testing.T) {
	f := NewTextField("field", NewTokenSet(theme.DefaultDark()))
	f.Layout(LayoutContext{}, Rect{W: 8, H: 1})
	f.SetValue("one two")
	f.Edit().SetCursor(4, false)
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModCtrl}))
	if got := f.Value(); got != "one " {
		t.Fatalf("ctrl+delete=%q", got)
	}
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}))
	if got := f.Value(); got != "one two" {
		t.Fatalf("undo=%q", got)
	}
	f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift}))
	if got := f.Value(); got != "one " {
		t.Fatalf("redo=%q", got)
	}

	f.SetValue("alpha beta")
	click := MouseClickEvent(tea.MouseClickMsg{X: 2, Button: tea.MouseLeft})
	f.Handle(EventContext{LocalX: 2}, click)
	f.Handle(EventContext{LocalX: 2}, click)
	if got := f.SelectedText(); got != "alpha" {
		t.Fatalf("double-click selection=%q", got)
	}
	if res := f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})); !res.Handled || res.Cmd == nil {
		t.Fatalf("ctrl+c result=%+v", res)
	}
	f.Handle(EventContext{LocalX: 2}, click)
	if got := f.SelectedText(); got != "alpha beta" {
		t.Fatalf("triple-click selection=%q", got)
	}
	if res := f.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})); !res.Handled || res.Cmd == nil || f.Value() != "" {
		t.Fatalf("ctrl+x result=%+v value=%q", res, f.Value())
	}
}

func TestTextFieldDragOutsideAutoscrollsSelection(t *testing.T) {
	f := NewTextField("field", nil)
	f.SetValue("0123456789abcdef")
	f.Layout(LayoutContext{}, Rect{W: 5, H: 1})
	f.Edit().SetCursor(0, false)
	f.Handle(EventContext{LocalX: 0}, MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
	for range 12 {
		f.Handle(EventContext{LocalX: 12}, MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
	}
	if f.ViewStart() == 0 || !f.HasSelection() {
		t.Fatalf("outside drag viewStart=%d selection=%q", f.ViewStart(), f.SelectedText())
	}
}

func TestTextFieldWideGraphemeCursorAndShiftClick(t *testing.T) {
	f := NewTextField("field", NewTokenSet(theme.DefaultDark()))
	f.SetValue("界alpha")
	f.Edit().SetCursor(0, false)
	f.Layout(LayoutContext{}, Rect{W: 10, H: 1})
	f.cursor.MarkActivity()
	buf := surface.NewCellBuffer(10, 1)
	f.Paint(buf, 0, 0, PaintContext{Focused: true})
	if c := buf.Get(0, 0); c.Content != "界" || c.Width != 2 || !c.Attr.Has(cell.AttrReverse) {
		t.Fatalf("wide cursor cell=%+v", c)
	}
	f.Handle(EventContext{LocalX: 2}, MouseClickEvent(tea.MouseClickMsg{X: 2, Button: tea.MouseLeft}))
	f.Handle(EventContext{LocalX: 5}, MouseClickEvent(tea.MouseClickMsg{X: 5, Button: tea.MouseLeft, Mod: tea.ModShift}))
	if got := f.SelectedText(); got != "alp" {
		t.Fatalf("shift-click selection=%q", got)
	}
}
