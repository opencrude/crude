package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/shellheader"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
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

// Exercise the mounted pane surfaces and Update input, not just linked editors.
func TestLinkedPanePointerAndEditingJourney(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	path := filepath.Join(ws.RootPath, "linked.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("abcdefghij\n", 100)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	_ = app.View()
	right := app.Win.ActiveGroup()
	lv, rv := app.groupEditorView(left, path), app.groupEditorView(right, path)
	if lv == nil || rv == nil || lv.Editor == rv.Editor {
		t.Fatal("distinct pane views not mounted")
	}
	rv.Editor.SetCursorPosition(50, 2)
	rv.Editor.ScrollToCursor()
	_ = app.View()
	rscroll := rv.Editor.ScrollY()
	lrect := app.mainPanes.groups[left].editor
	x, y := lrect.X+8, lrect.Y+3
	_, _ = app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, _ = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	_ = app.View()
	if app.EditorState.Editor != lv.Editor || lv.Editor.CursorLine() != 3 {
		t.Fatalf("left click selected wrong view/caret: group=%v editor=%p left=%p cursor=%d", app.Win.ActiveGroup(), app.EditorState.Editor, lv.Editor, lv.Editor.CursorLine())
	}
	if rv.Editor.CursorLine() != 50 || rv.Editor.ScrollY() != rscroll {
		t.Fatalf("left click changed right view: %d scroll=%d want=%d", rv.Editor.CursorLine(), rv.Editor.ScrollY(), rscroll)
	}
	paneJourneyKey(app, 'X', 0)
	if lv.Editor.Value() != rv.Editor.Value() || !strings.Contains(rv.Editor.LineText(3), "X") {
		t.Fatal("edit missing from other view")
	}
	if rv.Editor.CursorLine() != 50 || rv.Editor.ScrollY() != rscroll {
		t.Fatalf("edit disturbed right view: %d/%d", rv.Editor.CursorLine(), rv.Editor.ScrollY())
	}
	// Focus the other pane without clicking a new caret position.
	paneJourneyKey(app, ']', tea.ModAlt)
	if app.EditorState.Editor != rv.Editor || rv.Editor.CursorLine() != 50 || rv.Editor.ScrollY() != rscroll {
		t.Fatalf("switch changed right position or editor: %d/%d", rv.Editor.CursorLine(), rv.Editor.ScrollY())
	}
	paneJourneyKey(app, 'Y', 0)
	if lv.Editor.Value() != rv.Editor.Value() || !strings.Contains(lv.Editor.LineText(50), "Y") {
		t.Fatal("right edit missing from left view")
	}
}

// A real shared-document attachment must keep both rendered panes current and
// their independent viewport stable through edits, acknowledgement and save.
func TestConnectedLinkedPanesEditAndSaveKeepViewports(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("abcdefghij\n", 100)), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	app := shell.app
	paneJourneySize(app, 160, 40)
	liveTestOpen(t, app, path)
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	_ = app.View()
	right := app.Win.ActiveGroup()
	lv, rv := app.groupEditorView(left, path), app.groupEditorView(right, path)
	if lv == nil || rv == nil {
		t.Fatal("missing views")
	}
	app.focusMainGroup(left)
	_ = app.View()
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	runAppCommand(t, app, cmd)
	_ = app.View()
	if lv.Editor.Value() != rv.Editor.Value() {
		t.Fatal("sibling text is stale after edit")
	}
	paneJourneyRequireText(t, app, left, "Xabcdefghij")
	// A sibling showing the same location must repaint the edited bytes in
	// the same presented frame, not merely share an updated buffer.
	paneJourneyRequireText(t, app, right, "Xabcdefghij")
	rv.Editor.SetCursorPosition(50, 2)
	rv.Editor.ScrollToCursor()
	_ = app.View()
	beforeScroll := rv.Editor.ScrollY()
	liveTestSync(t, app)
	if rv.Editor.ScrollY() != beforeScroll || rv.Editor.CursorLine() != 50 {
		t.Fatalf("ack moved sibling: %d/%d", rv.Editor.CursorLine(), rv.Editor.ScrollY())
	}
	app.requestEditorSaveWithFormat(lv.Editor, workspace.DocumentSaveManual, false, false)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !lv.Editor.Modified() })
	_ = app.View()
	if lv.Editor.Value() != rv.Editor.Value() || rv.Editor.Modified() || rv.Editor.ScrollY() != beforeScroll || rv.Editor.CursorLine() != 50 {
		t.Fatalf("save changed sibling: cursor=%d scroll=%d modified=%v", rv.Editor.CursorLine(), rv.Editor.ScrollY(), rv.Editor.Modified())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != lv.Editor.Value() {
		t.Fatalf("save mismatch: %v", err)
	}
	app.focusMainGroup(right)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'Y', Text: "Y"})
	runAppCommand(t, app, cmd)
	_ = app.View()
	if lv.Editor.Value() != rv.Editor.Value() || !strings.Contains(lv.Editor.LineText(50), "Y") {
		t.Fatal("editing right pane did not update shared document")
	}
}

