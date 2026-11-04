package toolsetup

import (
	"slices"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func setupFixture() types.ProjectToolReview {
	return types.ProjectToolReview{Project: "/project", Items: []types.ProjectToolCandidate{
		{ID: "download", Package: "fixture-server", Version: "v1", Source: "github", URL: "https://example.invalid/server.zip",
			File: "server.zip", Command: "fixture-server", Languages: []string{"fixture"}, Licenses: []string{"MIT"}, Installable: true},
		{ID: "manager", Package: "package-manager-server", Languages: []string{"other"},
			Reason: "Crude cannot download this tool itself.", Installable: false},
	}}
}

// Every offered tool is a working choice. A tool that Crude cannot download
// itself stays on: Aeron works out how to get it.
func TestEveryToolIsARealChoiceAndStartsOn(t *testing.T) {
	screen := New(theme.DefaultDark(), setupFixture())
	if got := screen.SelectedIDs(); !slices.Equal(got, []string{"download", "manager"}) {
		t.Fatalf("both tools must start on, got %v", got)
	}
	screen.model.SetValue("download", ui.StringValue("off"))
	if got := screen.SelectedIDs(); !slices.Equal(got, []string{"manager"}) {
		t.Fatalf("switching a tool off must remove only that tool, got %v", got)
	}
	screen.model.SetValue("manager", ui.StringValue("off"))
	if len(screen.SelectedIDs()) != 0 {
		t.Fatal("all switches off must select nothing")
	}
	if result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: ActionInstall}); result.Cmd != nil {
		t.Fatal("an empty selection sent a work order")
	}
}

func TestSetUpSendsExactlyTheSwitchedOnTools(t *testing.T) {
	screen := New(theme.DefaultDark(), setupFixture())
	screen.model.SetValue("download", ui.StringValue("off"))
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
		for _, want := range []string{"Project", "fixture-server", "package-manager-server", "Set up with Aeron", "Not now"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
	}
}
