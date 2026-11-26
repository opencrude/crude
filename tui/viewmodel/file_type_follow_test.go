package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/core/coreprotocol"
	"crude/internal/clientstate"
	"crude/tui/screens/markdownview"
	"crude/tui/ui"
)

// typeInto sends text to the focused editor as real key presses.
func typeInto(t *testing.T, app *App, text string) {
	t.Helper()
	for _, r := range text {
		key := tea.KeyPressMsg{Code: r, Text: string(r)}
		if r == '\n' {
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		_, cmd := app.Update(key)
		runAppCommand(t, app, cmd)
	}
}

// renderedPreview waits for the focused Markdown tab's rendering of the
// editor's current revision and returns the painted preview text.
func renderedPreview(t *testing.T, shell remoteJourneyShell) string {
	t.Helper()
	app := shell.app
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		_ = app.View()
		md := app.activeMarkdown()
		return md != nil && md.Document() != nil && md.Document().Revision == app.EditorState.Editor.Revision()
	})
	_ = app.View()
	return screenText(app, app.mainScreenRect(app.activeMarkdown().PreviewRect()))
}

// A new buffer saved as a Markdown file shows its preview at once. The text
// stays editable beside it, so typing after Save is never lost to a preview.
func TestNewBufferSavedAsMarkdownShowsPreviewWithoutReopening(t *testing.T) {
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	app := shell.app
	paneJourneySize(app, 140, 32)
	runAppCommand(t, app, app.createPaneUntitled(app.Win.ActiveGroup()))
	ed := app.EditorState.Editor
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.liveEditors[ed].topicReady })
	typeInto(t, app, "# Notes\n\nFirst **draft**.")
	_ = app.View()
	if app.activeMarkdown() != nil {
		t.Fatal("an unnamed buffer showed Markdown views before it had a name")
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return !app.FilePicker.AsyncDirectoryLoading() })
	_, cmd = app.Update(tea.PasteMsg{Content: "notes.md"})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	target := filepath.Join(root, "notes.md")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == target && !ed.Modified() })

	_ = app.View()
	md := app.activeMarkdown()
	if md == nil {
		t.Fatal("the saved Markdown file showed no Markdown views until the tab was reopened")
	}
	if md.Mode() != markdownview.Split || md.SourceRect().Empty() || md.PreviewRect().Empty() {
		t.Fatalf("the saved file opened in %q; the text being written must stay on screen beside the preview", md.Mode())
	}
	button := app.rootHost.Component(ui.NewID(ui.NewID(md.ID(), "toolbar"), string(markdownview.Split)))
	if button == nil || !button.Semantics().Selected {
		t.Fatal("the Markdown toolbar does not show the selected view")
	}
	if text := renderedPreview(t, shell); !strings.Contains(text, "Notes") || strings.Contains(text, "# Notes") || strings.Contains(text, "**draft**") {
		t.Fatalf("the preview does not render the saved file:\n%s", text)
	}
	typeInto(t, app, " More")
	if !strings.HasSuffix(ed.Value(), "First **draft**. More") {
		t.Fatalf("typing after Save did not reach the source: %q", ed.Value())
	}
	if text := renderedPreview(t, shell); !strings.Contains(text, "More") {
		t.Fatalf("the preview did not follow the edit:\n%s", text)
	}
}

