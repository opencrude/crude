package viewmodel

import (
	"crude/textencoding"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/clientstate"
	"crude/document"
	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
)

func TestConnectedDoubleClickCreatesIndependentEditableUntitledBuffers(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	app := shell.app
	original := liveTestOpen(t, app, existing)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[original].topicReady })
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, AutoSaveDelay: 1})

	doubleClick := func() {
		t.Helper()
		_ = app.View()
		rect, ok := app.rootHost.ScreenRect(app.tabScreen.ID())
		if !ok || rect.W < 10 {
			t.Fatal("main tab rail is not mounted")
		}
		for range 2 {
			_, cmd := app.Update(tea.MouseClickMsg{X: rect.X + rect.W - 2, Y: rect.Y, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			_, cmd = app.Update(tea.MouseReleaseMsg{X: rect.X + rect.W - 2, Y: rect.Y, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
		}
	}
	doubleClick()
	ed := app.EditorState.Editor
	if ed == nil || ed == original || ed.FilePath() != "" || !document.IsUntitledID(ed.DocumentState().DocumentID) ||
		ed.BaseTitle() != "Untitled-1" || app.EditorState.ActiveView != ed.DocumentState().DocumentID {
		t.Fatalf("double click did not open an unnamed editor: %+v", ed)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return ed.Value() == "x" && ed.DocumentState().PendingEdits == 0 && !app.liveEditors[ed].sync
	})
	if !ed.Modified() || app.liveEditors[ed].save || !app.liveEditors[ed].due.IsZero() {
		t.Fatal("autosave changed or scheduled an unnamed buffer")
	}
	id := ed.DocumentState().DocumentID
	if tip := app.tabTooltip(app.EditorState.ActiveTab); strings.Contains(tip, "untitled:") {
		t.Fatalf("internal identity leaked in tooltip: %q", tip)
	}
	doubleClick()
	second := app.EditorState.Editor
	if second == nil || second == ed || second.BaseTitle() != "Untitled-2" || second.Value() != "" ||
		second.DocumentState().DocumentID == id || ed.Value() != "x" {
		t.Fatal("another unnamed buffer replaced or shared the first draft")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "existing.txt" {
		t.Fatalf("unnamed editing changed project files: %v, %v", entries, err)
	}
}

func TestConnectedEmptyRailDoubleClickCreatesInReceivingPane(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	paneJourneySize(app, 160, 40)
	liveTestOpen(t, app, path)
	left := app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	right := app.Win.ActiveGroup()
	_ = app.View()
	group := app.mainPanes.groups[left]
	rect, ok := app.rootHost.ScreenRect(group.tabs.ID())
	if !ok || rect.W < 10 || left == right {
		t.Fatal("split tab rail is not mounted")
	}
	click := func() {
		t.Helper()
		x := rect.X + rect.W - 2
		_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: rect.Y, Button: tea.MouseLeft})
		runAppCommand(t, app, cmd)
		_, cmd = app.Update(tea.MouseReleaseMsg{X: x, Y: rect.Y, Button: tea.MouseLeft})
		runAppCommand(t, app, cmd)
	}
	click()
	if app.Win.GroupTabCount(left) != 1 || app.Win.GroupTabCount(right) != 1 {
		t.Fatal("a single empty-rail click created a tab")
	}
	click()
	ed := app.EditorState.Editor
	if ed == nil || !document.IsUntitledID(ed.DocumentState().DocumentID) ||
		app.Win.ActiveGroup() != left || app.Win.GroupTabCount(left) != 2 || app.Win.GroupTabCount(right) != 1 ||
		app.groupEditorView(left, ed.DocumentState().DocumentID) == nil ||
		app.groupEditorView(right, ed.DocumentState().DocumentID) != nil {
		t.Fatal("the new buffer did not appear exactly once in the receiving pane")
	}
}

func TestConnectedUntitledSaveUsesFileWindowAndPreservesText(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(existing, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	app := shell.app
	liveTestOpen(t, app, existing)
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, AutoSaveDelay: 250})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	if !app.overlayHost.IsOpen(ui.OverlayFilePicker) || app.untitledSave == nil {
		t.Fatal("Save did not open the file window")
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	_, cmd = app.Update(tea.PasteMsg{Content: "existing.txt"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	data, err := os.ReadFile(existing)
	if err != nil || string(data) != "existing" || ed.Value() != "x" || !app.overlayHost.IsOpen(ui.OverlayFilePicker) {
		t.Fatalf("existing target lost data or dismissed the draft: %q %v", data, err)
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.PasteMsg{Content: "saved.txt"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	target := filepath.Join(root, "saved.txt")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == target })
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "x" || ed.Modified() || app.overlayHost.IsOpen(ui.OverlayFilePicker) {
		t.Fatalf("first save lost text or did not settle: bytes=%q err=%v dirty=%v", data, err, ed.Modified())
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	runAppCommand(t, app, cmd)
	time.Sleep(300 * time.Millisecond)
	// The coordinator is also called by the normal tick. Invoke that same
	// drain without recursively following the fixture's repeating tick timer.
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !ed.Modified() })
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "xy" {
		t.Fatalf("autosave did not resume after first save: %q %v", data, err)
	}
}

func TestConnectedUntitledOverwriteReviewCancelAndReplace(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("old text\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	id := ed.DocumentState().DocumentID
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	_, cmd := app.Update(tea.PasteMsg{Content: "new text\n"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	_, cmd = app.Update(tea.PasteMsg{Content: "existing.txt"})
	runAppCommand(t, app, cmd)
	submit := func() {
		t.Helper()
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, app, cmd)
		remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.overlayHost.IsOpen(untitledOverwriteOverlay) })
	}
	submit()
	first := app.untitledSave.review.preview
	paint := ansi.Strip(app.View().RenderContent())
	for _, want := range []string{"Replace File", "Local History", "existing.txt", "Compare", "Cancel"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("replacement review did not show %q:\n%s", want, paint)
		}
	}
	if app.LSPPopup.EditPreview != nil || app.activeLocalDiff() != nil || strings.Contains(paint, "old text") {
		t.Fatal("replacement automatically opened a diff instead of a confirmation")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "old text\n" || ed.FilePath() != "" {
		t.Fatalf("review changed the file or draft before consent: %q %v", data, err)
	}
	// The focused default is Cancel.
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	if app.overlayHost.IsOpen(untitledOverwriteOverlay) || !app.overlayHost.IsOpen(ui.OverlayFilePicker) || ed.Value() != "new text\n" {
		t.Fatal("cancelling the replacement did not keep the draft and save window")
	}
	if data, _ := os.ReadFile(target); string(data) != "old text\n" {
		t.Fatal("cancel replaced the file")
	}
	submit()
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		view := app.activeLocalDiff()
		return view != nil && view.screen.Comparison().Before == "old text\n"
	})
	view := app.activeLocalDiff()
	if view.screen.Comparison().After != "new text\n" || app.overlayHost.IsOpen(ui.OverlayFilePicker) {
		t.Fatal("Compare did not use the readable main comparison tab")
	}
	if data, _ := os.ReadFile(target); string(data) != "old text\n" || ed.FilePath() != "" {
		t.Fatal("Compare changed the file or named the draft")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return app.untitledSave != nil && !app.FilePicker.AsyncDirectoryLoading()
	})
	if app.FilePicker.SaveName() != "existing.txt" || app.EditorState.Editor != ed {
		t.Fatal("closing comparison lost the filename or source editor")
	}
	submit()
	second := app.untitledSave.review.preview
	if second.ID == first.ID {
		t.Fatal("a cancelled review was reused")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return ed.FilePath() == target && !app.overlayHost.IsOpen(untitledOverwriteOverlay) && !app.overlayHost.IsOpen(ui.OverlayFilePicker)
	})
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new text\n" || ed.DocumentState().DocumentID != id || ed.Modified() {
		t.Fatalf("replacement lost text or identity: %q %v %+v", data, err, ed.DocumentState())
	}
	result, err := app.ws.(shellport.ReviewedEdits).InspectReviewedEdit(t.Context(), second.ID, second.Digest)
	if err != nil || result.State != "committed" || result.SafetySnapshotID == "" {
		t.Fatalf("replacement did not retain its recoverable outcome: %+v %v", result, err)
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == "" && ed.Modified() })
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.Value() == "new text\n" && !ed.Modified() })
	if ed.DocumentState().DocumentID != id || ed.FilePath() != target {
		t.Fatal("undo/redo changed the saved document identity")
	}
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, AutoSaveDelay: 250})
	ed.SetCursorPosition(0, 0)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	time.Sleep(300 * time.Millisecond)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !ed.Modified() })
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "xnew text\n" {
		t.Fatalf("autosave did not resume after reviewed replacement: %q %v", data, err)
	}
}

