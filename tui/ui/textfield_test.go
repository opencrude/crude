package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

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
