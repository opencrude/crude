package confirmation

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestConfirmationQuickKeysUseTypedActions(t *testing.T) {
	for _, test := range []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{key: tea.KeyPressMsg{Code: 'y', Text: "y"}, want: "allow"},
		{key: tea.KeyPressMsg{Code: 'a', Text: "a"}, want: "always"},
		{key: tea.KeyPressMsg{Code: 'n', Text: "n"}, want: "deny"},
	} {
		screen, host := testHost(t, Details{Question: "Proceed?", Operation: "write", Tool: "apply_patch"})
		result := host.Handle(test.key)
		if result.Cmd == nil {
			t.Fatalf("key %q did not emit a result", test.key.String())
		}
		message, ok := result.Cmd().(ResultMsg)
		if !ok || message.Target != screen || message.Choice != test.want {
			t.Fatalf("key %q result=%#v", test.key.String(), message)
		}
	}
}

func TestConfirmationEscapeCancelsTurnWithoutDenyingPermission(t *testing.T) {
	screen, host := testHost(t, Details{Question: "Proceed?", Operation: "write", Tool: "apply_patch"})
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Cmd == nil {
		t.Fatal("Escape did not emit a cancellation")
	}
	message, ok := result.Cmd().(CancelMsg)
	if !ok || message.Target != screen {
		t.Fatalf("Escape result = %#v, want CancelMsg for owning screen", message)
	}
}

func TestConfirmationPaintsRiskMetadataAndMouseActions(t *testing.T) {
	_, host := testHost(t, Details{
		Question: "Allow this operation?", Operation: "delete", Tool: "bash", Target: "/project/out",
		Risk: "destructive", Reason: "Removes generated files", Suggestion: "Review the target",
	})
	buf := surface.NewCellBuffer(100, 12)
	host.Paint(buf)
	for _, want := range []string{"Confirmation required", "DANGEROUS", "delete", "/project/out", "Review the target"} {
		if !bufferContains(buf, want) {
			t.Fatalf("confirmation paint missing %q:\n%s", want, bufferText(buf))
		}
	}

	click := findPainted(buf, "Allow (y)")
	if click.x < 0 {
		t.Fatalf("Allow button not painted:\n%s", bufferText(buf))
	}
	result := host.Handle(tea.MouseClickMsg{X: click.x, Y: click.y, Button: tea.MouseLeft})
	if result.Cmd == nil || result.Cmd().(ResultMsg).Choice != "allow" {
		t.Fatalf("mouse allow result=%#v", result)
	}
}

func TestConfirmationPaintsAcrossThemesAndSizes(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, size := range []ui.Size{{W: 42, H: 8}, {W: 120, H: 20}} {
			screen := New(th)
			screen.Reset(Details{Question: "Proceed?", Risk: "network", Tool: "fetch"})
			deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
			deck.Open(ui.PanelEntry{ID: "confirmation", Title: "Confirmation", Content: screen, Policy: ui.PanelBlocking})
			host := ui.NewHost(th, deck)
			host.Resize(size.W, size.H)
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			if !bufferContains(buf, "Confirmation") {
				t.Fatalf("confirmation did not paint at %#v", size)
			}
		}
	}
}

func bufferText(buf *surface.CellBuffer) string {
	text := ""
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			r := buf.Get(x, y).Char
			if r == 0 {
				r = ' '
			}
			text += string(r)
		}
		text += "\n"
	}
	return text
}

func testHost(t *testing.T, details Details) (*Screen, *ui.Host) {
	t.Helper()
	th := theme.DefaultDark()
	screen := New(th)
	screen.Reset(details)
	deck := ui.NewPanelDeck("deck", ui.NewTokenSet(th))
	deck.Open(ui.PanelEntry{ID: "confirmation", Title: "Confirmation", Content: screen, Policy: ui.PanelBlocking})
	host := ui.NewHost(th, deck)
	host.Resize(100, 12)
	host.Layout()
	if !host.Focus(ActionsID) {
		t.Fatal("could not focus confirmation actions")
	}
	return screen, host
}

func bufferContains(buf *surface.CellBuffer, want string) bool {
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
