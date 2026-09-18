package sourcecontrol

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

// TestGitListOffsetSurvivesRelayout guards against scroll snap-back: the
// offset produced by wheel scrolling must not be yanked back to the selection
// by a subsequent layout/frame. List.Layout must clamp the viewport but never
// call EnsureVisible (that used to reset offset to the selected row on every
// frame, undoing the wheel gesture within one tick).
func TestGitListOffsetSurvivesRelayout(t *testing.T) {
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
	if afterWheel <= before {
		t.Fatalf("wheel did not scroll: offset before=%d after=%d", before, afterWheel)
	}

	// simulate a fresh shell frame: Layout() then Paint() then Layout()
	host.Layout()
	host.MarkLayout()
	host.Layout()
	afterFrame := screen.list.Scroll().Offset()
	if afterFrame != afterWheel {
		t.Fatalf("SNAP-BACK: offset %d after wheel became %d after relayout", afterWheel, afterFrame)
	}
}
