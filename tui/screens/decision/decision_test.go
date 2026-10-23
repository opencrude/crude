package decision

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestYoloKeyboardMouseAndEscapeActions(t *testing.T) {
	for _, test := range []struct {
		key     tea.KeyPressMsg
		enabled bool
	}{
		{key: tea.KeyPressMsg{Code: 'y', Text: "y"}, enabled: true},
		{key: tea.KeyPressMsg{Code: 'n', Text: "n"}, enabled: false},
		{key: tea.KeyPressMsg{Code: tea.KeyEsc}, enabled: false},
		{key: tea.KeyPressMsg{Code: tea.KeyEnter}, enabled: false},
	} {
		screen, host := testHost(t)
		result := host.Handle(test.key)
		if result.Cmd == nil {
			t.Fatalf("key %q did not emit a result", test.key.String())
		}
		message := result.Cmd().(ResultMsg)
		if message.Target != screen || message.Confirmed != test.enabled || message.Purpose != "yolo" {
			t.Fatalf("key %q result=%#v", test.key.String(), message)
		}
	}

	_, host := testHost(t)
	buf := surface.NewCellBuffer(100, 12)
	host.Paint(buf)
	click := findPainted(buf, "[ Enable YOLO")
	if click.x < 0 {
		t.Fatalf("Enable button not painted:\n%s", text(buf))
	}
	result := host.Handle(tea.MouseClickMsg{X: click.x, Y: click.y, Button: tea.MouseLeft})
	if result.Cmd == nil || !result.Cmd().(ResultMsg).Confirmed {
		t.Fatalf("mouse enable result=%#v", result)
	}
}

func TestYoloPaintsCompleteWarning(t *testing.T) {
	_, host := testHost(t)
	buf := surface.NewCellBuffer(100, 12)
	host.Paint(buf)
	for _, want := range []string{"Enable YOLO for this session?", "Approval prompts", "Skipped", "No confirmation", "Hard blocks", "Restore safeguards", "Enforced"} {
		if !contains(buf, want) {
			t.Fatalf("YOLO paint missing %q:\n%s", want, text(buf))
		}
	}
}

func TestYoloEnableRequiresExplicitSelection(t *testing.T) {
	_, host := testHost(t)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Cmd == nil || !result.Cmd().(ResultMsg).Confirmed {
		t.Fatal("explicit enable selection did not activate")
	}
}

func TestYoloPaintsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 8}, {W: 120, H: 20}} {
			screen := New(th)
			screen.Show(yoloSpec())
			deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
			deck.Open(ui.PanelEntry{ID: "yolo", Title: "YOLO", Content: screen, Policy: ui.PanelBlocking})
			host := ui.NewHost(th, deck)
			host.Resize(size.W, size.H)
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if !contains(buf, "YOLO") {
				t.Fatalf("YOLO did not paint at %#v", size)
			}
		}
	}
}

func text(buf *surface.CellBuffer) string {
	result := ""
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			result += string(r)
		}
		result += "\n"
	}
	return result
}

func yoloSpec() Spec {
	return Spec{
		Purpose: "yolo", Title: "Enable YOLO for this session?", Hint: "Use /yolo to restore approvals.",
		Facts:   []Fact{{"Approval prompts", "Skipped"}, {"Commands / edits", "No confirmation"}, {"Hard blocks", "Enforced"}, {"Restore safeguards", "Enforced"}},
		Confirm: Answer{Label: "Enable YOLO", Shortcut: "y"}, Cancel: Answer{Label: "Cancel", Shortcut: "n"}, ConfirmDanger: true,
	}
}

func testHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Show(yoloSpec())
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
	deck.Open(ui.PanelEntry{ID: "yolo", Title: "YOLO", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(th, deck)
	host.Resize(100, 12)
	host.Layout()
	if !host.Focus(ActionsID) {
		t.Fatal("could not focus YOLO actions")
	}
	return screen, host
}

func contains(buf *surface.CellBuffer, want string) bool {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for index, r := range runes {
				if buf.Get(x+index, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return true
			}
		}
	}
	return false
}

type paintedAt struct{ x, y int }

func findPainted(buf *surface.CellBuffer, want string) paintedAt {
	runes := []rune(want)
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x <= buf.Width()-len(runes); x++ {
			matched := true
			for index, r := range runes {
				if buf.Get(x+index, y).Char != r {
					matched = false
					break
				}
			}
			if matched {
				return paintedAt{x: x, y: y}
			}
		}
	}
	return paintedAt{x: -1, y: -1}
}

func TestAlternateAnswerIsExplicitAndDistinct(t *testing.T) {
	th := theme.DefaultDark()
	open := func() (*Screen, *ui.Host) {
		screen := New(th)
		screen.Show(Spec{Purpose: "implement_plan", Title: "Implement this plan?",
			Confirm:   Answer{Label: "Implement plan", Shortcut: "y"},
			Alternate: &Answer{Label: "Implement in new session", Shortcut: "c"},
			Cancel:    Answer{Label: "Keep planning", Shortcut: "n"}})
		deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
		deck.Open(ui.PanelEntry{ID: "plan", Title: "Plan", Content: screen, Policy: ui.PanelBlocking})
		host := ui.NewHost(th, deck)
		host.Resize(100, 10)
		host.Layout()
		if !host.Focus(ActionsID) {
			t.Fatal("could not focus actions")
		}
		return screen, host
	}
	for _, test := range []struct {
		key                  tea.KeyPressMsg
		confirmed, alternate bool
	}{
		{key: tea.KeyPressMsg{Code: 'c', Text: "c"}, confirmed: true, alternate: true},
		{key: tea.KeyPressMsg{Code: 'y', Text: "y"}, confirmed: true},
		{key: tea.KeyPressMsg{Code: tea.KeyEnter}},
		{key: tea.KeyPressMsg{Code: tea.KeyEsc}},
	} {
		_, host := open()
		result := host.Handle(test.key)
		if result.Cmd == nil {
			t.Fatalf("key %q emitted nothing", test.key.String())
		}
		if message := result.Cmd().(ResultMsg); message.Confirmed != test.confirmed || message.Alternate != test.alternate {
			t.Fatalf("key %q result=%#v", test.key.String(), message)
		}
	}
	_, host := open()
	buf := surface.NewCellBuffer(100, 10)
	host.Paint(buf)
	click := findPainted(buf, "[ Implement in new session")
	if click.x < 0 {
		t.Fatalf("alternate answer not painted:\n%s", text(buf))
	}
	result := host.Handle(tea.MouseClickMsg{X: click.x, Y: click.y, Button: tea.MouseLeft})
	if result.Cmd == nil || !result.Cmd().(ResultMsg).Alternate {
		t.Fatalf("click on the alternate answer = %#v", result)
	}
}
