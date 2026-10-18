package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
)

// Execute only the pointer timer in the returned batch. Other App commands
// (frame deadlines, watcher collectors) are not part of this deterministic input.
func pointerScrollMessage(t *testing.T, cmd tea.Cmd) editor.PointerScrollMsg {
	t.Helper()
	var find func(tea.Cmd) (editor.PointerScrollMsg, bool)
	find = func(c tea.Cmd) (editor.PointerScrollMsg, bool) {
		if c != nil {
			switch msg := c().(type) {
			case editor.PointerScrollMsg:
				return msg, true
			case tea.BatchMsg:
				for _, child := range msg {
					if tick, ok := find(child); ok {
						return tick, true
					}
				}
			}
		}
		return editor.PointerScrollMsg{}, false
	}
	msg, ok := find(cmd)
	if !ok {
		t.Fatal("real pointer route did not return a scroll timer")
	}
	return msg
}

func TestEditorPointerMountedJourney(t *testing.T) {
	for _, floating := range []bool{false, true} {
		t.Run(map[bool]string{false: "main pane", true: "floating pane"}[floating], func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 160, 40)
			path := filepath.Join(ws.RootPath, "pointer.txt")
			var text strings.Builder
			for i := 0; i < 160; i++ {
				fmt.Fprintf(&text, "marker %03d alpha beta gamma\n", i)
			}
			if err := os.WriteFile(path, []byte(text.String()), 0600); err != nil {
				t.Fatal(err)
			}
			openEditorTabs(t, app, "pointer.txt")
			if floating {
				app.openTabInWindow(app.Win.ActiveTab().ID)
			}
			_ = app.View()
			group := app.Win.ActiveGroup()
			if floating {
				_ = paneWindowText(t, app, group)
			}
			ed := app.EditorState.Editor
			rect := app.mainPanes.groups[group].editor
			x, y := rect.X+22, rect.Y+2
			_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			_, cmd := app.Update(tea.MouseMotionMsg{X: x, Y: rect.Y + rect.H + 2, Button: tea.MouseLeft})
			for i := 0; i < 3; i++ {
				msg := pointerScrollMessage(t, cmd)
				_, cmd = app.Update(msg)
				_ = app.View()
				if floating {
					_ = paneWindowText(t, app, group)
				}
			}
			if ed.ScrollY() == 0 || !ed.HasSelection() || ed.Modified() {
				t.Fatalf("mounted drag failed: scroll=%d selection=%q dirty=%v", ed.ScrollY(), ed.SelectedText(), ed.Modified())
			}
			painted := paneJourneyText(app, rect)
			if floating {
				painted = paneWindowText(t, app, group)
			}
			if !strings.Contains(painted, fmt.Sprintf("marker %03d", ed.ScrollY())) {
				t.Fatal("viewport mutation not presented in mounted editor")
			}
			late := pointerScrollMessage(t, cmd)
			_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: rect.Y + rect.H + 2, Button: tea.MouseLeft})
			before, selection := ed.ScrollY(), ed.SelectedText()
			_, _ = app.Update(late)
			if ed.ScrollY() != before || selection != ed.SelectedText() || ed.IsMouseDragging() || selection == "" {
				t.Fatal("release lost editable selection or timer remained live")
			}
			// End-to-end: the retained drag range is editable and one Undo restores
			// exact original bytes, not just internal selection coordinates.
			paneJourneyKey(app, 'X', 0)
			if ed.Value() == text.String() {
				t.Fatal("typing did not replace dragged selection")
			}
			paneJourneyKey(app, 'z', tea.ModCtrl)
			if ed.Value() != text.String() {
				t.Fatal("undo after scrolled drag replacement did not restore exact bytes")
			}
		})
	}
}

func TestEditorPointerBlurAndTabUnmountStopRepeating(t *testing.T) {
	for _, stop := range []string{"blur", "tab", "modal"} {
		t.Run(stop, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			defer ws.Close()
			paneJourneySize(app, 120, 32)
			paths := openEditorTabs(t, app, "pointer.txt", "other.txt")
			app.activateEditorTab(paths[0])
			ed := app.EditorState.Editor
			ed.ReplaceDocument(strings.Repeat("alpha beta gamma\n", 100))
			app.paintShellFrame()
			r := app.mainPanes.groups[app.Win.ActiveGroup()].editor
			_, _ = app.Update(tea.MouseClickMsg{X: r.X + 20, Y: r.Y + 2, Button: tea.MouseLeft})
			_, cmd := app.Update(tea.MouseMotionMsg{X: r.X + 20, Y: r.Y + r.H + 2, Button: tea.MouseLeft})
			late := pointerScrollMessage(t, cmd)
			switch stop {
			case "blur":
				_, _ = app.Update(tea.BlurMsg{})
			case "tab":
				app.activateEditorTab(paths[1])
				app.paintShellFrame()
			case "modal":
				app.showDirtyClosePrompt(paths[0])
			}
			before := ed.ScrollY()
			_, _ = app.Update(late)
			if ed.IsMouseDragging() || ed.ScrollY() != before {
				t.Fatal("blur/unmounted editor kept a live timer")
			}
		})
	}
}
