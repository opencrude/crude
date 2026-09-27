package statusoverview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	ttypes "crude/types"
)

func testProjection() Projection {
	return Projection{Active: ViewTabs, Views: map[ViewID][]Item{
		ViewTabs: {
			{ID: "group", Label: "Agents", Header: true},
			{ID: "agent:one", Label: "One", Summary: "Waiting", Subtitle: "Ollama / glm", Lines: []DetailLine{{Label: "Context", Value: "71.5k / 1.05M · 6.8%", Role: ui.ColorFg}}, PrimaryAction: "agent:one", PrimaryLabel: "Switch to Tab", PrimaryEnabled: true},
			{ID: "agent:two", Label: "Two", Summary: "Idle", PrimaryAction: "agent:two", PrimaryLabel: "Switch to Tab", PrimaryEnabled: true},
		},
		ViewWorkspace:     {{ID: "workspace", Label: "Workspace", Summary: "Ready", Lines: []DetailLine{{Label: "Path", Value: "/tmp/project", Role: ui.ColorFg}}}},
		ViewNotifications: {{ID: "notice:n", Label: "Failed", Summary: "Unread", NotificationID: "n", NotificationUnread: true, Owner: ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: "one"}, Lines: []DetailLine{{Label: "Source", Value: "Agent", Role: ui.ColorMuted}}}},
	}}
}

func TestOverviewUsesFullWidthSearchAndList(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 28)
	host.Layout()
	if screen.views.Bounds() != (ui.Rect{X: 2, Y: 0, W: 96, H: 2}) {
		t.Fatalf("mode strip bounds=%+v", screen.views.Bounds())
	}
	if screen.search.Bounds() != (ui.Rect{X: 2, Y: 2, W: 96, H: 1}) {
		t.Fatalf("search is not full width: %+v", screen.search.Bounds())
	}
	if screen.list.Bounds().X != 2 || screen.list.Bounds().W < 94 {
		t.Fatalf("list is not full width: %+v", screen.list.Bounds())
	}
	if screen.primaryActions.Bounds().Y != 27 || screen.utilityActions.Bounds().Y != 27 {
		t.Fatalf("command row not flush with the last host row: %+v/%+v", screen.utilityActions.Bounds(), screen.primaryActions.Bounds())
	}
	buf := surface.NewCellBuffer(100, 28)
	host.Paint(buf)
	row := PlainRow(buf, 27, 100)
	if !strings.Contains(row, "[ Copy summary ]") || !strings.Contains(row, "[ Switch to Tab ]") || !strings.Contains(row, "[ Close ]") {
		t.Fatalf("command row missing stable groups: %q", row)
	}
}

func TestOverviewFrameDropsGapsUnderStripAndCommandRow(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th, testProjection())
	widget := ui.NewWindowFrameWidget(th, ui.NewID(ui.RootID, "status-overview-frame"), ui.FrameWindow, "Status Overview", screen, nil, true, 104, 30)
	surf := surface.NewSurface("frame", 104, 30, cell.TransparentBlank())
	widget.Render(surf, nil)
	buf := surf.Buffer()
	if row := PlainRow(buf, 3, 104); !strings.Contains(row, "Search status") {
		t.Fatalf("extra gap under the tab strip: row=%q", row)
	}
	if row := PlainRow(buf, 28, 104); !strings.Contains(row, "[ Copy summary ]") || !strings.Contains(row, "[ Close ]") {
		t.Fatalf("extra gap under the command row: row=%q", row)
	}
}

func TestOverviewClickTogglesInlineDetails(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 28)
	host.Layout()
	buf := surface.NewCellBuffer(100, 28)
	host.Paint(buf)
	if screen.IsExpanded("agent:one") {
		t.Fatal("first item starts expanded")
	}
	rowY := screen.list.Bounds().Y + 1
	result := host.Handle(tea.MouseClickMsg{X: screen.list.Bounds().X + 4, Y: rowY, Button: tea.MouseLeft})
	if !result.Handled || !screen.IsExpanded("agent:one") {
		t.Fatalf("click did not expand first item: result=%#v", result)
	}
	host.MarkLayout()
	host.Layout()
	buf = surface.NewCellBuffer(100, 28)
	host.Paint(buf)
	painted := bufferText(buf, 100, 28)
	if !strings.Contains(painted, "Context") || !strings.Contains(painted, "71.5k / 1.05M") || !strings.Contains(painted, "Ollama / glm") {
		t.Fatalf("expanded details missing:\n%s", painted)
	}
	result = host.Handle(tea.MouseClickMsg{X: screen.list.Bounds().X + 4, Y: rowY, Button: tea.MouseLeft})
	if !result.Handled || screen.IsExpanded("agent:one") {
		t.Fatalf("second click did not collapse first item: result=%#v", result)
	}
}

