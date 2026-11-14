package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
)

type textSelectionFixture struct {
	host   *Host
	list   *List
	copied []string
	buf    *surface.CellBuffer
}

func newTextSelectionFixture(t *testing.T, enabled bool) *textSelectionFixture {
	t.Helper()
	th := theme.DefaultDark()
	list := NewList("rows", NewTokenSet(th))
	list.SetActivateAction("rows.choose")
	list.SetItems([]ListItem{{ID: "a", Label: "alpha choice"}, {ID: "b", Label: "beta choice"}, {ID: "c", Label: "gamma choice"}})
	f := &textSelectionFixture{list: list, buf: surface.NewCellBuffer(24, 3)}
	f.host = NewHost(th, list)
	f.host.SetPointerTextSelection(enabled)
	f.host.SetClipboardCopy(func(text string) tea.Cmd {
		f.copied = append(f.copied, text)
		return nil
	})
	f.host.Resize(24, 3)
	f.host.Layout()
	f.host.Paint(f.buf)
	return f
}

func TestHostDragSelectsPaintedListTextWithoutChoosing(t *testing.T) {
	f := newTextSelectionFixture(t, true)
	press := f.host.Handle(tea.MouseClickMsg{X: 2, Y: 0, Button: tea.MouseLeft})
	if press.Action != nil {
		t.Fatalf("press chose %q before the gesture ended", press.Action.ID)
	}
	f.host.Handle(tea.MouseMotionMsg{X: 5, Y: 1, Button: tea.MouseLeft})
	f.host.Paint(f.buf)
	if text, ok := f.host.PointerTextSelection(); !ok || text == "" {
		t.Fatalf("drag selection = %q, %v", text, ok)
	}
	if got := f.buf.Get(3, 0); got.BG != f.host.Tokens().Color(ColorTextSelection) {
		t.Fatalf("selected cell is not marked: %+v", got)
	}
	release := f.host.Handle(tea.MouseReleaseMsg{X: 5, Y: 1, Button: tea.MouseLeft})
	if release.Action != nil {
		t.Fatalf("selecting row text chose %q", release.Action.ID)
	}
	want := "lpha choice\n beta" // rows paint after a one-cell inset
	if len(f.copied) != 1 || f.copied[0] != want {
		t.Fatalf("copied %q, want %q", f.copied, want)
	}
	if f.host.HasPointerCapture() {
		t.Fatal("release kept pointer capture")
	}
	f.host.Paint(f.buf)
	if got := f.buf.Get(3, 0); got.BG == f.host.Tokens().Color(ColorTextSelection) {
		t.Fatal("selection mark stayed after release")
	}
}

func TestHostClickOnSelectableTextStillActivates(t *testing.T) {
	f := newTextSelectionFixture(t, true)
	f.host.Handle(tea.MouseClickMsg{X: 2, Y: 1, Button: tea.MouseLeft})
	res := f.host.Handle(tea.MouseReleaseMsg{X: 2, Y: 1, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "rows.choose" {
		t.Fatalf("click action = %+v, want rows.choose", res.Action)
	}
	if item, ok := res.Action.Payload.(ListItem); !ok || item.ID != "b" {
		t.Fatalf("click chose %+v, want b", res.Action.Payload)
	}
	if len(f.copied) != 0 {
		t.Fatalf("click copied %q", f.copied)
	}
}

func TestHostWithoutTextSelectionKeepsPressActivation(t *testing.T) {
	f := newTextSelectionFixture(t, false)
	res := f.host.Handle(tea.MouseClickMsg{X: 2, Y: 2, Button: tea.MouseLeft})
	if res.Action == nil || res.Action.ID != "rows.choose" {
		t.Fatalf("press action = %+v, want rows.choose", res.Action)
	}
}
