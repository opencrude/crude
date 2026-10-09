package viewmodel

import (
	"path/filepath"
	"testing"

	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
)

// Cancel close must preserve the document and re-arm normal eligible
// scheduling: the prompt suspended the pending After Delay deadline, and a
// dirty buffer with no deadline would never autosave again.
func TestCancelCloseRearmsAfterDelayAutosave(t *testing.T) {
	app := newAutosaveTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	app.editorSettings = types.NormalizeEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay})
	ed.InsertRune('x')
	if got := wsOf(app).Documents.PendingDeadlines(); got != 1 {
		t.Fatalf("typing armed %d deadlines, want 1", got)
	}

	app.showDirtyClosePrompt(ed.FilePath())
	if got := wsOf(app).Documents.PendingDeadlines(); got != 0 {
		t.Fatalf("open close prompt kept %d deadlines, want 0", got)
	}

	app.dirtyPromptCancel()
	if got := wsOf(app).Documents.PendingDeadlines(); got != 1 {
		t.Fatalf("cancel close left %d deadlines, want 1", got)
	}
	if !ed.IsOpen() || !ed.Modified() {
		t.Fatal("cancel close discarded the buffer")
	}

	// Re-arming replaces the deadline instead of stacking a duplicate timer.
	ed.InsertRune('y')
	if got := wsOf(app).Documents.PendingDeadlines(); got != 1 {
		t.Fatalf("edit after cancel left %d deadlines, want 1", got)
	}
}

// Cancel close only re-arms the mode that actually owns a deadline.
func TestCancelCloseArmsNothingWhenAutosaveIsOff(t *testing.T) {
	app := newAutosaveTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	app.editorSettings = types.NormalizeEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveOff})
	ed.InsertRune('x')

	app.showDirtyClosePrompt(ed.FilePath())
	app.dirtyPromptCancel()
	if got := wsOf(app).Documents.PendingDeadlines(); got != 0 {
		t.Fatalf("disabled autosave armed %d deadlines", got)
	}
}

// "Close others" must not discard unsaved buffers. The walk stops at the first
// dirty tab and the unsaved-changes prompt decides its fate.
func TestCloseOthersPromptsInsteadOfDiscardingDirtyBuffer(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	keep := newSaveTestEditorAt(t, app, filepath.Join(root, "keep.go"), "keep")
	first := newSaveTestEditorAt(t, app, filepath.Join(root, "first.go"), "first")
	second := newSaveTestEditorAt(t, app, filepath.Join(root, "second.go"), "second")
	first.InsertRune('x')

	app.closeAllEditorTabsExcept(keep.FilePath())
	if !first.IsOpen() {
		t.Fatal("Close others destroyed a dirty buffer")
	}
	if app.DirtyPromptPath != first.FilePath() {
		t.Fatalf("prompt path=%q want %q", app.DirtyPromptPath, first.FilePath())
	}
	if !second.IsOpen() {
		t.Fatal("Close others closed a tab before the prompt was resolved")
	}

	// "Don't Save" closes the prompted tab and resumes the remaining walk.
	app.dirtyPromptDiscard()
	if first.IsOpen() {
		t.Fatal("discarded tab stayed open")
	}
	if second.IsOpen() {
		t.Fatal("resolved bulk close did not close the remaining tab")
	}
	if !keep.IsOpen() || len(app.EditorState.Tabs) != 1 {
		t.Fatalf("kept tabs=%v", app.EditorState.Tabs)
	}
}

