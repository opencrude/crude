package toolsetup

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func setupFixture() types.ProjectToolReview {
	return types.ProjectToolReview{Project: "/project", Items: []types.ProjectToolCandidate{
		{ID: "selected", Package: "fixture-server", Version: "v1", URL: "https://example.invalid/server.zip",
			File: "server.zip", Command: "fixture-server", Languages: []string{"fixture"}, Licenses: []string{"MIT"}, Installable: true},
		{ID: "unsupported", Package: "unsupported", Version: "v2", Source: "fixture registry",
			Languages: []string{"example"}, Licenses: []string{"MIT"},
			Reason: "Install this tool yourself, then check again."},
	}}
}

func TestProjectToolsUnavailableOnlyExplainsNextStep(t *testing.T) {
	review := types.ProjectToolReview{Project: "/project", Items: []types.ProjectToolCandidate{{
		ID: "missing", Package: "missing-server", Version: "1.0", Source: "npm registry",
		URL: "pkg:npm/missing-server@1.0", Bin: "npm:missing-server", Command: "missing-server",
		Reason: "missing-server uses npm and needs Node.js. Crude cannot install it yet. Install it yourself, then check again.",
	}}}
	for _, size := range [][2]int{{100, 30}, {74, 20}} {
		screen := New(theme.DefaultDark(), review)
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.RegisterTree(screen)
		host.Resize(size[0], size[1])
		text := paintText(host, size[0], size[1])
		for _, want := range []string{"cannot install these tools", "npm registry", "Program: missing-server", "Not now"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
		for _, unwanted := range []string{"Select tools to install", "Install 0 tools", "Space: select", "pkg:npm", "npm:missing-server"} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("misleading detail %q at %v:\n%s", unwanted, size, text)
			}
		}
	}
}

func paintText(host *ui.Host, width, height int) string {
	host.Layout()
	buffer := surface.NewCellBuffer(width, height)
	host.Paint(buffer)
	var plain strings.Builder
	for y := 0; y < buffer.Height(); y++ {
		for x := 0; x < buffer.Width(); x++ {
			ch := buffer.Get(x, y).Char
			if ch == 0 {
				ch = ' '
			}
			plain.WriteRune(ch)
		}
		plain.WriteByte('\n')
	}
	return plain.String()
}

func TestProjectToolsSelectionAndDetailsUseNativeInput(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {74, 20}} {
		screen := New(theme.DefaultDark(), setupFixture())
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.RegisterTree(screen)
		host.Resize(size[0], size[1])
		host.Layout()
		host.Focus(ListID)
		text := paintText(host, size[0], size[1])
		for _, want := range []string{"[x]", "fixture-server", "Not available", "unsupported", "Install 1 tool", "Not now"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
		// Space changes the selected installable row and disables installation.
		host.Handle(tea.KeyPressMsg{Code: ' ', Text: " "})
		if len(screen.SelectedIDs()) != 0 || screen.actions.ActionEnabled(ActionInstall) {
			t.Fatal("Space did not deselect the installable tool")
		}
		// The unavailable row has no control, but its full reason is readable.
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		text = paintText(host, size[0], size[1])
		if !strings.Contains(text, "Install this tool yourself") {
			t.Fatalf("unavailable reason not visible:\n%s", text)
		}
		for _, want := range []string{"unsupported v2", "fixture registry", "example", "MIT"} {
			if !strings.Contains(screen.detail.Value(), want) {
				t.Fatalf("unavailable details omit %q: %s", want, screen.detail.Value())
			}
			if !strings.Contains(text, want) {
				t.Fatalf("unavailable details do not render %q at %v:\n%s", want, size, text)
			}
		}
		host.Handle(tea.KeyPressMsg{Code: ' ', Text: " "})
		if len(screen.SelectedIDs()) != 0 {
			t.Fatal("unavailable tool became selectable")
		}
		// Clicking the real first row toggles the same selection owner.
		r, ok := host.ScreenRect(ListID)
		if !ok {
			t.Fatal("candidate list is not mounted")
		}
		host.Handle(tea.MouseClickMsg{X: r.X + 3, Y: r.Y, Button: tea.MouseLeft})
		if got := screen.SelectedIDs(); len(got) != 1 || got[0] != "selected" {
			t.Fatalf("pointer did not restore the reviewed selection: %v", got)
		}
		result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionDecline})
		decision, ok := result.Cmd().(DecisionMsg)
		if !ok || len(decision.Selected) != 0 || decision.Target != screen {
			t.Fatal("Not now invented a selection")
		}
	}
}
