package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/ui"
)

func reversedCells(app *App, r ui.Rect) int {
	count := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if app.mainSurf.Buffer().Get(x, y).Attr.Has(cell.AttrReverse) {
				count++
			}
		}
	}
	return count
}

func TestComposerAndQuestionCaretFollowInputOwnerAndTerminalFocus(t *testing.T) {
	app, prompt := questionDockJourneyApp(t)
	draft := app.Input.Value()
	questionDockClickInput(t, app, prompt)
	app.SpinFrame = 0
	app.Input.SetSpinFrame(0)
	questionDockPaint(app)
	if reversedCells(app, app.inputRect) != 0 || reversedCells(app, app.sessionDeckRect) == 0 {
		t.Fatal("question owns input but composer also paints a caret, or question has none")
	}
	questionDockClickText(t, app, app.inputRect, "private composer draft")
	questionDockPaint(app)
	if reversedCells(app, app.inputRect) == 0 || reversedCells(app, app.sessionDeckRect) != 0 {
		t.Fatal("composer focus did not remove the retained question caret")
	}
	_, _ = app.Update(tea.BlurMsg{})
	questionDockPaint(app)
	if reversedCells(app, app.inputRect) != 0 || reversedCells(app, app.sessionDeckRect) != 0 {
		t.Fatal("terminal blur left a caret visible")
	}
	_, _ = app.Update(tea.FocusMsg{})
	questionDockPaint(app)
	if reversedCells(app, app.inputRect) == 0 || reversedCells(app, app.sessionDeckRect) != 0 || app.Input.Value() != draft {
		t.Fatal("terminal focus did not restore the composer and its unchanged draft")
	}
}

func TestEditorCaretFollowsPopupAndTerminalFocus(t *testing.T) {
	app, _ := newRealShellApp(t)
	openEditorTabs(t, app, "main.go")
	app.SpinFrame = 0
	app.forceFrame()
	app.View()
	r := app.mainPanes.groups[app.Win.ActiveGroup()].editor
	if reversedCells(app, r) == 0 {
		t.Fatal("focused editor has no caret")
	}
	app.toggleCommandPalette()
	app.forceFrame()
	app.View()
	if reversedCells(app, r) != 0 {
		t.Fatal("editor kept a caret while the palette owned input")
	}
	app.overlayHost.Close(ui.OverlayCommandPalette)
	app.forceFrame()
	app.View()
	if reversedCells(app, r) == 0 {
		t.Fatal("editor caret did not return after closing the palette")
	}
	_, _ = app.Update(tea.BlurMsg{})
	app.forceFrame()
	app.View()
	if reversedCells(app, r) != 0 {
		t.Fatal("editor caret survived terminal blur")
	}
	_, _ = app.Update(tea.FocusMsg{})
	app.forceFrame()
	app.View()
	if reversedCells(app, r) == 0 {
		t.Fatal("editor caret did not return after terminal focus")
	}
}

func TestHeaderShowsLiveFocusedFileLineCount(t *testing.T) {
	for _, width := range []int{160, 80} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app, _ := newRealShellApp(t)
			_, _ = app.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			openEditorTabs(t, app, "main.go")
			check := func(want string) {
				t.Helper()
				app.forceFrame()
				app.View()
				r, ok := app.rootHost.ScreenRect(app.headerScreen.ID())
				if !ok || !strings.Contains(paneJourneyText(app, r), want) {
					t.Fatalf("header does not show %q: %q", want, paneJourneyText(app, r))
				}
			}
			check("1:1 · 2 lines")
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
			_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			check("3:1 · 3 lines")

			empty := filepath.Join(app.ws.RootPath(), "empty.txt")
			if err := os.WriteFile(empty, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			openEditorTabs(t, app, "empty.txt")
			check("1:1 · 1 line")
			app.AddOrchestrator("header-agent")
			app.forceFrame()
			app.View()
			r, _ := app.rootHost.ScreenRect(app.headerScreen.ID())
			if strings.Contains(paneJourneyText(app, r), "1 line") {
				t.Fatal("the prior editor's count leaked into the agent header")
			}
		})
	}
}