func TestConnectedUntitledEncodingChoiceReachesDiskAndAutosave(t *testing.T) {
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	_, cmd := app.Update(tea.PasteMsg{Content: "café 😀\n"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	_, cmd = app.Update(tea.PasteMsg{Content: "encoded.txt"})
	runAppCommand(t, app, cmd)
	for _, key := range []rune{tea.KeyDown, tea.KeyRight, tea.KeyRight, tea.KeyUp, tea.KeyEnter} {
		_, cmd = app.Update(tea.KeyPressMsg{Code: key})
		runAppCommand(t, app, cmd)
	}
	target := filepath.Join(root, "encoded.txt")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == target && !ed.Modified() })
	raw, err := os.ReadFile(target)
	text, encoding, decodeErr := textencoding.DecodeText(raw)
	if err != nil || decodeErr != nil || string(text) != "café 😀\n" || encoding != textencoding.UTF16LE {
		t.Fatalf("encoding selection did not reach disk: %x %v %v", raw, err, decodeErr)
	}
	app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveTerminalFocusLoss})
	_, cmd = app.Update(tea.PasteMsg{Content: "x"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.BlurMsg{})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !ed.Modified() })
	raw, err = os.ReadFile(target)
	text, encoding, decodeErr = textencoding.DecodeText(raw)
	if err != nil || decodeErr != nil || string(text) != ed.Value() || encoding != textencoding.UTF16LE {
		t.Fatalf("autosave lost encoding: %x %v %v", raw, err, decodeErr)
	}
}

