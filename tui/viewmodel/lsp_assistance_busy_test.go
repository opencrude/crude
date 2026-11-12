package viewmodel

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/tui/editor"
	"crude/types"
)

type retryCompletionPort struct {
	*completionTestPort
	first error
	calls atomic.Int32
}

func (p *retryCompletionPort) RequestLSPCompletions(ctx context.Context, source types.InsightSource, view string, sequence uint64, line, col int, trigger types.LSPCompletionContext) (types.LSPCompletionSet, error) {
	if p.calls.Add(1) == 1 {
		return types.LSPCompletionSet{}, p.first
	}
	return p.completionTestPort.RequestLSPCompletions(ctx, source, view, sequence, line, col, trigger)
}
func TestCompletionKnownBusyRecoversWithoutAnotherEdit(t *testing.T) {
	app, base := completionTestApp(t, 80, 24, "pr")
	port := &retryCompletionPort{completionTestPort: base, first: &coreprotocol.Fault{Code: "busy", Message: "capacity occupied"}}
	app.ws = port
	completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if port.calls.Load() != 2 || app.completion == nil || !app.completion.shown || app.completion.screen == nil {
		t.Fatal("busy completion did not recover for the same source", port.calls.Load())
	}
	if app.EditorState.Editor.Value() != "pr" || app.EditorState.Editor.Modified() || app.Input.Value() != "keep completion private draft" {
		t.Fatal("completion retry changed source or draft")
	}
}
func TestCompletionUncertainOrFailedAdmissionIsNotReplayed(t *testing.T) {
	for _, failure := range []error{&coreclient.UncertainError{RequestID: "original", Cause: &coreprotocol.Fault{Code: "busy"}}, &coreprotocol.Fault{Code: "completion_failed"}, errors.New("connection lost"), errors.Join(&coreprotocol.Fault{Code: "busy"}, context.Canceled), errors.Join(&coreprotocol.Fault{Code: "busy"}, context.DeadlineExceeded)} {
		t.Run(failure.Error(), func(t *testing.T) {
			app, base := completionTestApp(t, 80, 24, "pr")
			port := &retryCompletionPort{completionTestPort: base, first: failure}
			app.ws = port
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			if port.calls.Load() != 1 || app.EditorState.Editor.Value() != "pr" {
				t.Fatal("unconfirmed completion was replayed", port.calls.Load())
			}
		})
	}
}

type retryInlayPort struct {
	*inlayCursorWorkspace
	first error
	calls atomic.Int32
}

func (p *retryInlayPort) RequestLSPInlayHints(_ context.Context, source types.InsightSource, view string, seq uint64, span types.LSPInlayRange) (types.LSPInlaySet, error) {
	if p.calls.Add(1) == 1 {
		return types.LSPInlaySet{}, p.first
	}
	return types.LSPInlaySet{ID: "inlay-result", Digest: strings.Repeat("a", 64), ViewID: view, Sequence: seq, Range: span, WireVersion: 1,
		Boundary: types.InsightBoundary{Source: source, Context: types.InsightContext{Generation: p.service.cursor}, Producer: types.InsightProducer{InstanceID: "independent-provider"}},
		Answer:   "hints", ExpiresAt: time.Now().Add(time.Minute), Hints: []types.LSPInlayHint{{Index: 0, SourceByte: 1, Position: types.LSPInlayPosition{Line: 0, Character: 1}, Label: "hint"}}}, nil
}
func inlayRetryFixture(t *testing.T, first error) (*App, *editor.Editor, *retryInlayPort) {
	t.Helper()
	app, ws := newRealShellApp(t)
	path := filepath.Join(ws.RootPath, "inlay.go")
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	ed := app.EditorState.Editor
	snapshot := document.Snapshot{ID: "inlay-retry", Path: path, Revision: 1, Text: []byte("abc\n"), Saved: []byte("abc\n"), DiskExists: true, DiskHash: sha256.Sum256([]byte("abc\n")), AnalysisGeneration: "inlay-replica-owner", AnalysisLifetime: "inlay-open"}
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	original := app.ws
	service := &inlayCursorService{LSPService: original.LSPMgr(), cursor: 7}
	port := &retryInlayPort{inlayCursorWorkspace: &inlayCursorWorkspace{Workspace: original, service: service}, first: first}
	app.ws = port
	t.Cleanup(func() { app.resetInlayHints(); app.ws = original })
	app.Input.SetInput("inlay retry draft")
	app.setFocusEditor()
	app.View()
	return app, ed, port
}
func TestInlayKnownBusyRecoversWithoutAnotherEdit(t *testing.T) {
	app, ed, port := inlayRetryFixture(t, &coreprotocol.Fault{Code: "busy", Message: "capacity occupied"})
	runAppCommand(t, app, app.syncInlayHints())
	if port.calls.Load() != 2 || !ed.HasInlayProjection() {
		t.Fatal("busy inlay did not recover for the same source", port.calls.Load())
	}
	if ed.Value() != "abc\n" || ed.Modified() || app.Input.Value() != "inlay retry draft" {
		t.Fatal("inlay retry changed source or draft")
	}
}
func TestInlayUncertainOrFailedAdmissionIsNotReplayed(t *testing.T) {
	for _, failure := range []error{&coreclient.UncertainError{RequestID: "original", Cause: &coreprotocol.Fault{Code: "busy"}}, &coreprotocol.Fault{Code: "inlay_hints_failed"}, errors.New("connection lost"), errors.Join(&coreprotocol.Fault{Code: "busy"}, context.Canceled), errors.Join(&coreprotocol.Fault{Code: "busy"}, context.DeadlineExceeded)} {
		t.Run(failure.Error(), func(t *testing.T) {
			app, ed, port := inlayRetryFixture(t, failure)
			runAppCommand(t, app, app.syncInlayHints())
			if port.calls.Load() != 1 || ed.Value() != "abc\n" {
				t.Fatal("unconfirmed inlay was replayed", port.calls.Load())
			}
		})
	}
}

