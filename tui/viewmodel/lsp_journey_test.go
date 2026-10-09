package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	model "crude/types"
	"crude/workspace"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func lspJourneyEditor(t *testing.T, text string) (*App, string, *editor.Editor) {
	t.Helper()
	app := newDiagnosticsTreeApp(t, t.TempDir())
	wsOf(app).LSPMgr.SetRuntime(noopLSPRuntime{})
	path := filepath.Join(app.ws.RootPath(), "lsp-journey.go")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return app, path, app.EditorState.Editor
}
func TestLSPCompletionReplacesPrefixAndRejectsLateOwners(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "fmt.Pr")
	ed.SetCursorPosition(0, 6)
	line, col := ed.LSPCursorPosition()
	response := lspCompletionResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, line: line, col: col, items: []model.CompletionItem{{Label: "Println", InsertText: "Println"}}}
	app.handleAppMessages(response)
	if app.LSPPopup.CompletionID == "" {
		t.Fatal("completion did not render")
	}
	app.acceptCompletion()
	if ed.Value() != "fmt.Println" {
		t.Fatalf("completion duplicated typed prefix: %q", ed.Value())
	}
	ed.Undo()
	if ed.Value() != "fmt.Pr" {
		t.Fatal("completion was not one undo")
	}
	app.handleAppMessages(response)
	if app.LSPPopup.CompletionID != "" {
		t.Fatal("stale revision reopened completion")
	}
	response.revision = ed.Revision()
	response.owner = editor.New(app.Theme)
	app.handleAppMessages(response)
	if app.LSPPopup.CompletionID != "" {
		t.Fatal("foreign editor accepted result")
	}
}
func TestLSPDefinitionNavigatesUTF16AfterTabAndEmoji(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "\t😀name\n")
	app.handleAppMessages(lspDefinitionResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, locations: []editor.LSPLocation{{Path: path, Line: 0, Col: 3}}})
	if ed.CursorCol() != 6 {
		t.Fatalf("UTF16 navigation at visual col=%d want6", ed.CursorCol())
	}
}
func TestLSPRenamePreviewCancelAndApplyAreExplicit(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "apply"}[apply], func(t *testing.T) {
			app, path, ed := lspJourneyEditor(t, "var old = 1\nold\n")
			request := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, newName: "new"}
			edit := &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{protocol.URIFromPath(path): {
				{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 4}, End: protocol.Position{Line: 0, Character: 7}}, NewText: "new"},
				{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 3}}, NewText: "new"},
			}}}
			prepared := prepareLSPRename(app.ws.RootPath(), request, edit)
			if prepared.err != nil {
				t.Fatal(prepared.err)
			}
			if prepared.edits[0].NewContent != "var new = 1\nnew\n" {
				t.Fatalf("rename destroyed suffix: %q", prepared.edits[0].NewContent)
			}
			app.showLSPRenamePreview(prepared)
			if app.LSPPopup.RenamePreview == nil {
				t.Fatal("missing review surface")
			}
			_ = app.View()
			before, _ := os.ReadFile(path)
			if string(before) != "var old = 1\nold\n" {
				t.Fatal("preview wrote files")
			}
			p := app.LSPPopup.RenamePreview
			if !apply {
				app.handleLSPRenameAction(lspRenameActionMsg{p, "cancel"})
				after, _ := os.ReadFile(path)
				if string(after) != string(before) {
					t.Fatal("Cancel changed bytes")
				}
				return
			}
			cmd := app.handleLSPRenameAction(lspRenameActionMsg{p, "apply"})
			if cmd == nil {
				t.Fatal("Apply returned no work")
			}
			result := cmd().(lspRenameAppliedMsg)
			if result.result.Err != nil || result.safety == "" {
				t.Fatalf("rename failed or lacked safety snapshot: %v", result.result.Err)
			}
			app.handleLSPRenameApplied(result)
			after, _ := os.ReadFile(path)
			if string(after) != "var new = 1\nnew\n" {
				t.Fatal(string(after))
			}
		})
	}
}
func TestLSPRenameRefusesStaleDirtyAndMalformedEdit(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "old\n")
	request := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, newName: "new"}
	edit := &protocol.WorkspaceEdit{Changes: map[protocol.DocumentURI][]protocol.TextEdit{protocol.URIFromPath(path): {{Range: protocol.Range{End: protocol.Position{Character: 3}}, NewText: "new"}}}}
	prepared := prepareLSPRename(app.ws.RootPath(), request, edit)
	app.showLSPRenamePreview(prepared)
	p := app.LSPPopup.RenamePreview
	ed.InsertRune('x')
	if cmd := app.handleLSPRenameAction(lspRenameActionMsg{p, "apply"}); cmd != nil {
		t.Fatal("dirty changed preimage submitted")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "old\n" {
		t.Fatal("dirty refusal wrote bytes")
	}
	edit.Changes[protocol.URIFromPath(path)][0].Range.End.Character = 999
	if got := prepareLSPRename(app.ws.RootPath(), request, edit); got.err == nil {
		t.Fatal("out of range edit silently clamped")
	}
}
func TestLSPRenameDialogFitsConstrainedSurface(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "old")
	for _, size := range [][2]int{{160, 40}, {64, 24}} {
		_, _ = app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		msg := lspRenamePreparedMsg{request: lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, newName: "new"}, edits: []workspace.FileEdit{{Path: path, OldContent: "old", NewContent: "new"}}, preview: strings.Repeat("review full evidence\n", 100)}
		app.showLSPRenamePreview(msg)
		_ = app.View()
		fw := app.overlayHost.Window(lspRenameOverlay)
		if fw == nil {
			t.Fatal("missing dialog")
		}
		w, h := fw.Size()
		if w > size[0] || h > size[1] {
			t.Fatal("dialog escaped grid")
		}
		if p := app.LSPPopup.RenamePreview; p.area.Bounds().H <= 0 || p.bar.Bounds().Y >= h {
			t.Fatal("review/action inaccessible")
		}
		app.overlayHost.Close(lspRenameOverlay)
	}
}
