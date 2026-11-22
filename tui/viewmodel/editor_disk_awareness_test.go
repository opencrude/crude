package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/diffview"
	"crude/tui/ui"
	"crude/types/statuscenter"
	"crude/workspace"
)

// diskNotices returns the editor's disk-change notices in the status center.
func diskNoticesOf(app *App) []statuscenter.NotificationRecord {
	var out []statuscenter.NotificationRecord
	for _, n := range app.ws.Status().Notifications(true) {
		if strings.HasPrefix(n.DedupKey, "disk:") {
			out = append(out, n)
		}
	}
	return out
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The reported defect: an outside edit was not applied and nobody said so
// until Save or close. Now an edit that touches no unsaved line joins the text
// at once, the user hears it once, and a stream of further writes (a build, a
// formatter) says nothing more until the text is saved.
func TestOutsideEditMergesIntoUnsavedTextAndSaysSoOnce(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	app := newRemoteJourneyShell(t, rt, root).app
	path := filepath.Join(root, "notes.txt")
	writeFile(t, path, "one\ntwo\nthree\n")
	ed := liveTestOpen(t, app, path)
	ed.SetCursorPosition(0, 3)
	ed.InsertRune('!')
	settleLive(t, app)

	writeFile(t, path, "one\ntwo\nTHREE\n")
	liveTestPump(t, []*App{app}, func() bool { return ed.Value() == "one!\ntwo\nTHREE\n" })
	if !ed.Modified() || ed.DiskConflict() {
		t.Fatalf("the merge must keep the edit unsaved without a conflict: modified=%v conflict=%v", ed.Modified(), ed.DiskConflict())
	}
	liveTestPump(t, []*App{app}, func() bool { return len(diskNoticesOf(app)) > 0 })
	notices := diskNoticesOf(app)
	if len(notices) != 1 || notices[0].Summary != "notes.txt changed on disk. Crude added the changes to your unsaved text." {
		t.Fatalf("one merge notice expected, got %+v", notices)
	}
	if notices[0].Owner.Kind != statuscenter.ResourceEditor || notices[0].Owner.ID != path {
		t.Fatalf("the notice must open the file: %+v", notices[0].Owner)
	}

	// More writes in the same unsaved session merge silently.
	writeFile(t, path, "one\nTWO\nTHREE\n")
	liveTestPump(t, []*App{app}, func() bool { return ed.Value() == "one!\nTWO\nTHREE\n" })
	settleLive(t, app)
	if got := diskNoticesOf(app); len(got) != 1 || got[0].Occurrences > 1 {
		t.Fatalf("a second merge in one unsaved session must not notify again: %+v", got)
	}
	if paint := ansi.Strip(app.View().RenderContent()); strings.Contains(paint, "Changed on disk") {
		t.Fatalf("a merge shows no disk bar:\n%s", paint)
	}

	// Saving ends the session; the file follows silently while saved.
	runAppCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	settleLive(t, app)
	writeFile(t, path, "ONE!\nTWO\nTHREE\n")
	liveTestPump(t, []*App{app}, func() bool { return ed.Value() == "ONE!\nTWO\nTHREE\n" })
	settleLive(t, app)
	if ed.Modified() || len(diskNoticesOf(app)) != 1 {
		t.Fatalf("saved text must follow the file silently: modified=%v notices=%+v", ed.Modified(), diskNoticesOf(app))
	}
}

// An outside edit of a line the user changed needs the user. The editor shows
// one bar with the choices, on the view itself, and does not also raise a
// notice for a file the user is looking at. Discard from the bar settles it
// and the bar goes away.
func TestConflictingOutsideEditShowsDiskBarOnTheVisibleEditor(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	app := newRemoteJourneyShell(t, rt, root).app
	path := filepath.Join(root, "notes.txt")
	writeFile(t, path, "one\ntwo\n")
	ed := liveTestOpen(t, app, path)
	ed.SetCursorPosition(0, 3)
	ed.InsertRune('!')
	settleLive(t, app)

	writeFile(t, path, "ONE\ntwo\n")
	liveTestPump(t, []*App{app}, ed.DiskConflict)
	settleLive(t, app)
	paint := ansi.Strip(app.View().RenderContent())
	for _, want := range []string{"Changed on disk. Conflicts with your", "Compare", "Overwrite", "Discard"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("the disk bar misses %q:\n%s", want, paint)
		}
	}
	if got := diskNoticesOf(app); len(got) != 0 {
		t.Fatalf("a conflict in a visible editor must not also notify: %+v", got)
	}
	tab := app.Win.ActiveTab()
	if tip := app.tabTooltip(tab.ID); !strings.Contains(tip, "Changed on disk") || !strings.Contains(tip, "Unsaved changes") {
		t.Fatalf("the tab tooltip must state the conflict: %q", tip)
	}

	// Click Discard on the bar itself, like the user.
	x, y, ok := paintedCell(app, "[ Discard ]")
	if !ok {
		t.Fatal("Discard is not painted")
	}
	_, cmd := app.Update(tea.MouseClickMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.MouseReleaseMsg{X: x + 2, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	settleLive(t, app)
	if ed.Value() != "ONE\ntwo\n" || ed.Modified() || ed.DiskConflict() {
		t.Fatalf("Discard must load the file: %q modified=%v conflict=%v", ed.Value(), ed.Modified(), ed.DiskConflict())
	}
	if paint := ansi.Strip(app.View().RenderContent()); strings.Contains(paint, "Changed on disk. Conflicts") {
		t.Fatalf("the bar must go once the conflict ends:\n%s", paint)
	}
}

// A conflict in a file the user cannot see raises one notice that opens it.
func TestConflictInHiddenEditorNotifiesOnce(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	app := newRemoteJourneyShell(t, rt, root).app
	hidden, shown := filepath.Join(root, "hidden.txt"), filepath.Join(root, "shown.txt")
	writeFile(t, hidden, "one\n")
	writeFile(t, shown, "other\n")
	ed := liveTestOpen(t, app, hidden)
	ed.SetCursorPosition(0, 3)
	ed.InsertRune('!')
	settleLive(t, app)
	liveTestOpen(t, app, shown)
	if app.editorVisible(ed) {
		t.Fatal("setup: the changed file must be hidden behind another tab")
	}
	writeFile(t, hidden, "uno\n")
	liveTestPump(t, []*App{app}, ed.DiskConflict)
	liveTestPump(t, []*App{app}, func() bool { return len(diskNoticesOf(app)) > 0 })
	notices := diskNoticesOf(app)
	if len(notices) != 1 || !strings.Contains(notices[0].Summary, "hidden.txt changed on disk and conflicts with your unsaved changes") || notices[0].Severity != statuscenter.SeverityWarning {
		t.Fatalf("one conflict notice expected: %+v", notices)
	}
	// The file changing again while the conflict waits says nothing new.
	writeFile(t, hidden, "UNO\n")
	settleLive(t, app)
	if got := diskNoticesOf(app); len(got) != 1 || got[0].Occurrences > 1 {
		t.Fatalf("a waiting conflict must not notify again: %+v", got)
	}
}

// The second defect: a comparison opened for a conflict stayed open with dead
// tools after the user settled the conflict elsewhere. It now follows its
// sources and closes when nothing is left to choose.
func TestDiskComparisonClosesWhenTheConflictIsSettledElsewhere(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	app := newRemoteJourneyShell(t, rt, root).app
	path := filepath.Join(root, "notes.txt")
	writeFile(t, path, "one\ntwo\n")
	ed := liveTestOpen(t, app, path)
	ed.SetCursorPosition(0, 3)
	ed.InsertRune('!')
	settleLive(t, app)
	writeFile(t, path, "ONE\ntwo\n")
	liveTestPump(t, []*App{app}, ed.DiskConflict)
	runAppCommand(t, app, app.applyDiskChoice(ed, ui.DiskConflictCompare, false))
	view := app.activeLocalDiff()
	if view == nil || !view.screen.Resolvable() || view.screen.ComparisonReader().HunkCount() != 1 {
		t.Fatal("Compare must open a comparison with one change to choose")
	}
	paint := ansi.Strip(app.View().RenderContent())
	for _, want := range []string{"1 change chosen", "Keep on disk", "Keep your text", "Save"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("comparison bar misses %q:\n%s", want, paint)
		}
	}
	for _, unwanted := range []string{path, "Refresh", "Unified / Split", "Open Source"} {
		if strings.Contains(paint, unwanted) {
			t.Fatalf("the comparison repeats %q:\n%s", unwanted, paint)
		}
	}
	if tip := app.tabTooltip(view.tab); !strings.Contains(tip, path) || !strings.Contains(tip, "Left: On disk. Right: Your text.") {
		t.Fatalf("the comparison tab tooltip must name the file and versions: %q", tip)
	}

	// The file changes again while the conflict waits: neither the text nor
	// the save baseline moves, but the comparison follows the file and keeps
	// the choice made for the change that is still the same.
	reader := view.screen.ComparisonReader()
	reader.SetChoices([]diffview.Choice{diffview.ChoiceLeft})
	writeFile(t, path, "ONE\ntwo\nthree\n")
	liveTestPump(t, []*App{app}, func() bool {
		return reader.HunkCount() == 2 && !view.screen.Loading()
	})
	if got := reader.Choices(); len(got) != 2 || got[0] != diffview.ChoiceLeft {
		t.Fatalf("the unchanged change must keep its choice: %v", got)
	}

	// The user discards in the editor instead, through its menu path.
	runAppCommand(t, app, app.discardEditorChanges(ed))
	settleLive(t, app)
	liveTestPump(t, []*App{app}, func() bool { return app.localDiffs[view.tab] == nil })
	if app.Win.IndexOf(view.tab) >= 0 {
		t.Fatal("the settled comparison must close its tab")
	}
	if active := app.Win.ActiveTab(); active == nil || app.editorForTab(active.ID) != ed {
		t.Fatal("closing the comparison must return to the file")
	}
}

// paintedCell finds text in the painted frame and returns its screen cell.
func paintedCell(app *App, text string) (x, y int, ok bool) {
	for row, line := range strings.Split(ansi.Strip(app.View().RenderContent()), "\n") {
		if i := strings.Index(line, text); i >= 0 {
			return len([]rune(line[:i])), row, true
		}
	}
	return 0, 0, false
}
