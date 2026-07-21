package yolo

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
	} {
		screen, host := testHost(t)
		result := host.Handle(test.key)
		if result.Cmd == nil {
			t.Fatalf("key %q did not emit a result", test.key.String())
		}
		message := result.Cmd().(ResultMsg)
		if message.Target != screen || message.Enabled != test.enabled {
			t.Fatalf("key %q result=%#v", test.key.String(), message)
		}
	}

	_, host := testHost(t)
	result := host.Handle(tea.MouseClickMsg{X: 1, Y: 11, Button: tea.MouseLeft})
	if result.Cmd == nil || !result.Cmd().(ResultMsg).Enabled {
		t.Fatalf("mouse enable result=%#v", result)
	}
}

func TestYoloPaintsCompleteWarning(t *testing.T) {
	_, host := testHost(t)
	buf := surface.NewCellBuffer(100, 12)
	host.Paint(buf)
	for _, want := range []string{"YOLO mode", "Disables all safety checks", "Prompt injection", "Unintended command execution"} {
		if !contains(buf, want) {
			t.Fatalf("YOLO paint missing %q:\n%s", want, text(buf))
		}
	}
}

func TestYoloPaintsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 8}, {W: 120, H: 20}} {
			screen := New(th)
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

func testHost(t *testing.T) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
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