// Cancelling the prompt aborts the rest of a bulk close: nothing is lost and
// nothing else is closed behind the modal.
func TestCancelClosePromptAbortsBulkClose(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	keep := newSaveTestEditorAt(t, app, filepath.Join(root, "keep.go"), "keep")
	first := newSaveTestEditorAt(t, app, filepath.Join(root, "first.go"), "first")
	second := newSaveTestEditorAt(t, app, filepath.Join(root, "second.go"), "second")
	first.InsertRune('x')
	second.InsertRune('y')

	app.closeAllEditorTabsExcept(keep.FilePath())
	if app.DirtyPromptPath != first.FilePath() {
		t.Fatalf("prompt path=%q want %q", app.DirtyPromptPath, first.FilePath())
	}
	app.dirtyPromptCancel()

	if !first.IsOpen() || !second.IsOpen() || !keep.IsOpen() {
		t.Fatal("cancel close closed a tab")
	}
	if len(app.pendingTabClose) != 0 {
		t.Fatalf("cancel left %d queued closes", len(app.pendingTabClose))
	}
}

// "Close to the right" shares the bulk-close guard.
func TestCloseTabsToTheRightPromptsForDirtyBuffer(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	anchor := newSaveTestEditorAt(t, app, filepath.Join(root, "a.go"), "a")
	dirty := newSaveTestEditorAt(t, app, filepath.Join(root, "b.go"), "b")
	clean := newSaveTestEditorAt(t, app, filepath.Join(root, "c.go"), "c")
	dirty.InsertRune('x')
	for _, ed := range []*editor.Editor{anchor, dirty, clean} {
		app.Win.OpenTab(app.editorTabTitle(ed.FilePath()), "✎", true)
	}

	anchorTab := app.Win.TabByTitle(app.editorTabTitle(anchor.FilePath()))
	if anchorTab == nil {
		t.Fatal("anchor tab missing from the strip")
	}
	app.closeEditorTabsToRightOf(app.Win.IndexOf(anchorTab.ID))
	if !dirty.IsOpen() {
		t.Fatal("Close to the right destroyed a dirty buffer")
	}
	if app.DirtyPromptPath != dirty.FilePath() {
		t.Fatalf("prompt path=%q want %q", app.DirtyPromptPath, dirty.FilePath())
	}

	app.dirtyPromptDiscard()
	if dirty.IsOpen() || clean.IsOpen() {
		t.Fatalf("resolved bulk close left tabs open: dirty=%v clean=%v", dirty.IsOpen(), clean.IsOpen())
	}
	if !anchor.IsOpen() {
		t.Fatal("bulk close closed the anchor tab")
	}
}

// The prompt's "Save" choice must also resume a waiting bulk close.
func TestSaveFromDirtyPromptResumesBulkClose(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	keep := newSaveTestEditorAt(t, app, filepath.Join(root, "keep.go"), "keep")
	dirty := newSaveTestEditorAt(t, app, filepath.Join(root, "dirty.go"), "dirty")
	clean := newSaveTestEditorAt(t, app, filepath.Join(root, "clean.go"), "clean")
	dirty.InsertRune('x')

	app.closeAllEditorTabsExcept(keep.FilePath())
	finishSaveCommand(t, app, app.dirtyPromptSave())
	if dirty.IsOpen() {
		t.Fatal("saved-and-closed tab stayed open")
	}
	if clean.IsOpen() {
		t.Fatal("resolved bulk close did not close the remaining tab")
	}
	if !keep.IsOpen() {
		t.Fatal("bulk close closed the surviving tab")
	}
}

// Each dirty buffer gets its own decision: resolving one prompt must not
// silently close the next dirty tab, and a discard must resume the walk.
func TestBulkClosePromptsEachDirtyBufferInTurn(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	keep := newSaveTestEditorAt(t, app, filepath.Join(root, "keep.go"), "keep")
	first := newSaveTestEditorAt(t, app, filepath.Join(root, "first.go"), "first")
	second := newSaveTestEditorAt(t, app, filepath.Join(root, "second.go"), "second")
	first.InsertRune('x')
	second.InsertRune('y')

	app.closeAllEditorTabsExcept(keep.FilePath())
	if app.DirtyPromptPath != first.FilePath() {
		t.Fatalf("prompt path=%q want %q", app.DirtyPromptPath, first.FilePath())
	}
	app.dirtyPromptDiscard()
	if !second.IsOpen() {
		t.Fatal("first decision silently discarded the second dirty buffer")
	}
	if app.DirtyPromptPath != second.FilePath() {
		t.Fatalf("second prompt path=%q want %q", app.DirtyPromptPath, second.FilePath())
	}

	app.dirtyPromptDiscard()
	if second.IsOpen() {
		t.Fatal("second discarded tab stayed open")
	}
	if !keep.IsOpen() || len(app.EditorState.Tabs) != 1 {
		t.Fatalf("kept tabs=%v", app.EditorState.Tabs)
	}
}

