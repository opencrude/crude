package termdock

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellterm"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestDockAttentionSetAndClearedOnActivate(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	a := shellterm.New(th, "t1", "one")
	b := shellterm.New(th, "t2", "two")
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	dock.Mount("t1", a)
	dock.Mount("t2", b)

	// t2 is active; a bell in t1 flags it.
	dock.SetAttention("t1", true)
	if !dock.HasAttention("t1") {
		t.Fatal("attention not recorded for background tab")
	}
	// Bells on the active tab are already seen: never flagged.
	dock.SetAttention("t2", true)
	if dock.HasAttention("t2") {
		t.Fatal("active tab must not collect attention")
	}
	if !dock.SetActive("t1") {
		t.Fatal("SetActive failed")
	}
	if dock.HasAttention("t1") {
		t.Fatal("activating the tab must clear its attention flag")
	}
}

func TestDockHeaderActionsEmitPanelCommands(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	a := shellterm.New(th, "t1", "one")
	t.Cleanup(a.Close)
	dock.Mount("t1", a)
	dock.Layout(ui.LayoutContext{}, ui.Rect{X: 0, Y: 0, W: 80, H: 12})

	if len(dock.actions) != 3 {
		t.Fatalf("header actions=%d want 3", len(dock.actions))
	}
	click := func(i int) ui.EventResult {
		r := dock.actions[i].rect
		return dock.Handle(ui.EventContext{LocalX: r.X + 1, LocalY: r.Y},
			ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft, X: r.X + 1, Y: r.Y}))
	}
	res := click(0)
	if res.Action == nil || res.Action.ID != ActionNew {
		t.Fatalf("new icon action=%+v want %s", res.Action, ActionNew)
	}
	res = click(1)
	if res.Action == nil || res.Action.ID != shellterm.ActionPromote {
		t.Fatalf("float icon action=%+v want %s", res.Action, shellterm.ActionPromote)
	}
	if id, _ := res.Action.Payload.(string); id != "t1" {
		t.Fatalf("float payload=%v want t1", res.Action.Payload)
	}
	res = click(2)
	if res.Action == nil || res.Action.ID != shellterm.ActionClose {
		t.Fatalf("close icon action=%+v want %s", res.Action, shellterm.ActionClose)
	}

	// Tabs must not extend under the header actions.
	tabsRight := dock.tabs.Bounds().X + dock.tabs.Bounds().W
	if tabsRight > dock.actions[0].rect.X {
		t.Fatalf("tabs end %d overlaps actions at %d", tabsRight, dock.actions[0].rect.X)
	}
}

func TestDockAttentionClearedOnUnmount(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	a := shellterm.New(th, "t1", "one")
	b := shellterm.New(th, "t2", "two")
	t.Cleanup(a.Close)
	t.Cleanup(b.Close)
	dock.Mount("t1", a)
	dock.Mount("t2", b)
	dock.SetAttention("t1", true)
	dock.Unmount("t1")
	if dock.HasAttention("t1") {
		t.Fatal("unmount left a stale attention flag")
	}
}