// Renaming an open file changes its viewer with its file type, in both
// directions, without closing the tab or losing unsaved text.
func TestRenamedFileFollowsItsNewFileType(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(plain, []byte("# Plan\n\nSteps.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), root)
	app := shell.app
	paneJourneySize(app, 140, 32)
	ed := liveTestOpen(t, app, plain)
	if app.activeMarkdown() != nil {
		t.Fatal("a text file opened with Markdown views")
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	typeInto(t, app, "x")
	draft := ed.Value()
	if draft != "# Plan\n\nSteps.\nx" {
		t.Fatalf("precondition: the draft is %q", draft)
	}

	markdown := filepath.Join(root, "notes.md")
	if err := app.relocateTreePath(plain, markdown); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == markdown })
	_ = app.View()
	md := app.activeMarkdown()
	if md == nil || md.Mode() != markdownview.Split {
		t.Fatalf("renaming to .md did not show the Markdown views beside the text: %v", md)
	}
	if text := renderedPreview(t, shell); !strings.Contains(text, "Plan") || strings.Contains(text, "# Plan") {
		t.Fatalf("the renamed file's preview does not render it:\n%s", text)
	}
	if ed.Value() != draft || !ed.Modified() {
		t.Fatal("the rename lost unsaved text")
	}

	// A Markdown file renamed to another Markdown name keeps the view the user chose.
	app.setMarkdownMode(markdownview.Preview)
	moved := filepath.Join(root, "plan.md")
	if err := app.relocateTreePath(markdown, moved); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == moved })
	_ = app.View()
	if got := app.activeMarkdown(); got != md || got.Mode() != markdownview.Preview {
		t.Fatal("a rename between Markdown names replaced the tab's chosen view")
	}

	back := filepath.Join(root, "plan.txt")
	if err := app.relocateTreePath(moved, back); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return ed.FilePath() == back })
	_ = app.View()
	if app.activeMarkdown() != nil || app.markdownPreviewOwnsKeys() {
		t.Fatal("a file renamed away from .md kept its Markdown views")
	}
	typeInto(t, app, "y")
	if want := draft + "y"; ed.Value() != want {
		t.Fatalf("keys did not reach the editor after the Markdown views closed: %q, want %q", ed.Value(), want)
	}
}

// Problems found under a file's old name leave with it. The editor keeps no
// facts that its painted marks could come from; the new name's own problems
// arrive from the same authority as at open.
func TestRenamedFileDropsProblemsOfItsOldName(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	app.drainLSPDiagnostics()
	if len(ed.Diagnostics()) != 1 {
		t.Fatal("precondition: the problem is not shown under the old name")
	}
	oldPath := ed.FilePath()
	newPath := filepath.Join(filepath.Dir(oldPath), "renamed.md")
	if err := ed.RetargetDocumentPath(newPath); err != nil {
		t.Fatal(err)
	}
	app.retitleEditorWindowTab(oldPath, newPath, ed)
	if got := ed.DiagnosticFacts(); got.ID != "" || len(ed.DiagnosticsAt(0, 6)) != 0 {
		t.Fatalf("the renamed file kept the problems of its old name: %s", got.ID)
	}
	if err := ed.RetargetDocumentPath(oldPath); err != nil {
		t.Fatal(err)
	}
	app.retitleEditorWindowTab(newPath, oldPath, ed)
	if ed.DiagnosticFacts().ID != facts.ID {
		t.Fatal("renaming back did not show the problems the authority holds for that name")
	}
}

// Opening a new Markdown file directly (crude new.md) shows its text ready to
// type, not an empty preview that ignores keys. The file appears only on Save.
func TestFileEntryNewMarkdownFileOpensReadyToType(t *testing.T) {
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "clients.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	shell := newRemoteJourneyShellWithEntry(t, remoteJourneyRuntime(t, "http://unused.invalid"), root, coreprotocol.EntryIntent{Explicit: true, FileOnly: true})
	app := shell.app
	path := filepath.Join(root, "new.md")
	app.SetLaunchFile(path, true)
	attachViewMemoryProfile(t, store, shell, "file-entry-markdown")
	runAppCommand(t, app, app.drainLiveEditorCommands())
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return app.EditorState.ActiveView == path && app.liveEditors[app.EditorState.Editor].topicReady
	})
	paneJourneySize(app, 140, 32)
	ed := app.EditorState.Editor
	md := app.activeMarkdown()
	if md == nil || md.Mode() != markdownview.Split || app.markdownPreviewOwnsKeys() {
		t.Fatalf("a new Markdown file opened without its text on screen: %v", md)
	}
	typeInto(t, app, "# Hello")
	if ed.Value() != "# Hello" {
		t.Fatalf("typing into the new file was lost: %q", ed.Value())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("opening or typing created the file before Save", err)
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		data, e := os.ReadFile(path)
		return e == nil && string(data) == "# Hello"
	})
	if got := app.activeMarkdown(); got != md || got.Mode() != markdownview.Split {
		t.Fatal("saving the new file changed its view")
	}
	if text := renderedPreview(t, shell); !strings.Contains(text, "Hello") || strings.Contains(text, "# Hello") {
		t.Fatalf("the new file's preview does not render it:\n%s", text)
	}
}
