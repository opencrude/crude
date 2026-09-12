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
			{ID: "agent:one", Label: "One", Summary: "Waiting", PrimaryAction: "agent:one", PrimaryLabel: "Switch to Tab", PrimaryEnabled: true},
			{ID: "agent:two", Label: "Two", Summary: "Idle", PrimaryAction: "agent:two", PrimaryLabel: "Switch to Tab", PrimaryEnabled: true},
		},
		ViewWorkspace:     {{ID: "workspace", Label: "Workspace", Summary: "Ready"}},
		ViewNotifications: {{ID: "notice:n", Label: "Failed", Summary: "Unread", NotificationID: "n", NotificationUnread: true, Owner: ttypes.ResourceRef{Kind: ttypes.ResourceAgent, ID: "one"}}},
	}}
}

func TestOverviewRegionsAndActionsDoNotOverlap(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 28)
	host.Layout()
	if screen.views.Bounds() != (ui.Rect{X: 2, Y: 0, W: 96, H: 2}) {
		t.Fatalf("mode strip bounds=%+v", screen.views.Bounds())
	}
	if screen.search.Bounds().X != screen.views.Bounds().X {
		t.Fatalf("first tab/search rails differ: tabs=%+v search=%+v", screen.views.Bounds(), screen.search.Bounds())
	}
	if screen.search.Bounds().Y != screen.views.Bounds().Y+screen.views.Bounds().H {
		t.Fatalf("gap under tab strip: tabs=%+v search=%+v", screen.views.Bounds(), screen.search.Bounds())
	}
	left, right := screen.PaneBounds()
	if left.W != 28 || right.W < 48 {
		t.Fatalf("default panes are not navigation/detail weighted: left=%+v right=%+v", left, right)
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
	if strings.Contains(row, strings.Repeat("-", 8)) {
		t.Fatalf("separator overlaps command row: %q", row)
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

func TestOverviewDividerKeyboardAndPointerResizePaintedPanes(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	host := ui.NewHost(theme.DefaultDark(), screen)
	host.Resize(100, 28)
	host.Layout()
	before, _ := screen.PaneBounds()
	host.Focus(DividerID)
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !result.Handled {
		t.Fatal("focused divider ignored Right")
	}
	host.MarkLayout()
	host.Layout()
	afterKey, _ := screen.PaneBounds()
	if afterKey.W != before.W+1 {
		t.Fatalf("keyboard resize did not move painted pane: before=%+v after=%+v", before, afterKey)
	}
	divider := screen.divider.Bounds()
	pressX, pressY := divider.X+divider.W/2, divider.Y+1
	result = host.Handle(tea.MouseClickMsg{X: pressX, Y: pressY, Button: tea.MouseLeft})
	if !result.Handled || !host.HasPointerCapture() {
		t.Fatalf("divider press did not capture pointer: result=%#v", result)
	}
	host.Handle(tea.MouseMotionMsg{X: pressX + 5, Y: pressY, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: pressX + 5, Y: pressY, Button: tea.MouseLeft})
	host.MarkLayout()
	host.Layout()
	afterPointer, _ := screen.PaneBounds()
	if afterPointer.W <= afterKey.W || host.HasPointerCapture() {
		t.Fatalf("pointer resize/release failed: key=%+v pointer=%+v captured=%v", afterKey, afterPointer, host.HasPointerCapture())
	}
}

func PlainRow(buf *surface.CellBuffer, y, width int) string {
	var out strings.Builder
	for x := 0; x < width; x++ {
		out.WriteRune(buf.Get(x, y).Char)
	}
	return out.String()
}

func TestOverviewSeparatesInspectionFromNavigation(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	if !screen.Select("agent:two") {
		t.Fatal("could not inspect second item")
	}
	selected, _ := screen.Selected()
	if selected.ID != "agent:two" {
		t.Fatalf("inspected=%q", selected.ID)
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

func TestOverviewArrowInspectionMarksDisplayedNoticeRead(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	result := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: string(ViewNotifications)})
	if result.Cmd == nil {
		t.Fatal("displaying an unread notice produced no read command")
	}
	msg := result.Cmd()
	found := false
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, cmd := range batch {
			if read, ok := cmd().(MarkReadMsg); ok && read.ID == "n" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("read command=%#v", msg)
	}
}

func TestOverviewNarrowLayoutUsesDrillDown(t *testing.T) {
	screen := New(theme.DefaultDark(), testProjection())
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 50, H: 16})
	if !screen.narrow || screen.detailMode {
		t.Fatalf("initial narrow state=%v detail=%v", screen.narrow, screen.detailMode)
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "status.item.open"})
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 50, H: 16})
	if !screen.detailMode || screen.details.Bounds().Empty() {
		t.Fatal("narrow inspector did not enter details")
	}
	result := screen.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEsc}))
	if !result.Handled || screen.detailMode {
		t.Fatal("Esc did not return to narrow master list")
	}
}

func TestAgentDetailsShowOneContextAndOneSelectedUsageScope(t *testing.T) {
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
	if got := detailText(screen.details.lines); strings.Count(got, "Context") != 1 || !strings.Contains(got, "Input 10") || strings.Contains(got, "Input 20") {
		t.Fatalf("default details repeat or mix scopes: %q", got)
	}
	host.Focus(screen.details.scopeField.ID())
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !result.Handled {
		t.Fatal("scope cycle ignored Right")
	}
	host.MarkLayout()
	host.Layout()
	if got := detailText(screen.details.lines); !strings.Contains(got, "Input 20") || strings.Contains(got, "Input 10") {
		t.Fatalf("scope cycle did not replace the one usage table: %q", got)
	}
}

func detailText(lines []DetailLine) string {
	var out strings.Builder
	for _, line := range lines {
		if line.Label != "" {
			out.WriteString(line.Label)
			out.WriteByte(' ')
		}
		out.WriteString(strings.TrimSpace(line.Value))
		out.WriteByte('\n')
	}
	return out.String()
}