func TestConnectedUntitledOverwriteRefusesLaterDiskChange(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	_, cmd := app.Update(tea.PasteMsg{Content: "draft"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	_, cmd = app.Update(tea.PasteMsg{Content: "existing.txt"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.overlayHost.IsOpen(untitledOverwriteOverlay) })
	if err := os.WriteFile(target, []byte("newer work"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return app.untitledSave != nil && app.untitledSave.review == nil
	})
	if paint := ansi.Strip(app.View().RenderContent()); !strings.Contains(paint, "File was not replaced") {
		t.Fatalf("refusal was not visible:\n%s", paint)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "newer work" || ed.FilePath() != "" || ed.Value() != "draft" ||
		!app.overlayHost.IsOpen(ui.OverlayFilePicker) {
		t.Fatalf("stale review lost a file or the draft: disk=%q err=%v draft=%q path=%q", data, err, ed.Value(), ed.FilePath())
	}
}

func TestConnectedUntitledLinkedViewsAndCloseCancel(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "original.txt")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	paneJourneySize(app, 160, 40)
	liveTestOpen(t, app, path)
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	id, left := ed.DocumentState().DocumentID, app.Win.ActiveGroup()
	app.splitEditor(ui.Horizontal, false)
	right := app.Win.ActiveGroup()
	lv, rv := app.groupEditorView(left, id), app.groupEditorView(right, id)
	if lv == nil || rv == nil || lv.Editor == rv.Editor {
		t.Fatal("split did not create linked views")
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	if lv.Editor.Value() != "x" || rv.Editor.Value() != "x" {
		t.Fatal("editing did not update both views")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return lv.Editor.Value() == "" && rv.Editor.Value() == "" })
	_, cmd = app.Update(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return lv.Editor.Value() == "x" && rv.Editor.Value() == "x" })
	app.closeEditorTab() // Closing one view must not discard or prompt.
	if app.overlayHost.IsOpen(ui.OverlayDirtyClose) || len(app.documentViews(id)) != 1 {
		t.Fatal("closing a linked view lost or prompted for shared text")
	}
	app.closeEditorTab()
	if !app.overlayHost.IsOpen(ui.OverlayDirtyClose) {
		t.Fatal("closing the last dirty view did not ask")
	}
	app.dirtyPromptSave()
	if !app.overlayHost.IsOpen(ui.OverlayFilePicker) {
		t.Fatal("Save from the close dialog did not open the file window")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, cmd)
	if app.overlayHost.IsOpen(ui.OverlayFilePicker) || !app.overlayHost.IsOpen(ui.OverlayDirtyClose) || ed.Value() != "x" {
		t.Fatal("cancelling the save window lost the draft or close decision")
	}
	app.dirtyPromptCancel()
	if !ed.IsOpen() || ed.Value() != "x" || !ed.Modified() {
		t.Fatal("cancelling close changed the unnamed draft")
	}
}

func TestConnectedUntitledIgnoresAllAutosaveModesAndReopens(t *testing.T) {
	for _, mode := range []types.AutoSaveMode{types.AutoSaveAfterDelay, types.AutoSaveEditorFocusChange, types.AutoSaveTerminalFocusLoss} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "original.txt")
			if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			runtime := remoteJourneyRuntime(t, "http://unused.invalid")
			shell := newRemoteJourneyShell(t, runtime, root)
			app := shell.app
			liveTestOpen(t, app, path)
			runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
			ed := app.EditorState.Editor
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
			id := ed.DocumentState().DocumentID
			app.applyEditorSettings(types.EditorSettings{AutoSave: mode, AutoSaveDelay: 1})
			_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			runAppCommand(t, app, cmd)
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				return ed.DocumentState().PendingEdits == 0 && !app.liveEditors[ed].sync
			})
			switch mode {
			case types.AutoSaveAfterDelay:
				time.Sleep(5 * time.Millisecond)
				runAppCommand(t, app, app.drainLiveEditorCommands())
			case types.AutoSaveEditorFocusChange:
				_, cmd = app.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl})
				runAppCommand(t, app, cmd)
				if app.EditorState.Editor == ed {
					t.Fatal("focus-change check did not leave the unnamed editor")
				}
			case types.AutoSaveTerminalFocusLoss:
				_, cmd = app.Update(tea.BlurMsg{})
				runAppCommand(t, app, cmd)
			}
			state := app.liveEditors[ed]
			if ed.FilePath() != "" || ed.Value() != "x" || !ed.Modified() || state.save || !state.due.IsZero() ||
				app.overlayHost.IsOpen(ui.OverlayFilePicker) {
				t.Fatal("automatic saving changed, scheduled or prompted for the unnamed draft")
			}
			other := newRemoteJourneyShell(t, runtime, root)
			runAppCommand(t, other.app, other.app.openPaneFiles([]string{id}, other.app.Win.ActiveGroup()))
			restored := other.app.EditorState.Editor
			if restored == nil || restored.DocumentState().DocumentID != id || restored.FilePath() != "" || restored.Value() != "x" {
				t.Fatal("reopening did not preserve the unnamed shared draft")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 || entries[0].Name() != "original.txt" {
				t.Fatalf("automatic saving created a file: %v %v", entries, err)
			}
		})
	}
}

