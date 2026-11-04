package toolsetup

import (
	"slices"
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
		{ID: "download", Package: "fixture-server", Version: "v1", Source: "github", URL: "https://example.invalid/server.zip",
			File: "server.zip", Command: "fixture-server", Languages: []string{"fixture"}, Licenses: []string{"MIT"}, Installable: true},
		{ID: "manager", Package: "package-manager-server", Version: "v2", Source: "fixture registry",
			Languages: []string{"example"}, Licenses: []string{"MIT"},
			Reason: "Install this tool yourself, then check again."},
	}}
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

// Every offered tool is a working choice and starts selected. A tool that
// Crude cannot download itself stays a choice: Aeron works out how to get it.
func TestEveryToolIsARealChoiceAndStartsOn(t *testing.T) {
	screen := New(theme.DefaultDark(), setupFixture())
	if got := screen.SelectedIDs(); !slices.Equal(got, []string{"download", "manager"}) {
		t.Fatalf("both tools must start selected, got %v", got)
	}
	toggle := func(id string) {
		screen.HandleAction(ui.EventContext{}, ui.Action{ID: rowActivate, Payload: ui.ListItem{ID: id}})
	}
	toggle("download")
	if got := screen.SelectedIDs(); !slices.Equal(got, []string{"manager"}) {
		t.Fatalf("clearing one tool must remove only that tool, got %v", got)
	}
	toggle("manager")
	if len(screen.SelectedIDs()) != 0 || screen.actions.ActionEnabled(ActionInstall) {
		t.Fatal("an empty selection must disable Set up with Aeron")
	}
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInstall}); result.Cmd != nil {
		t.Fatal("an empty selection sent a work order")
	}
}

func TestSetUpSendsExactlyTheSelectedTools(t *testing.T) {
	screen := New(theme.DefaultDark(), setupFixture())
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: rowActivate, Payload: ui.ListItem{ID: "download"}})
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInstall})
	decision, ok := result.Cmd().(DecisionMsg)
	if !ok || decision.Target != screen || !slices.Equal(decision.Selected, []string{"manager"}) {
		t.Fatalf("Set up with Aeron sent %+v", decision)
	}
	result = screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionDecline})
	decision, ok = result.Cmd().(DecisionMsg)
	if !ok || len(decision.Selected) != 0 || decision.Target != screen {
		t.Fatal("Not now invented a selection")
	}
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
		for _, want := range []string{"Project: /project", "[x]", "fixture-server", "package-manager-server", "Set up with Aeron", "Not now"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
		for _, unwanted := range []string{"Not available", "Install 0 tools", "cannot install these tools"} {
			if strings.Contains(text, unwanted) {
				t.Fatalf("stale catalog-only wording %q at %v:\n%s", unwanted, size, text)
			}
		}
		// Space clears the first row through the shared List.
		host.Handle(tea.KeyPressMsg{Code: ' ', Text: " "})
		if got := screen.SelectedIDs(); !slices.Equal(got, []string{"manager"}) {
			t.Fatalf("Space did not clear the first tool: %v", got)
		}
		// The second row explains that Aeron gets the tool, not the user.
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		text = paintText(host, size[0], size[1])
		for _, want := range []string{"package-manager-server v2", "fixture registry", "example", "MIT", "Aeron finds another way"} {
			if !strings.Contains(screen.detail.Value(), want) {
				t.Fatalf("details omit %q: %s", want, screen.detail.Value())
			}
		}
		if !strings.Contains(text, "Aeron finds") {
			t.Fatalf("Aeron detail not rendered at %v:\n%s", size, text)
		}
		if strings.Contains(screen.detail.Value(), "Install this tool yourself") {
			t.Fatal("details still tell the user to install the tool by hand")
		}
		host.Handle(tea.KeyPressMsg{Code: ' ', Text: " "})
		if len(screen.SelectedIDs()) != 0 {
			t.Fatal("Space did not clear the second tool")
		}
		// Clicking the real first row toggles the same selection owner.
		r, ok := host.ScreenRect(ListID)
		if !ok {
			t.Fatal("candidate list is not mounted")
		}
		host.Handle(tea.MouseClickMsg{X: r.X + 3, Y: r.Y, Button: tea.MouseLeft})
		if got := screen.SelectedIDs(); !slices.Equal(got, []string{"download"}) {
			t.Fatalf("pointer did not select the first tool: %v", got)
		}
	}
}
