package toolsetup

import (
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func setupFixture() types.ProjectToolReview {
	return types.ProjectToolReview{Project: "/project", Items: []types.ProjectToolCandidate{
		{ID: "selected", Package: "fixture-server", Version: "v1", URL: "https://example.invalid/server.zip",
			File: "server.zip", Command: "fixture-server", Languages: []string{"fixture"}, Licenses: []string{"MIT"}, Installable: true},
		{ID: "unsupported", Package: "unsupported", Reason: "Install this tool yourself, then check again."},
	}}
}

func TestProjectToolsSelectionUsesExactReviewedInstallableItems(t *testing.T) {
	screen := New(theme.DefaultDark(), setupFixture())
	if got := screen.SelectedIDs(); len(got) != 1 || got[0] != "selected" {
		t.Fatal("default selection does not match installable reviewed items", got)
	}
	screen.model.SetValue("selected", ui.StringValue("off"))
	screen.model.SetValue("unsupported", ui.StringValue("on"))
	if len(screen.SelectedIDs()) != 0 {
		t.Fatal("an unsupported or deselected item can be installed")
	}
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInstall}); result.Cmd != nil {
		t.Fatal("empty selection emitted an install")
	}
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionDecline})
	decision, ok := result.Cmd().(DecisionMsg)
	if !ok || len(decision.Selected) != 0 || decision.Target != screen {
		t.Fatal("Not now invented a selection")
	}
}

func TestProjectToolsPreviewRendersAtDesktopAndConstrainedSizes(t *testing.T) {
	for _, size := range [][2]int{{100, 30}, {74, 20}} {
		screen := New(theme.DefaultDark(), setupFixture())
		host := ui.NewHost(theme.DefaultDark(), screen)
		host.RegisterTree(screen)
		host.Resize(size[0], size[1])
		host.Layout()
		buffer := surface.NewCellBuffer(size[0], size[1])
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
		text := plain.String()
		for _, want := range []string{"Project", "fixture-server", "Install selected", "Not now"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
	}
}