func TestConnectedUntitledPaneMemoryRestoresDraftAndSelection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	first := newRemoteJourneyShell(t, runtime, root)
	liveTestOpen(t, first.app, path)
	runAppCommand(t, first.app, first.app.createPaneUntitled(first.app.Win.ActiveGroup()))
	ed := first.app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{first}, func() bool { return first.app.liveEditors[ed].topicReady })
	id := ed.DocumentState().DocumentID
	_, cmd := first.app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, first.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{first}, func() bool {
		return ed.Value() == "x" && ed.DocumentState().PendingEdits == 0 && !first.app.liveEditors[ed].sync
	})
	raw, err := json.Marshal(first.app.captureViewMemory())
	if err != nil {
		t.Fatal(err)
	}
	var memory viewMemory
	if err := json.Unmarshal(raw, &memory); err != nil || !validViewPanes(memory) {
		t.Fatalf("invalid persisted pane memory: %s: %v", raw, err)
	}
	second := newRemoteJourneyShell(t, runtime, root)
	second.app.restoreViewPanes(memory)
	commands := second.app.editorCommands
	second.app.editorCommands = nil
	for _, command := range commands {
		runAppCommand(t, second.app, command)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{first, second}, func() bool {
		return second.app.EditorState.Editor != nil && second.app.EditorState.ActiveView == id
	})
	restored := second.app.EditorState.Editor
	if restored.DocumentState().DocumentID != id || restored.FilePath() != "" || restored.Value() != "x" || !restored.Modified() {
		t.Fatalf("pane memory lost or replaced the unnamed draft: %q %+v", restored.Value(), restored.DocumentState())
	}
	if second.app.Win.GroupTabCount(second.app.Win.ActiveGroup()) != 2 {
		t.Fatal("pane memory did not restore both named and unnamed tabs")
	}
}