// A definite refusal may retry only while the same user-visible source and
// view still own the request. A queued timer is not permission to retarget it.
func TestCompletionBusyRetryRetiresWithItsOwner(t *testing.T) {
	for _, change := range []string{"escape", "edit", "context", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			app, base := completionTestApp(t, 80, 24, "pr")
			port := &retryCompletionPort{completionTestPort: base, first: &coreprotocol.Fault{Code: "busy"}}
			app.ws = port
			cmd := app.requestCompletionCmd()
			if cmd == nil {
				t.Fatal("manual completion was not admitted")
			}
			result, ok := cmd().(completionResultMsg)
			if !ok {
				t.Fatal("missing first result")
			}
			retry := app.handleCompletionResult(result)
			if retry == nil {
				t.Fatal("definite refusal did not schedule a retry")
			}
			switch change {
			case "escape":
				app.closeCompletionPopup()
			case "edit":
				app.EditorState.Editor.InsertRune('X')
			case "context":
				port.service.context++
			case "shutdown":
				app.resetLSPRequestContext()
			}
			due, ok := retry().(completionRetryDueMsg)
			if !ok {
				t.Fatal("unexpected retry timer")
			}
			if next := app.startCompletionRequest(due.owner); next != nil || port.calls.Load() != 1 {
				t.Fatal("obsolete completion retry reached provider", change)
			}
			if app.Input.Value() != "keep completion private draft" {
				t.Fatal("retry consumed the draft")
			}
		})
	}
}
func TestInlayBusyRetryRetiresWithItsOwner(t *testing.T) {
	for _, change := range []string{"hide", "edit", "context", "shutdown"} {
		t.Run(change, func(t *testing.T) {
			app, ed, port := inlayRetryFixture(t, &coreprotocol.Fault{Code: "busy"})
			app.syncInlayHints()
			view := app.inlayViews[ed]
			if view == nil || view.request == nil {
				t.Fatal("inlay was not admitted")
			}
			request := view.request
			cmd := app.startInlayRequest(inlayDueMsg{owner: request})
			if cmd == nil {
				t.Fatal("inlay did not start")
			}
			result, ok := cmd().(inlayResultMsg)
			if !ok {
				t.Fatal("missing first result")
			}
			retry := app.handleInlayResult(result)
			if retry == nil {
				t.Fatal("definite refusal did not schedule a retry")
			}
			switch change {
			case "hide":
				app.setInlayViewHidden(true)
			case "edit":
				ed.InsertRune('X')
			case "context":
				port.service.cursor++
			case "shutdown":
				app.resetInlayHints()
			}
			due, ok := retry().(inlayDueMsg)
			if !ok {
				t.Fatal("unexpected retry timer")
			}
			if next := app.startInlayRequest(due); next != nil || port.calls.Load() != 1 {
				t.Fatal("obsolete inlay retry reached provider", change)
			}
			if app.Input.Value() != "inlay retry draft" {
				t.Fatal("retry consumed the draft")
			}
		})
	}
}
