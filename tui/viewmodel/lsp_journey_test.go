package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/editor"
	model "crude/types"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
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

// A detached core review is opaque to the shell. Connected tests separately
// exercise provider normalization, real mutation, canonical publication and undo.
type renamePreviewPort struct {
	result workspace.ReviewedEditResult
	err    error
	calls  int
}

func (p *renamePreviewPort) PrepareLSPRename(context.Context, model.InsightSource, int, int, string) (workspace.ReviewedEditPreview, error) {
	return workspace.ReviewedEditPreview{}, errors.New("unexpected second prepare")
}
func (p *renamePreviewPort) ApplyReviewedEdit(_ context.Context, id, digest string) (workspace.ReviewedEditResult, error) {
	p.calls++
	return p.result, p.err
}
func (p *renamePreviewPort) ReleaseReviewedEdit(context.Context, string, string) error { return nil }
func (p *renamePreviewPort) InspectReviewedEdit(context.Context, string, string) (workspace.ReviewedEditResult, error) {
	return p.result, p.err
}

func TestLSPRenamePreviewCancelAndApplyAreExplicit(t *testing.T) {
	for _, apply := range []bool{false, true} {
		t.Run(fmt.Sprint(apply), func(t *testing.T) {
			app, path, ed := lspJourneyEditor(t, "old\n")
			port := &renamePreviewPort{result: workspace.ReviewedEditResult{ID: "review-test", Digest: "exact-digest", State: "committed", SafetySnapshotID: "safety-123", SafetyReference: "review-test"}}
			request := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, newName: "new", port: port, preview: workspace.ReviewedEditPreview{ID: "review-test", Digest: "exact-digest", Files: []workspace.ReviewedEditFile{{Path: path}}, Text: "- old\n+ new\n"}}
			app.showLSPRenamePreview(request)
			app.View()
			p := app.LSPPopup.RenamePreview
			if p == nil || !strings.Contains(p.area.Value(), "retained Local History safety") {
				t.Fatal("missing exact review and recovery notice")
			}
			action := "cancel"
			if apply {
				action = "apply"
			}
			cmd := app.handleLSPRenameAction(lspRenameActionMsg{p, action})
			if apply {
				if cmd == nil {
					t.Fatal("no apply request")
				}
				result := cmd().(lspRenameAppliedMsg)
				app.handleLSPRenameApplied(result)
				if port.calls != 1 {
					t.Fatal("apply was not exactly once")
				}
			} else {
				if cmd == nil {
					t.Fatal("Cancel did not await exact pending-review release")
				}
				app.handleLSPRenameReleased(cmd().(lspRenameReleasedMsg))
				if port.calls != 0 {
					t.Fatal("Cancel submitted a mutation")
				}
			}
			disk, _ := os.ReadFile(path)
			if string(disk) != "old\n" {
				t.Fatal("shell wrote source from a private preview/receipt")
			}
			if app.LSPPopup.RenamePreview != nil {
				t.Fatal("completed/cancelled preview retained")
			}
		})
	}
}
func TestLSPRenameRefusesStaleSourceAndNeverReportsEmptySafetySuccess(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "old\n")
	port := &renamePreviewPort{result: workspace.ReviewedEditResult{ID: "review-test", Digest: "exact-digest", State: "committed"}}
	request := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, port: port, preview: workspace.ReviewedEditPreview{ID: "review-test", Digest: "exact-digest", Text: "- old\n+ new\n"}}
	app.showLSPRenamePreview(request)
	p := app.LSPPopup.RenamePreview
	cmd := app.handleLSPRenameAction(lspRenameActionMsg{p, "apply"})
	app.handleLSPRenameApplied(cmd().(lspRenameAppliedMsg))
	if app.LSPPopup.RenamePreview != p || !strings.Contains(p.area.Value(), "Outcome unconfirmed") {
		t.Fatal("empty safety became a success")
	}
	if cmd := app.handleLSPRenameAction(lspRenameActionMsg{p, "apply"}); cmd != nil || port.calls != 1 {
		t.Fatal("unknown result was replayed")
	}
	app.overlayHost.Close(lspRenameOverlay)
	app.showLSPRenamePreview(request)
	p = app.LSPPopup.RenamePreview
	ed.InsertRune('x')
	if cmd := app.handleLSPRenameAction(lspRenameActionMsg{p, "apply"}); cmd != nil || port.calls != 1 {
		t.Fatal("stale shared view reached Apply")
	}
}
func TestLSPRenameDialogFitsConstrainedSurface(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "old")
	for _, size := range [][2]int{{160, 40}, {64, 24}} {
		_, _ = app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		msg := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, newName: "new", preview: workspace.ReviewedEditPreview{ID: "review-grid", Files: []workspace.ReviewedEditFile{{Path: path}}, Text: strings.Repeat("review full evidence\n", 100)}}
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
