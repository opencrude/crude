package termdock

import (
	"testing"

	"crude/tui/screens/shellterm"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func TestDockMinimumHeightPreservesUsableTerminalBody(t *testing.T) {
	th := theme.DefaultDark()
	dock := New(th)
	screen := shellterm.New(th, "compact", "compact")
	t.Cleanup(screen.Close)
	dock.Mount("compact", screen)

	dock.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: MinRenderableHeight})
	if !dock.renderable {
		t.Fatal("minimum-height dock was not renderable")
	}
	if got := screen.Bounds().H; got != workspace.MinTerminalRows {
		t.Fatalf("terminal body height=%d want=%d", got, workspace.MinTerminalRows)
	}
	if len(dock.Children()) != 2 {
		t.Fatalf("renderable dock children=%d want divider and terminal", len(dock.Children()))
	}

	dock.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: MinRenderableHeight - 1})
	if dock.renderable {
		t.Fatal("dock rendered chrome without the minimum terminal body")
	}
	if len(dock.Children()) != 0 {
		t.Fatalf("undersized dock exposed %d stale children", len(dock.Children()))
	}
	if len(dock.actions) != 0 {
		t.Fatalf("undersized dock exposed %d stale header actions", len(dock.actions))
	}
}