// A close deferred until an in-flight writer settles must still resume the bulk
// close once its save acknowledgement lands.
func TestBulkCloseResumesAfterDeferredDiscard(t *testing.T) {
	app := newAutosaveTestApp(t)
	root := t.TempDir()
	keep := newSaveTestEditorAt(t, app, filepath.Join(root, "keep.go"), "keep")
	dirty := newSaveTestEditorAt(t, app, filepath.Join(root, "dirty.go"), "dirty")
	later := newSaveTestEditorAt(t, app, filepath.Join(root, "later.go"), "later")
	dirty.InsertRune('x')
	save := app.requestEditorSave(dirty, workspace.DocumentSaveAfterDelay, false)
	if save == nil {
		t.Fatal("missing save command")
	}

	app.closeAllEditorTabsExcept(keep.FilePath())
	app.dirtyPromptDiscard()
	if !dirty.IsOpen() {
		t.Fatal("tab closed before its owned writer settled")
	}

	finishSaveCommand(t, app, save)
	if dirty.IsOpen() {
		t.Fatal("deferred close did not finish after the writer settled")
	}
	if later.IsOpen() {
		t.Fatal("bulk close did not resume after the deferred discard")
	}
	if !keep.IsOpen() {
		t.Fatal("bulk close closed the surviving tab")
	}
}

// Closing a dirty tab must prompt in every mode: a full-screen overlay or a
// missing modal host may not turn a close key into silent data loss.
func TestDirtyTabClosePromptsOutsideChatMode(t *testing.T) {
	app := newFullTestApp(t)
	ed := newSaveTestEditor(t, app, "base")
	ed.InsertRune('x')
	app.Win.OpenTab(app.editorTabTitle(ed.FilePath()), "✎", true)
	app.OverlayState.Set(workspace.ModeTextOverlay)

	idx := app.Win.IndexOf(app.Win.TabByTitle(app.editorTabTitle(ed.FilePath())).ID)
	if cmd := app.handleAgentTabClose(idx); cmd != nil {
		t.Fatalf("unexpected command %v", cmd != nil)
	}
	if !ed.IsOpen() {
		t.Fatal("dirty tab was closed without a prompt outside chat mode")
	}
	if app.DirtyPromptPath != ed.FilePath() {
		t.Fatalf("prompt path=%q want %q", app.DirtyPromptPath, ed.FilePath())
	}
}

// Workspace teardown must not leave a bulk close queued against replacement tabs.
func TestWorkspaceTeardownDropsPendingBulkClose(t *testing.T) {
	app := newFullTestApp(t)
	root := t.TempDir()
	keep := newSaveTestEditorAt(t, app, filepath.Join(root, "keep.go"), "keep")
	dirty := newSaveTestEditorAt(t, app, filepath.Join(root, "dirty.go"), "dirty")
	later := newSaveTestEditorAt(t, app, filepath.Join(root, "later.go"), "later")
	dirty.InsertRune('x')
	app.closeAllEditorTabsExcept(keep.FilePath())
	if len(app.pendingTabClose) == 0 {
		t.Fatal("bulk close did not queue the remainder")
	}
	if !later.IsOpen() {
		t.Fatal("queued tab closed before its prompt")
	}

	app.closeWorkspaceBoundOverlays()
	if len(app.pendingTabClose) != 0 || app.DirtyPromptPath != "" {
		t.Fatalf("teardown kept pending close state: %v %q", app.pendingTabClose, app.DirtyPromptPath)
	}
}
