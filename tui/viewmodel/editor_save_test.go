package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func newSaveTestEditor(t *testing.T, app *App, content string) *editor.Editor {
	t.Helper()
	return newSaveTestEditorAt(t, app, filepath.Join(t.TempDir(), "save.go"), content)
}

// newAutosaveTestApp builds a test app with the workspace document save
// service the production constructor installs, so autosave scheduling and its
// timers are real.
func newAutosaveTestApp(t *testing.T) *App {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Documents = workspace.NewDocumentSaveService()
	wsOf(app).Documents.SetEventBus(app.eventBus)
	t.Cleanup(wsOf(app).Documents.Close)
	return app
}

// newSaveTestEditorAt opens a tab for an exact path so tests can control tab
// ordering (map iteration would otherwise make bulk operations nondeterministic).
func newSaveTestEditorAt(t *testing.T, app *App, path, content string) *editor.Editor {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := editor.New(app.Theme)
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ed.Close)
	app.EditorState.Editor, app.EditorState.ActiveView = ed, path
	app.EditorState.Tabs[path] = NewEditorTab(path, ed)
	ed.SetOnContentChanged(func(path, text string) { app.onEditorContentChanged(ed, path, text) })
	return ed
}

func finishSaveCommand(t *testing.T, app *App, cmd tea.Cmd) {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		if msg == nil {
			return
		}
		var next tea.Cmd
		switch msg := msg.(type) {
		case editorSaveResultMsg:
			next = app.handleEditorSaveResult(msg)
		case editorFormatResultMsg:
			next = app.handleEditorFormatResult(msg)
		case tea.BatchMsg:
			for _, child := range msg {
				finishSaveCommand(t, app, child)
			}
			return
		default:
			t.Fatalf("unexpected save message %T", msg)
		}
		cmd = next
	}
}

func TestEditorSaveSnapshotDoesNotClearNewerEdits(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	cmd := app.requestEditorSave(ed, workspace.DocumentSaveManual, false)
	if cmd == nil {
		t.Fatal("missing save command")
	}
	ed.InsertRune('y')
	finishSaveCommand(t, app, cmd)
	data, err := os.ReadFile(ed.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "xbase" || ed.Value() != "xybase" || !ed.Modified() {
		t.Fatalf("disk=%q editor=%q dirty=%v", data, ed.Value(), ed.Modified())
	}
	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	if ed.Modified() {
		t.Fatal("latest revision did not become clean")
	}
	ed.Undo()
	if !ed.Modified() {
		t.Fatal("undo to an older snapshot must become dirty")
	}
	ed.Redo()
	if ed.Modified() {
		t.Fatal("redo to saved content must become clean")
	}
}

func TestByteIdenticalManualEditClosesWithoutSavePrompt(t *testing.T) {
	app := newAutosaveTestApp(t)
	app.editorSettings = types.NormalizeEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, AutoSaveDelay: 60_000})
	ed := newSaveTestEditor(t, app, "original bytes\n")
	path := ed.FilePath()
	ed.InsertRune('x')
	if wsOf(app).Documents.PendingDeadlines() != 1 {
		t.Fatal("real edit did not arm autosave")
	}
	ed.HandleEvent(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if ed.Modified() || wsOf(app).Documents.PendingDeadlines() != 0 {
		t.Fatal("byte-identical edit retained dirty state or autosave deadline")
	}
	app.closeEditorTab()
	if app.overlayHost.IsOpen(ui.OverlayDirtyClose) || app.EditorState.Tabs[path] != nil {
		t.Fatal("byte-identical file prompted instead of closing")
	}
}

func TestLateSaveAckAfterUndoClosesWithoutSavePrompt(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	cmd := app.requestEditorSave(ed, workspace.DocumentSaveManual, false)
	ed.InsertRune('y')
	ed.Undo()
	finishSaveCommand(t, app, cmd)
	path := ed.FilePath()
	app.closeEditorTab()
	if app.overlayHost.IsOpen(ui.OverlayDirtyClose) || app.EditorState.Tabs[path] != nil {
		t.Fatal("late acknowledgement re-dirtied byte-identical undo state")
	}
}

func TestAutosavePreservesTextWhileManualSaveFormats(t *testing.T) {
	app := newFullTestApp(t)
	app.editorSettings = types.NormalizeEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, FormatOnSave: true})
	ed := newSaveTestEditor(t, app, "package main\nfunc f(){println(1)}\n")
	ed.InsertRune(' ')
	exact, revision, cursor := ed.Value(), ed.Revision(), ed.CursorCol()
	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveAfterDelay, false))
	if ed.Value() != exact || ed.Revision() != revision || ed.CursorCol() != cursor || ed.Modified() {
		t.Fatal("autosave mutated document or view state")
	}
	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	if ed.Value() == exact || ed.Modified() {
		t.Fatal("explicit formatting did not save formatted content")
	}
	ed.Undo()
	if ed.Value() != exact {
		t.Fatalf("formatting was not one undo step: %q", ed.Value())
	}
}

func TestExternalWriteBlocksSaveWithoutLosingLocalText(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	if err := os.WriteFile(ed.FilePath(), []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveAfterDelay, false))
	data, err := os.ReadFile(ed.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external" || ed.Value() != "xbase" || !ed.Modified() {
		t.Fatal("conflict lost content")
	}
	if cmd := app.requestEditorSave(ed, workspace.DocumentSaveAfterDelay, false); cmd != nil {
		t.Fatal("conflict repeated automatic write")
	}
}

