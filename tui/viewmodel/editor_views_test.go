package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellheader"
	"crude/tui/ui"
	"crude/tui/window"
)

func groupFileViews(app *App, group window.GroupID, path string) int {
	n := 0
	for _, tab := range app.Win.GroupTabs(group) {
		if v := app.editorView(tab.ID); v != nil && v.Path == path {
			n++
		}
	}
	return n
}

// Splitting a file shows the same file in the new pane, at the same place;
// both views edit one document and paint every keystroke at once; closing one
// view of a modified file needs no prompt because the other still shows it.
func TestSplitShowsTheSameFileInBothPanesInSync(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "other_file.go", "split_me.go")
	left := app.Win.ActiveGroup()

	if _, handled := app.handleMainPaneCommand("pane.split-right"); !handled {
		t.Fatal("split was not handled")
	}
	_ = app.View()
	right := app.Win.ActiveGroup()
	if len(app.Win.GroupIDs()) != 2 || right == left {
		t.Fatalf("split did not add a pane: groups=%v", app.Win.GroupIDs())
	}
	if groupFileViews(app, right, paths[1]) != 1 || groupFileViews(app, left, paths[1]) != 1 || app.EditorState.ActiveView != paths[1] {
		t.Fatalf("split did not show the file in the new pane: right=%d left=%d active=%s",
			groupFileViews(app, right, paths[1]), groupFileViews(app, left, paths[1]), app.EditorState.ActiveView)
	}
	paneJourneyRequireText(t, app, right, "package split_me.go")

	paneJourneyKey(app, 'Q', 0)
	paneJourneyRequireText(t, app, right, "Qpackage split_me.go")
	paneJourneyRequireText(t, app, left, "Qpackage split_me.go")

	// Close the right view: the left one keeps the unsaved text, no prompt.
	closing := app.Win.ActiveTab().ID
	_ = app.handleAgentTabClose(app.Win.IndexOf(closing))
	_ = app.View()
	if app.DirtyPromptPath != "" || app.Win.IndexOf(closing) >= 0 {
		t.Fatalf("closing one view prompted or failed: prompt=%q", app.DirtyPromptPath)
	}
	doc := app.EditorState.Tabs[paths[1]]
	if doc == nil || !doc.Editor.IsOpen() || !strings.HasPrefix(doc.Editor.Value(), "Qpackage") || !doc.Editor.Modified() {
		t.Fatal("closing one view closed or changed the document")
	}
	paneJourneyRequireText(t, app, left, "Qpackage split_me.go")
}

// A file opened while a pane is focused opens in that pane, even when another
// pane already shows it. Open in Right Pane reuses the right pane's view.
func TestOpenLandsInTheFocusedPane(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "shared.go", "here.go")
	left := app.Win.ActiveGroup()
	app.splitMainGroup(ui.Horizontal, false, 0) // an empty pane on the right
	right := app.Win.ActiveGroup()

	if _, err := app.openEditorTab(paths[0]); err != nil {
		t.Fatal(err)
	}
	_ = app.View()
	if app.Win.ActiveGroup() != right || groupFileViews(app, right, paths[0]) != 1 || groupFileViews(app, left, paths[0]) != 1 {
		t.Fatalf("open did not land in the focused pane: active=%d right=%d left=%d",
			app.Win.ActiveGroup(), groupFileViews(app, right, paths[0]), groupFileViews(app, left, paths[0]))
	}

	// From the left pane, Open in Right Pane selects the existing right view.
	leftTab := app.Win.GroupTabByTitle(left, app.editorTabTitle(paths[0]))
	app.Win.SwitchTo(app.Win.IndexOf(leftTab.ID))
	app.handleTabSwitch()
	app.openBeside(leftTab.ID, ui.Horizontal, false)
	if app.Win.ActiveGroup() != right || groupFileViews(app, right, paths[0]) != 1 || app.Win.IndexOf(leftTab.ID) < 0 {
		t.Fatalf("Open in Right Pane duplicated or moved the tab: right=%d", groupFileViews(app, right, paths[0]))
	}

	// Move Tab Right with the file already there keeps one view in that pane.
	app.Win.SwitchTo(app.Win.IndexOf(leftTab.ID))
	app.handleTabSwitch()
	app.moveBeside(leftTab.ID, ui.Horizontal, false)
	if groupFileViews(app, right, paths[0]) != 1 || groupFileViews(app, left, paths[0]) != 0 {
		t.Fatalf("Move Tab Right: right=%d left=%d", groupFileViews(app, right, paths[0]), groupFileViews(app, left, paths[0]))
	}
}

// Go Back returns to the previous place and Go Forward returns again, by key,
// by mouse button and by the header arrows.
func TestGoBackAndForwardReturnToEachPlace(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "first.go", "second.go")
	first := app.Win.GroupTabByTitle(app.Win.ActiveGroup(), app.editorTabTitle(paths[0]))
	second := app.Win.ActiveTab().ID
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt})
	_ = app.View()
	if app.Win.ActiveTab().ID != first.ID {
		t.Fatalf("Alt+Left did not go back to the first file: %s", app.EditorState.ActiveView)
	}
	if !app.canNavigate(false) {
		t.Fatal("Go Forward is not offered after Go Back")
	}
	if nav := app.statusHeaderNavSegments(); nav[0].Role != ui.ColorMuted || nav[1].Role != ui.ColorFg {
		t.Fatalf("header arrows do not read as empty/filled after Go Back: %+v", nav)
	}
	_, _ = app.Update(tea.MouseClickMsg{X: 10, Y: 10, Button: tea.MouseForward})
	_ = app.View()
	if app.Win.ActiveTab().ID != second {
		t.Fatalf("mouse Forward did not return: %s", app.EditorState.ActiveView)
	}
	_, _ = app.Update(shellheader.ActionMsg{Action: "nav.back"})
	_ = app.View()
	if app.Win.ActiveTab().ID != first.ID {
		t.Fatalf("header Back did not go back: %s", app.EditorState.ActiveView)
	}
}
