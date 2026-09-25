package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
)

// Motion reaches settings via the popup hover handler. The WM must repaint its
// cached window without giving the hovered field keyboard focus.
func TestSettingsWindowHoverRendersWithoutChangingKeyboardTarget(t *testing.T) {
	app, window, body := newSettingsPromptApp(t)
	frame := window.Content.(*ui.WindowFrameWidget)
	host := frame.Host()
	body.ShowMainAgent() // focuses Account, the first interactive field
	host.MarkLayout()
	host.Layout()
	if got := host.Focused(); got != settingsui.WorkspaceSettingsFormID {
		t.Fatalf("form focus=%q", got)
	}
	formRect, ok := host.ScreenRect(settingsui.WorkspaceSettingsFormID)
	if !ok {
		t.Fatal("settings form not mounted")
	}
	window.Render(&wm.RenderContext{Theme: app.Theme})
	before := window.Surface().Children()[0].Buffer()
	rowY := -1
	for y, line := range strings.Split(bufferPlain(before), "\n") {
		if strings.Contains(line, "Thinking level") {
			rowY = y
			break
		}
	}
	if rowY < formRect.Y || rowY >= formRect.Y+formRect.H {
		t.Fatalf("Thinking level row not visible in settings form: y=%d form=%+v", rowY, formRect)
	}
	localX := formRect.X + 2
	baseline := before.Get(localX, rowY).BG
	px, py := window.Position()
	selected := body.Model().SelectedCategory()
	_, _ = app.Update(tea.MouseMotionMsg{X: px + localX, Y: py + rowY, Button: tea.MouseNone})
	if host.Focused() != settingsui.WorkspaceSettingsFormID || body.Model().SelectedCategory() != selected {
		t.Fatal("hover changed keyboard focus or category")
	}
	window.Render(&wm.RenderContext{Theme: app.Theme})
	painted := window.Surface().Children()[0].Buffer()
	tokens := ui.NewTokenSet(app.Theme)
	if got, want := painted.Get(localX, rowY).BG, tokens.Color(ui.ColorHover); !got.Equal(want) || got.Equal(baseline) {
		t.Fatalf("cached settings hover bg=%v, before=%v, want %v", got, baseline, want)
	}
	if got, want := painted.Get(localX, rowY).FG, tokens.Color(ui.ColorFg); !got.Equal(want) {
		t.Fatalf("hovered field fg=%v want unfocused %v", got, want)
	}

	// Moving over the category rail should not navigate without a click.
	rail, ok := host.ScreenRect(settingsui.WorkspaceSettingsListID)
	if !ok {
		t.Fatal("settings category rail not mounted")
	}
	_, _ = app.Update(tea.MouseMotionMsg{X: px + rail.X + 2, Y: py + rail.Y + 1, Button: tea.MouseNone})
	if body.Model().SelectedCategory() != selected || host.Focused() != settingsui.WorkspaceSettingsFormID {
		t.Fatal("rail motion selected a category or stole focus")
	}
	window.Render(&wm.RenderContext{Theme: app.Theme})
	if got := window.Surface().Children()[0].Buffer().Get(localX, rowY).BG; !got.Equal(baseline) {
		t.Fatalf("hover remained after leaving form: bg=%v want %v", got, baseline)
	}
}
