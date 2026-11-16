package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/diffview"
	"crude/tui/screens/localdiff"
	"crude/tui/ui"
	"crude/workspace"
)

// settleLive runs queued editor commands until every reply is applied.
func settleLive(t *testing.T, app *App) {
	t.Helper()
	for range 6 {
		runAppCommand(t, app, app.drainLiveEditorCommands())
		liveTestSync(t, app)
	}
}

// The reported defect: an agent rewrote a file while the user had unsaved
// changes, and Crude gave no plain way to keep, discard or merge them. Save
// now opens one dialog with Overwrite, Discard, Compare and Cancel, and each
// choice does what it says on the real core document.
func TestDiskConflictDialogOverwriteDiscardAndCompare(t *testing.T) {
	for _, choice := range []string{"overwrite", "discard", "compare"} {
		t.Run(choice, func(t *testing.T) {
			rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			root := t.TempDir()
			app := newRemoteJourneyShell(t, rt, root).app
			path := filepath.Join(root, "conflict.txt")
			if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			ed := liveTestOpen(t, app, path)
			ed.SetCursorPosition(0, 3)
			ed.InsertRune('!')
			settleLive(t, app)
			if err := os.WriteFile(path, []byte("one\ntwo\nTHREE\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runAppCommand(t, app, app.requestEditorSave(ed, workspace.DocumentSaveManual, false))
			settleLive(t, app)
			prompt := app.diskConflict
			if prompt == nil || app.overlayHost.Window(diskConflictOverlay) == nil {
				t.Fatal("Save must ask which version to keep")
			}
			paint := ansi.Strip(app.View().RenderContent())
			for _, want := range []string{"File changed on disk", "Overwrite", "Discard", "Compare", "Cancel"} {
				if !strings.Contains(paint, want) {
					t.Fatalf("dialog misses %q:\n%s", want, paint)
				}
			}
			frame := app.overlayHost.Window(diskConflictOverlay).Content.(*ui.ComponentWidget)
			if focused := ui.ConfirmButtonIndexFromID(frame.Host().Focused()); focused != ui.DiskConflictCancel {
				t.Fatalf("default focus must be Cancel, got button %d", focused)
			}
			if state := app.liveEditors[ed]; state.err != nil {
				t.Fatal("a disk conflict must not block the editor")
			}
			want := ""
			switch choice {
			case "overwrite":
				runAppCommand(t, app, app.resolveDiskConflict(prompt, ui.DiskConflictOverwrite))
				want = "one!\ntwo\nthree\n"
			case "discard":
				runAppCommand(t, app, app.resolveDiskConflict(prompt, ui.DiskConflictDiscard))
				want = "one\ntwo\nTHREE\n"
			case "compare":
				runAppCommand(t, app, app.resolveDiskConflict(prompt, ui.DiskConflictCompare))
				view := app.activeLocalDiff()
				if view == nil || !view.screen.Resolvable() {
					t.Fatal("Compare must open a comparison with choices")
				}
				reader := view.screen.ComparisonReader()
				if got := len(reader.Choices()); got != 2 {
					t.Fatalf("changes = %d, want 2", got)
				}
				// Keep my first change, take the disk version of the second.
				reader.HandleAction(ui.EventContext{}, ui.Action{ID: diffview.ActionNext})
				reader.HandleAction(ui.EventContext{}, ui.Action{ID: diffview.ActionAcceptLeft})
				cmd, _ := app.handleLocalDiffAction(ui.Action{ID: localdiff.ActionSave, Payload: view.screen})
				runAppCommand(t, app, cmd)
				want = "one!\ntwo\nTHREE\n"
			}
			settleLive(t, app)
			if ed.Value() != want || ed.Modified() {
				t.Fatalf("editor = %q modified=%v, want %q saved", ed.Value(), ed.Modified(), want)
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != want {
				t.Fatalf("file = %q %v, want %q", data, err, want)
			}
			if app.diskConflict != nil || app.overlayHost.Window(diskConflictOverlay) != nil {
				t.Fatal("the dialog must close after a choice")
			}
		})
	}
}