func TestConnectedFirstSaveEnablesFocusAutosave(t *testing.T) {
	for _, mode := range []types.AutoSaveMode{types.AutoSaveEditorFocusChange, types.AutoSaveTerminalFocusLoss} {
		t.Run(string(mode), func(t *testing.T) {
			root := t.TempDir()
			existing := filepath.Join(root, "existing.txt")
			if err := os.WriteFile(existing, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
			app := shell.app
			liveTestOpen(t, app, existing)
			runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
			ed := app.EditorState.Editor
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
			app.applyEditorSettings(types.EditorSettings{AutoSave: mode})
			_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			runAppCommand(t, app, cmd)
			_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			runAppCommand(t, app, cmd)
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				return app.untitledSave != nil && !app.FilePicker.AsyncDirectoryLoading()
			})
			_, cmd = app.Update(tea.PasteMsg{Content: "saved.txt"})
			runAppCommand(t, app, cmd)
			_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, cmd)
			target := filepath.Join(root, "saved.txt")
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == target && !ed.Modified() })
			_, cmd = app.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
			runAppCommand(t, app, cmd)
			if !ed.Modified() {
				t.Fatal("the second edit did not make the saved file dirty")
			}
			if mode == types.AutoSaveEditorFocusChange {
				_, cmd = app.Update(tea.KeyPressMsg{Code: '[', Mod: tea.ModCtrl})
				runAppCommand(t, app, cmd)
				if app.EditorState.Editor == ed {
					t.Fatal("focus did not leave the saved editor")
				}
			} else {
				_, cmd = app.Update(tea.BlurMsg{})
				runAppCommand(t, app, cmd)
			}
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !ed.Modified() })
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "xy" {
				t.Fatalf("focus autosave did not save the named document: %q, %v", data, err)
			}
		})
	}
}

func TestConnectedUntitledReturnsAfterProfileRelease(t *testing.T) {
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runtime := remoteJourneyRuntime(t, "http://unused.invalid")
	root := t.TempDir()
	first := newRemoteJourneyShell(t, runtime, root)
	attachment := attachViewMemoryProfile(t, store, first, "editor-terminal")
	runAppCommand(t, first.app, first.app.executePaletteCommand("file.newBuffer"))
	ed := first.app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{first}, func() bool { return first.app.liveEditors[ed].topicReady })
	_, cmd := first.app.Update(tea.PasteMsg{Content: "retained café 😀"})
	runAppCommand(t, first.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{first}, func() bool { return ed.DocumentState().PendingEdits == 0 && !first.app.liveEditors[ed].sync })
	id := ed.DocumentState().DocumentID
	// The real teardown flush writes the profile. No fixture copies view memory.
	first.app.StopAsyncIO()
	if err := first.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	if err := store.Release(t.Context(), attachment.ClientID, attachment.Epoch); err != nil {
		t.Fatal(err)
	}
	first.app.composerDraftClient, first.app.viewMemory = nil, nil
	back := newRemoteJourneyShell(t, runtime, root)
	attachViewMemoryProfile(t, store, back, "editor-terminal")
	remoteJourneyUntil(t, []remoteJourneyShell{back}, func() bool {
		return back.app.EditorState.Editor != nil && back.app.EditorState.ActiveView == id
	})
	restored := back.app.EditorState.Editor
	if restored.Value() != "retained café 😀" || !restored.Modified() || restored.FilePath() != "" {
		t.Fatalf("returning shell lost its unnamed file: %q %+v", restored.Value(), restored.DocumentState())
	}
	if files, err := os.ReadDir(root); err != nil || len(files) != 0 {
		t.Fatal("profile restoration created a project file:", files, err)
	}
}

func TestConnectedWorkspaceSwitchWaitsForUntitledSaveWindow(t *testing.T) {
	root, target := t.TempDir(), t.TempDir()
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	app := shell.app
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return ed.Value() == "x" && ed.DocumentState().PendingEdits == 0 && !app.liveEditors[ed].sync
	})
	old := app.ws
	runAppCommand(t, app, confirmWorkspaceSwitchTest(t, app, target))
	if app.DirtyPromptPath != ed.DocumentState().DocumentID {
		t.Fatal("switch did not ask about the unnamed draft")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	runAppCommand(t, app, cmd)
	if app.untitledSave == nil || !app.overlayHost.IsOpen(ui.OverlayFilePicker) {
		t.Fatal("switch Save did not open the file window")
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	if app.ws != old || app.workspaceSwitchIntent == nil || !app.workspaceSwitchIntent.saving {
		t.Fatal("opening the save window consumed or completed the switch decision")
	}
	_, cmd = app.Update(tea.PasteMsg{Content: "saved.txt"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.ws != old })
	if app.ws.RootPath() != target {
		t.Fatalf("switch reached a different workspace: %q", app.ws.RootPath())
	}
	data, err := os.ReadFile(filepath.Join(root, "saved.txt"))
	if err != nil || string(data) != "x" {
		t.Fatalf("switch did not save the unnamed draft: %q, %v", data, err)
	}
}