func TestDiscardCancelsQueuedSaveBeforeClosing(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	path := ed.FilePath()
	ed.InsertRune('x')
	cmd := app.requestEditorSave(ed, workspace.DocumentSaveAfterDelay, false)
	app.closeEditorTabForce(path)
	if !ed.IsOpen() {
		t.Fatal("closed before owned writer settled")
	}
	finishSaveCommand(t, app, cmd)
	if ed.IsOpen() {
		t.Fatal("discard did not finish closing")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatalf("discarded snapshot was saved: %q", data)
	}
}

func TestSavedWatcherEchoDoesNotReloadOrClearUndo(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	revision := ed.Revision()
	app.applyEditorDisk(app.refreshEditorDiskCmd(ed)().(editorDiskResultMsg))
	if ed.Revision() != revision {
		t.Fatal("self-event reloaded document")
	}
	ed.Undo()
	if ed.Value() != "base" {
		t.Fatal("self-event cleared undo")
	}
}

func TestDisableCancelsInFlightAutomaticSave(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	cmd := app.requestEditorSave(ed, workspace.DocumentSaveAfterDelay, false)
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveOff})
	finishSaveCommand(t, app, cmd)
	if !ed.Modified() {
		t.Fatal("disabled automatic save cleared dirty state")
	}
	data, err := os.ReadFile(ed.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" {
		t.Fatal("disabled automatic save wrote queued snapshot")
	}
}

func TestExternalDeletionBecomesExplicitRecreatableConflict(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	path := ed.FilePath()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	msg := app.refreshEditorDiskCmd(ed)().(editorDiskResultMsg)
	app.applyEditorDisk(msg)
	if !ed.Modified() || app.editorSaves[ed.BufferID()] == nil {
		t.Fatal("deletion did not become a visible conflict")
	}
	finishSaveCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "base" || ed.Modified() {
		t.Fatalf("recreated data=%q dirty=%v", data, ed.Modified())
	}
}

func TestQuitDoesNotCloseWorkspaceDuringActiveSave(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	app.EditorState.ActiveView = ""
	app.editorSaves = make(map[string]*editorSaveState)
	app.editorSaves[ed.BufferID()] = &editorSaveState{inFlight: true}
	quitCalled := false
	app.OnQuit = func() { quitCalled = true }
	_, cmd := app.handleQuit()
	if quitCalled || cmd != nil {
		t.Fatalf("quitCalled=%v cmd=%v", quitCalled, cmd != nil)
	}
}

func TestInactiveBufferAutosaveDoesNotStealActiveEditor(t *testing.T) {
	app := newFullTestApp(t)
	first := newSaveTestEditor(t, app, "first")
	firstPath := first.FilePath()
	first.InsertRune('x')
	second := newSaveTestEditor(t, app, "second")
	secondPath := second.FilePath()
	finishSaveCommand(t, app, app.requestEditorSave(first, workspace.DocumentSaveAfterDelay, false))
	if app.EditorState.Editor != second || app.EditorState.ActiveView != secondPath {
		t.Fatal("inactive save stole active editor")
	}
	data, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "xfirst" {
		t.Fatalf("inactive disk=%q", data)
	}
}

func TestTerminalBlurAutosavesDirtyBuffers(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveTerminalFocusLoss})
	_, cmd := app.Update(tea.BlurMsg{})
	if cmd == nil {
		t.Fatal("terminal blur did not schedule save")
	}
	runAppCommand(t, app, cmd)
	if ed.Modified() {
		t.Fatal("terminal blur save did not acknowledge buffer")
	}
}

// A rename can land between a committed write and its acknowledgement. The
// acknowledgement belongs to the buffer, not to the path captured at request
// time: dropping it leaves the editor dirty against a stale disk baseline and
// turns the next save into a false "file changed on disk" conflict.
func TestRenameBeforeSaveAcknowledgementKeepsCommittedBaseline(t *testing.T) {
	root := t.TempDir()
	oldPath := filepath.Join(root, "before.go")
	newPath := filepath.Join(root, "after.go")
	if err := os.WriteFile(oldPath, []byte("package before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, _ := newUndrainedTreeMutationApp(t, root)
	if _, err := app.openEditorTab(oldPath); err != nil {
		t.Fatal(err)
	}
	ed := app.EditorState.Editor
	ed.InsertRune('x')
	msg := app.requestEditorSave(ed, workspace.DocumentSaveManual, false)().(editorSaveResultMsg)
	if msg.result.Err != nil || !msg.result.Committed {
		t.Fatalf("save committed=%v err=%v", msg.result.Committed, msg.result.Err)
	}

	// The write is on disk; the acknowledgement is still in flight while the
	// tree renames the file and retargets the open tab.
	app.startRenameInTree(oldPath)
	app.TreePrompt.Edit.SetValue(filepath.Base(newPath))
	updateTreeMutationWithoutDeadlock(t, app, "rename before save acknowledgement")
	if ed.FilePath() != newPath {
		t.Fatalf("rename did not retarget the editor: %q", ed.FilePath())
	}

	app.handleEditorSaveResult(msg)
	if ed.Modified() {
		t.Fatal("rename discarded the committed save acknowledgement")
	}
	if cmd := app.requestEditorSave(ed, workspace.DocumentSaveManual, false); cmd != nil {
		t.Fatal("clean renamed buffer requested another save")
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "xpackage before\n" {
		t.Fatalf("renamed disk=%q", data)
	}
}
