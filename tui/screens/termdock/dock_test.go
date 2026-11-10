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
	host := ui.NewHost(th, dock)
	host.Resize(80, 12)
	host.Layout()

	if len(dock.actions) != 5 {
		t.Fatalf("header actions=%d want 5 (picker, tab, float, kill, hide)", len(dock.actions))
	}
	click := func(i int) ui.EventResult {
		r := dock.actions[i].rect
		header := dock.header.Bounds()
		result := host.Handle(tea.MouseClickMsg{Button: tea.MouseLeft, X: header.X + r.X + 1, Y: header.Y})
		host.Handle(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: header.X + r.X + 1, Y: header.Y})
		return result
	}
	button := dock.newButton.Bounds()
	res := host.Handle(tea.MouseClickMsg{Button: tea.MouseLeft, X: button.X + 1, Y: button.Y})
	host.Handle(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: button.X + 1, Y: button.Y})
	if res.Action == nil || res.Action.ID != ActionNew {
		t.Fatalf("new icon action=%+v want %s", res.Action, ActionNew)
	}
	res = click(1)
	if res.Action == nil || res.Action.ID != ActionOpenAsTab || res.Action.Payload != PanelTerminal {
		t.Fatal("terminal has no open-as-tab action", res)
	}
	res = click(2)
	if res.Action == nil || res.Action.ID != shellterm.ActionPromote {
		t.Fatalf("float icon action=%+v want %s", res.Action, shellterm.ActionPromote)
	}
	if id, _ := res.Action.Payload.(string); id != "t1" {
		t.Fatalf("float payload=%v want t1", res.Action.Payload)
	}
	res = click(3)
	if res.Action == nil || res.Action.ID != shellterm.ActionClose {
		t.Fatalf("close icon action=%+v want %s", res.Action, shellterm.ActionClose)
	}

	if got := dock.Active().Bounds().Y; got != dividerH+ui.StripHeight {
		t.Fatalf("terminal body starts at %d want tab strip below divider", got)
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

// A surface's header moves it to a tab or a window, naming which surface.
func TestDockSurfaceHeaderEmitsPlacementActions(t *testing.T) {
	dock := New(theme.DefaultDark())
	dock.SetPanel(PanelProblems, ui.NewLabel("problems/body", "Problems", theme.DefaultDark()))
	dock.ShowPanel(PanelProblems)
	host := ui.NewHost(theme.DefaultDark(), dock)
	host.Resize(80, 12)
	host.Layout()
	header := dock.header.Bounds()
	for index, want := range []string{ActionOpenAsTab, ActionOpenAsWindow, ActionHide} {
		r := dock.actions[index].rect
		res := host.Handle(tea.MouseClickMsg{Button: tea.MouseLeft, X: header.X + r.X + 1, Y: header.Y})
		host.Handle(tea.MouseReleaseMsg{Button: tea.MouseLeft, X: header.X + r.X + 1, Y: header.Y})
		if res.Action == nil || res.Action.ID != want {
			t.Fatalf("action %d = %+v, want %s", index, res.Action, want)
		}
		if want != ActionHide && res.Action.Payload != PanelProblems {
			t.Fatalf("%s payload = %v", want, res.Action.Payload)
		}
	}
}
