package viewmodel

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/types"
)

// The first response is a known non-admission, not an uncertain operation.
// The second response is exact evidence for the same unchanged editor source.
type semanticBusyWorkspace struct {
	*semanticCursorWorkspace
	calls atomic.Int32
}

func (w *semanticBusyWorkspace) RequestLSPSemantic(_ context.Context, source types.InsightSource, view string, sequence uint64, span types.LSPSemanticRange) (types.LSPSemanticSet, error) {
	if w.calls.Add(1) == 1 {
		return types.LSPSemanticSet{}, &coreprotocol.Fault{Code: "busy", Message: "capacity occupied"}
	}
	return types.LSPSemanticSet{Boundary: types.InsightBoundary{Source: source, Context: types.InsightContext{Generation: w.service.cursor}, Producer: types.InsightProducer{InstanceID: "provider"}}, WireVersion: 1, ViewID: view, Sequence: sequence, Range: span, Full: true, Types: []string{"function"}, Spans: []types.LSPSemanticSpan{{Start: 0, End: 3, Type: 0}}}, nil
}
func semanticBusyFixture(t *testing.T) (*App, *editor.Editor, *semanticBusyWorkspace) {
	t.Helper()
	app, ws := newRealShellApp(t)
	path := filepath.Join(ws.RootPath, "busy.go")
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	ed := app.EditorState.Editor
	snap := document.Snapshot{ID: "semantic-busy", Path: path, Revision: 1, Text: []byte("abc\n"), Saved: []byte("abc\n"), AnalysisGeneration: "semantic-owner", AnalysisLifetime: "semantic-open", DiskExists: true, DiskHash: sha256.Sum256([]byte("abc\n"))}
	if err := ed.InstallDocument(snap); err != nil {
		t.Fatal(err)
	}
	original := app.ws
	port := &semanticBusyWorkspace{semanticCursorWorkspace: &semanticCursorWorkspace{Workspace: original, service: &semanticCursorService{LSPService: original.LSPMgr(), cursor: 7, producer: types.InsightProducer{InstanceID: "provider"}}}}
	app.ws = port
	t.Cleanup(func() { app.resetSemanticTokens(); app.ws = original })
	app.setFocusEditor()
	app.View()
	return app, ed, port
}

func TestSemanticBusyAdmissionRecoversWithoutAnotherEdit(t *testing.T) {
	app, ed, port := semanticBusyFixture(t)
	runAppCommand(t, app, app.syncSemanticTokens())
	v := app.semanticViews[ed]
	if port.calls.Load() != 2 || v == nil || v.request == nil || !v.request.installed {
		t.Fatalf("a transient busy response left this unchanged editor without semantic colors: calls=%d view=%+v", port.calls.Load(), v)
	}
	app.View()
	r := app.editorRect
	painted := app.mainSurf.Buffer().Get(r.X+ed.PadL+ed.GutterWidth()+1, r.Y)
	want := theme.SyntaxPalette(app.Theme, app.editorSettings.SyntaxTheme).Colors.Code.Function
	if painted.Char != 'b' || !painted.FG.Equal(rendering.ThemeColorToCell(want)) {
		t.Fatal("retried result did not paint source", painted)
	}
	if ed.Value() != "abc\n" || ed.Modified() || ed.CursorLine() != 0 || ed.CursorCol() != 0 {
		t.Fatal("semantic retry edited source or moved the caret")
	}
}

// The retry only applies to a definite refusal. A lost cleanup or a non-busy
// provider error is not replayed, even when the wrapped error says busy.
func TestSemanticRetryDoesNotReplayUncertainOrFailedCalls(t *testing.T) {
	for _, failure := range []error{
		&coreclient.UncertainError{RequestID: "original", Cause: &coreprotocol.Fault{Code: "busy", Message: "unconfirmed"}},
		&coreprotocol.Fault{Code: "semantic_tokens_failed", Message: "invalid provider"},
		errors.New("transport disconnected"),
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			app, ed, port := semanticBusyFixture(t)
			app.syncSemanticTokens()
			view := app.semanticViews[ed]
			if view == nil || view.request == nil {
				t.Fatal("request was not prepared")
			}
			r := view.request
			r.started = true
			if cmd := app.handleSemanticResult(semanticResultMsg{owner: r, err: failure}); cmd != nil || r.err != failure {
				t.Fatal("unconfirmed or failed operation was retried", failure)
			}
			if port.calls.Load() != 0 || ed.Value() != "abc\n" {
				t.Fatal("failure affected source or provider")
			}
		})
	}
}

// Delayed timers cannot start a request after a local edit, context change or
// workspace teardown. No new source, caret or history state is consumed.
func TestSemanticBusyRetryRetiresWithItsOwner(t *testing.T) {
	for _, change := range []string{"edit", "context", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			app, ed, port := semanticBusyFixture(t)
			app.syncSemanticTokens()
			r := app.semanticViews[ed].request
			r.started = true
			cmd := app.handleSemanticResult(semanticResultMsg{owner: r, err: &coreprotocol.Fault{Code: "busy", Message: "refused"}})
			if cmd == nil {
				t.Fatal("busy refusal did not schedule retry")
			}
			switch change {
			case "edit":
				ed.InsertRune('X')
			case "context":
				port.service.cursor++
			case "shutdown":
				app.resetSemanticTokens()
			}
			due, ok := cmd().(semanticDueMsg)
			if !ok {
				t.Fatal("retry timer returned unexpected message")
			}
			if next := app.startSemanticRequest(due); next != nil || port.calls.Load() != 0 {
				t.Fatal("obsolete retry reached provider", change)
			}
		})
	}
}
