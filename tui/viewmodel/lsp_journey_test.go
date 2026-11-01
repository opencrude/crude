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
	// Local editor history is intentionally separate from the canonical chooser
	// fixture: an installed shared document must never pretend local Undo is a
	// canonical receipt. Connected snippet journeys cover the real author history.
	local, _, standalone := lspJourneyEditor(t, "fmt.Pr")
	standalone.SetCursorPosition(0, 6)
	if err := standalone.ApplyCompletion(model.CompletionItem{Label: "Println", InsertText: "Println"}); err != nil {
		t.Fatal(err)
	}
	local.adoptEditorSnippet(standalone)
	if standalone.Value() != "fmt.Println" {
		t.Fatalf("completion duplicated typed prefix: %q", standalone.Value())
	}
	standalone.Undo()
	if standalone.Value() != "fmt.Pr" {
		t.Fatal("completion was not one undo")
	}

	app, port := completionTestApp(t, 120, 40, "fmt.Pr")
	ed := app.EditorState.Editor
	ed.SetCursorPosition(0, 6)
	port.set.Entries = []model.LSPCompletionEntry{{ID: "original-println", Insertion: model.CompletionItem{Label: "Println", InsertText: "Println"}}}
	completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if app.completion == nil || app.completion.screen == nil {
		t.Fatal("completion did not render")
	}
	old := app.completion
	response := completionResultMsg{owner: old, set: *old.set}
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEnter})
	if ed.Value() != "fmt.Println" {
		t.Fatalf("completion duplicated typed prefix: %q", ed.Value())
	}
	app.Update(response)
	if app.completion != nil {
		t.Fatal("retired result reopened completion after acceptance")
	}
	foreign := *old
	foreign.editor = editor.New(app.Theme)
	t.Cleanup(foreign.editor.Close)
	response.owner = &foreign
	app.Update(response)
	if app.completion != nil {
		t.Fatal("foreign editor accepted result")
	}
}
func TestLSPDefinitionNavigatesUTF16AfterTabAndEmoji(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedPoint, "\t😀name\n")
	one := f.shells[0]
	f.key(t, tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.EditorState.Editor.FilePath() == f.engine.target && one.app.navigation == nil
	})
	ed := one.app.EditorState.Editor
	if ed.CursorCol() != 6 || ed.CursorLine() != 0 || ed.HasSelection() || ed.Value() != "\t😀name\n" {
		t.Fatalf("qualified UTF16 navigation at visual %d:%d source=%q want 0:6", ed.CursorLine(), ed.CursorCol(), ed.Value())
	}
	if f.main.CursorLine() != 2 || f.main.CursorCol() != 4 {
		t.Fatal("asynchronous definition moved the old source editor")
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
			app.showRenameReview(request)
			app.View()
			p := app.LSPPopup.EditPreview
			if p == nil || !strings.Contains(p.area.Value(), "retained Local History safety") {
				t.Fatal("missing exact review and recovery notice")
			}
			action := "cancel"
			if apply {
				action = "apply"
			}
			cmd := app.handleReviewedEditAction(reviewedEditActionMsg{p, action})
			if apply {
				if cmd == nil {
					t.Fatal("no apply request")
				}
				result := cmd().(reviewedEditAppliedMsg)
				app.handleReviewedEditApplied(result)
				if port.calls != 1 {
					t.Fatal("apply was not exactly once")
				}
			} else {
				if cmd == nil {
					t.Fatal("Cancel did not await exact pending-review release")
				}
				app.handleReviewedEditReleased(cmd().(reviewedEditReleasedMsg))
				if port.calls != 0 {
					t.Fatal("Cancel submitted a mutation")
				}
			}
			disk, _ := os.ReadFile(path)
			if string(disk) != "old\n" {
				t.Fatal("shell wrote source from a private preview/receipt")
			}
			if app.LSPPopup.EditPreview != nil {
				t.Fatal("completed/cancelled preview retained")
			}
		})
	}
}
func TestLSPRenameRefusesStaleSourceAndNeverReportsEmptySafetySuccess(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "old\n")
	port := &renamePreviewPort{result: workspace.ReviewedEditResult{ID: "review-test", Digest: "exact-digest", State: "committed"}}
	request := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, port: port, preview: workspace.ReviewedEditPreview{ID: "review-test", Digest: "exact-digest", Text: "- old\n+ new\n"}}
	app.showRenameReview(request)
	p := app.LSPPopup.EditPreview
	cmd := app.handleReviewedEditAction(reviewedEditActionMsg{p, "apply"})
	app.handleReviewedEditApplied(cmd().(reviewedEditAppliedMsg))
	if app.LSPPopup.EditPreview != p || !strings.Contains(p.area.Value(), "Outcome unconfirmed") {
		t.Fatal("empty safety became a success")
	}
	if cmd := app.handleReviewedEditAction(reviewedEditActionMsg{p, "apply"}); cmd != nil || port.calls != 1 {
		t.Fatal("unknown result was replayed")
	}
	app.overlayHost.Close(reviewedEditOverlay)
	app.showRenameReview(request)
	p = app.LSPPopup.EditPreview
	ed.InsertRune('x')
	if cmd := app.handleReviewedEditAction(reviewedEditActionMsg{p, "apply"}); cmd != nil || port.calls != 1 {
		t.Fatal("stale shared view reached Apply")
	}
}
func TestLSPRenameDialogFitsConstrainedSurface(t *testing.T) {
	app, path, ed := lspJourneyEditor(t, "old")
	for _, size := range [][2]int{{160, 40}, {64, 24}} {
		_, _ = app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		msg := lspRenameResultMsg{owner: ed, revision: ed.Revision(), generation: app.workspaceSwitchGen.Load(), filePath: path, newName: "new", preview: workspace.ReviewedEditPreview{ID: "review-grid", Files: []workspace.ReviewedEditFile{{Path: path}}, Text: strings.Repeat("review full evidence\n", 100)}}
		app.showRenameReview(msg)
		_ = app.View()
		fw := app.overlayHost.Window(reviewedEditOverlay)
		if fw == nil {
			t.Fatal("missing dialog")
		}
		w, h := fw.Size()
		if w > size[0] || h > size[1] {
			t.Fatal("dialog escaped grid")
		}
		if p := app.LSPPopup.EditPreview; p.area.Bounds().H <= 0 || p.bar.Bounds().Y >= h {
			t.Fatal("review/action inaccessible")
		}
		app.overlayHost.Close(reviewedEditOverlay)
	}
}