// Reusing one pane for a different file must not disturb the other pane; the
// first pane's original view must retain its own caret when selected again.
func TestLinkedPaneCanOpenAnotherFileAndReturn(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "first.go", "second.go")
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	_ = app.View()
	right := app.Win.ActiveGroup()
	lv, rv := app.groupEditorView(left, paths[1]), app.groupEditorView(right, paths[1])
	lv.Editor.SetCursorPosition(0, 3)
	rv.Editor.SetCursorPosition(0, 8)
	if _, err := app.openEditorTab(paths[0]); err != nil {
		t.Fatal(err)
	}
	_ = app.View()
	if app.Win.ActiveGroup() != right || app.EditorState.ActiveView != paths[0] {
		t.Fatal("opening another file left the requesting pane")
	}
	if lv.Editor.CursorCol() != 3 {
		t.Fatal("opening another file moved the other pane's caret")
	}
	if _, err := app.openEditorTab(paths[1]); err != nil {
		t.Fatal(err)
	}
	_ = app.View()
	if app.EditorState.Editor != rv.Editor || rv.Editor.CursorCol() != 8 || lv.Editor.CursorCol() != 3 {
		t.Fatalf("return did not restore independent views: left=%d right=%d", lv.Editor.CursorCol(), rv.Editor.CursorCol())
	}
}

// Undo from another pane must not teleport the inactive pane back to the
// position where an earlier edit was made.
func TestConnectedLinkedPaneUndoKeepsOtherPanePosition(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "undo.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("abcdefghij\n", 100)), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	app := shell.app
	paneJourneySize(app, 160, 40)
	liveTestOpen(t, app, path)
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	_ = app.View()
	right := app.Win.ActiveGroup()
	lv, rv := app.groupEditorView(left, path), app.groupEditorView(right, path)
	app.focusMainGroup(left)
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	runAppCommand(t, app, cmd)
	liveTestSync(t, app)
	lv.Editor.SetCursorPosition(15, 2)
	lv.Editor.ScrollToCursor()
	rv.Editor.SetCursorPosition(50, 2)
	rv.Editor.ScrollToCursor()
	leftScroll := lv.Editor.ScrollY()
	rightScroll := rv.Editor.ScrollY()
	app.focusMainGroup(right)
	_ = app.View()
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return !strings.HasPrefix(rv.Editor.Value(), "X") && !rv.Editor.DocumentState().HistoryQueued
	})
	if lv.Editor.CursorLine() != 15 || lv.Editor.ScrollY() != leftScroll || rv.Editor.CursorLine() != 50 || rv.Editor.ScrollY() != rightScroll {
		t.Fatalf("undo moved an unrelated pane: left=%d/%d want=15/%d right=%d/%d want=50/%d", lv.Editor.CursorLine(), lv.Editor.ScrollY(), leftScroll, rv.Editor.CursorLine(), rv.Editor.ScrollY(), rightScroll)
	}
	if app.EditorState.Editor != rv.Editor {
		t.Fatalf("undo changed focused editor to %p, expected %p", app.EditorState.Editor, rv.Editor)
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.HasPrefix(rv.Editor.Value(), "X") && !rv.Editor.DocumentState().HistoryQueued
	})
	if lv.Editor.CursorLine() != 15 || lv.Editor.ScrollY() != leftScroll || rv.Editor.CursorLine() != 50 || rv.Editor.ScrollY() != rightScroll {
		t.Fatalf("redo moved an unrelated pane: left=%d/%d right=%d/%d", lv.Editor.CursorLine(), lv.Editor.ScrollY(), rv.Editor.CursorLine(), rv.Editor.ScrollY())
	}
}

// Background inventory reconciliation must keep the exact selected view,
// even when another pane has a tab with the same file title.
func TestLinkedPaneAgentSyncPreservesExactView(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "same.go")
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	_ = app.View()
	right := app.Win.ActiveGroup()
	rv := app.groupEditorView(right, paths[0])
	tab := rv.Tab
	rv.Editor.SetCursorPosition(0, 5)
	app.syncAgentWindowTabs()
	_ = app.View()
	if app.Win.ActiveGroup() != right || app.Win.ActiveTab().ID != tab || app.EditorState.Editor != rv.Editor || rv.Editor.CursorCol() != 5 {
		t.Fatalf("background tab synchronization switched pane: left=%v right=%v active=%v tab=%v", left, right, app.Win.ActiveGroup(), app.Win.ActiveTab().ID)
	}
}
