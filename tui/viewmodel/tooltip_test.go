package viewmodel

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"
)

func tooltipText(app *App) string {
	fw := app.overlayHost.Window(ui.OverlayTooltip)
	if fw == nil {
		return ""
	}
	w, h := fw.Size()
	surf := surface.NewSurface("tooltip-test", w, h, app.mainSurf.Buffer().Get(0, 0))
	fw.Content.Render(surf, app.renderCtx)
	var b strings.Builder
	for y := range h {
		b.WriteString(shellchat.PlainRow(surf.Buffer(), y, w))
		b.WriteByte('\n')
	}
	return b.String()
}

// tabLabelPoint returns a screen cell inside the painted label of a tab.
func tabLabelPoint(t *testing.T, app *App, label string) (int, int) {
	t.Helper()
	app.paintShellFrame()
	rect, ok := app.rootHost.ScreenRect(app.tabScreen.TabsControl().ID())
	if !ok {
		t.Fatal("the tab strip is not mounted")
	}
	buf := app.mainSurf.Buffer()
	cells := make([]rune, buf.Width())
	for x := range cells {
		cells[x] = ' '
		if c := buf.Get(x, rect.Y); c.Char != 0 {
			cells[x] = c.Char
		}
	}
	row := string(cells)
	index := strings.Index(row, label)
	if index < 0 {
		t.Fatalf("tab %q is not painted: %q", label, row)
	}
	return len([]rune(row[:index])) + 1, rect.Y
}

// Hovering a file tab shows its absolute path after the hover delay, in the
// shared popover frame. Input closes it, and it stays closed until the pointer
// leaves the tab.
func TestFileTabTooltipShowsAbsolutePath(t *testing.T) {
	for _, size := range [][2]int{{140, 36}, {60, 20}} {
		app := newFullTestApp(t)
		app.width, app.height = size[0], size[1]
		path := filepath.Join(t.TempDir(), "hovered.go")
		newSaveTestEditorAt(t, app, path, "base")
		app.Win.OpenTab(app.editorTabTitle(path), "✎", true)
		x, y := tabLabelPoint(t, app, "hovered.go")

		_, wait := app.Update(tea.MouseMotionMsg{X: x, Y: y})
		if app.overlayHost.IsOpen(ui.OverlayTooltip) {
			t.Fatal("the tooltip opened without the hover delay")
		}
		runAppCommand(t, app, wait)
		text := tooltipText(app)
		if !strings.Contains(strings.Join(strings.Fields(strings.ReplaceAll(text, "│", "")), ""), path) {
			t.Fatalf("width %d: the tooltip does not show the absolute path:\n%s", size[0], text)
		}
		fw := app.overlayHost.Window(ui.OverlayTooltip)
		if _, top := fw.Position(); top <= y {
			t.Fatalf("the tooltip covers the hovered tab (tooltip row %d, tab row %d)", top, y)
		}
		if app.windowManager.Focused() == fw {
			t.Fatal("the tooltip took keyboard focus")
		}

		app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		if app.overlayHost.IsOpen(ui.OverlayTooltip) {
			t.Fatal("a key press did not close the tooltip")
		}
		if _, again := app.Update(tea.MouseMotionMsg{X: x, Y: y}); again != nil {
			runAppCommand(t, app, again)
		}
		if app.overlayHost.IsOpen(ui.OverlayTooltip) {
			t.Fatal("the dismissed tooltip came back while the pointer stayed on the tab")
		}

		// Leaving the tab re-arms it.
		app.Update(tea.MouseMotionMsg{X: x, Y: y + 4})
		_, wait = app.Update(tea.MouseMotionMsg{X: x, Y: y})
		runAppCommand(t, app, wait)
		if !app.overlayHost.IsOpen(ui.OverlayTooltip) {
			t.Fatal("the tooltip did not return after the pointer left and came back")
		}
		app.Update(tea.MouseMotionMsg{X: x, Y: y + 4})
		if app.overlayHost.IsOpen(ui.OverlayTooltip) {
			t.Fatal("the tooltip stayed open after the pointer left the tab")
		}
	}
}

// A stale hover delay never opens a tooltip for a tab the pointer has left.
func TestTabTooltipDelayIsDroppedWhenThePointerLeaves(t *testing.T) {
	app := newFullTestApp(t)
	path := filepath.Join(t.TempDir(), "stale.go")
	newSaveTestEditorAt(t, app, path, "base")
	app.Win.OpenTab(app.editorTabTitle(path), "✎", true)
	x, y := tabLabelPoint(t, app, "stale.go")
	_, wait := app.Update(tea.MouseMotionMsg{X: x, Y: y})
	app.Update(tea.MouseMotionMsg{X: x, Y: y + 4})
	runAppCommand(t, app, wait)
	if app.overlayHost.IsOpen(ui.OverlayTooltip) {
		t.Fatal("a stale hover delay opened the tooltip")
	}
}

// An agent tab explains which agent it is and what it is doing.
func TestAgentTabTooltipNamesTheAgentAndItsState(t *testing.T) {
	app := newFullTestApp(t)
	id := addBackendTestOrchestrator(t, app)
	app.paintShellFrame()
	tab := app.Win.TabByTitle(app.agentTabTitle(id))
	if tab == nil {
		t.Fatal("the agent has no tab")
	}
	text := app.tabTooltip(tab.ID)
	lines := strings.Split(text, "\n")
	if len(lines) < 2 || lines[0] == "" || !strings.HasPrefix(lines[len(lines)-1], "Idle") {
		t.Fatalf("agent tooltip = %q", text)
	}
}