func TestOverviewKeyboardExpandsWithoutNavigating(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 28)
	host.Layout()
	host.Focus(ListID)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !result.Handled || !screen.IsExpanded("agent:one") {
		t.Fatalf("Enter did not expand selected row: %#v", result)
	}
	if result.Cmd != nil {
		if _, ok := result.Cmd().(NavigateMsg); ok {
			t.Fatal("row expansion unexpectedly navigated")
		}
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
	if !result.Handled || screen.IsExpanded("agent:one") {
		t.Fatalf("Left did not collapse selected row: %#v", result)
	}
}

func TestOverviewSearchMatchesAdditionalDetails(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	screen.query = "1.05m"
	screen.syncItems(false)
	if len(screen.list.list.Items()) != 1 || screen.list.list.Items()[0].ID != "agent:one" {
		t.Fatalf("detail search rows=%#v", screen.list.list.Items())
	}
}

func TestOverviewSeparatesExpansionFromNavigation(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	if !screen.Select("agent:two") {
		t.Fatal("could not select second item")
	}
	selected, _ := screen.Selected()
	if selected.ID != "agent:two" || !screen.IsExpanded("agent:two") {
		t.Fatalf("selected=%q expanded=%v", selected.ID, screen.IsExpanded("agent:two"))
	}
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "status.primary"})
	if result.Cmd == nil {
		t.Fatal("explicit navigation action produced no command")
	}
	msg, ok := result.Cmd().(NavigateMsg)
	if !ok || msg.Action != "agent:two" {
		t.Fatalf("navigation=%#v", result.Cmd())
	}
}

func TestOverviewExpandedUnreadNoticeMarksRead(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: string(ViewNotifications)})
	if result.Cmd == nil {
		t.Fatal("view change produced no command")
	}
	if screen.DisplayedUnreadID() != "" {
		t.Fatal("collapsed unread notice counts as displayed")
	}
	result = screen.HandleAction(ui.EventContext{}, ui.Action{ID: "status.item.toggle"})
	if result.Cmd == nil {
		t.Fatal("expanding unread notice produced no read command")
	}
	read, ok := result.Cmd().(MarkReadMsg)
	if !ok || read.ID != "n" {
		t.Fatalf("read command=%#v", result.Cmd())
	}
}

func TestExpandedAgentShowsAllUsageScopes(t *testing.T) {
	projection := testProjection()
	projection.Views[ViewTabs] = []Item{{
		ID: "agent:one", Label: "One", Summary: "Idle", Subtitle: "Ollama Cloud / glm-5.3",
		Lines: []DetailLine{{Label: "Context", Value: "71.5k / 1.05M · 6.8%", Role: ui.ColorFg}},
		Usage: []UsageSection{
			{ID: "agent", Label: "This Agent", Lines: []DetailLine{{Label: "Input", Value: "10", Role: ui.ColorFg}}},
			{ID: "session", Label: "Session", Lines: []DetailLine{{Label: "Input", Value: "20", Role: ui.ColorFg}}},
		},
		PrimaryAction: "agent:one", PrimaryLabel: "Switch to Tab", PrimaryEnabled: true,
	}}
	screen := New(theme.DefaultDark(), projection)
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 28)
	host.Layout()
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	host.MarkLayout()
	host.Layout()
	buf := surface.NewCellBuffer(100, 28)
	host.Paint(buf)
	painted := bufferText(buf, 100, 28)
	for _, want := range []string{"Context", "This Agent", "Input", "10", "Session", "20"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("expanded usage missing %q:\n%s", want, painted)
		}
	}
}

func PlainRow(buf *surface.CellBuffer, y, width int) string {
	var out strings.Builder
	for x := 0; x < width; x++ {
		out.WriteRune(buf.Get(x, y).Char)
	}
	return out.String()
}

func bufferText(buf *surface.CellBuffer, width, height int) string {
	var out strings.Builder
	for y := 0; y < height; y++ {
		out.WriteString(PlainRow(buf, y, width))
		out.WriteByte('\n')
	}
	return out.String()
}
