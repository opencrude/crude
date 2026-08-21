package sourcecontrol

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestReproGitScrollbarSnapBackOnRelayout(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	changes := make([]workspace.GitChange, 0, 60)
	for i := 0; i < 60; i++ {
		changes = append(changes, workspace.GitChange{Path: "file.go", Area: workspace.GitAreaWorking, Code: "M"})
	}
	screen.SetSnapshot(workspace.GitSnapshot{Branch: "main", Changes: changes})
	host := ui.NewHost(th, screen)
	host.Resize(42, 18)
	host.Layout()

	// wheel down over the list
	listRect, _ := host.ScreenRect(ListID)
	before := screen.list.Scroll().Offset()
	host.Handle(tea.MouseWheelMsg{X: listRect.X + 1, Y: listRect.Y + 1, Button: tea.MouseWheelDown})
	afterWheel := screen.list.Scroll().Offset()
	t.Logf("offset before=%d after wheel=%d", before, afterWheel)

	// simulate a fresh shell frame: Layout() then Paint() then Layout()
	host.Layout()
	host.MarkLayout()
	host.Layout()
	afterFrame := screen.list.Scroll().Offset()
	t.Logf("offset after relayout frame=%d", afterFrame)
	if afterFrame != afterWheel {
		t.Logf("SNAP-BACK CONFIRMED: %d -> %d after layout", afterWheel, afterFrame)
	}
}
