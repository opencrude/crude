package viewmodel

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/document"
	"crude/shellport"
	"crude/types"
)

type inlayCursorService struct {
	shellport.LSPService
	cursor         uint64
	producer       *types.InsightProducer
	unsynchronized bool
}

func (s *inlayCursorService) ServerReady() bool { return true }
func (s *inlayCursorService) FileProducer(string) types.InsightProducer {
	if s.producer != nil {
		return *s.producer
	}
	return types.InsightProducer{InstanceID: "independent-provider"}
}
func (s *inlayCursorService) FileSynchronized(types.InsightSource, uint64) bool {
	return !s.unsynchronized
}
func (s *inlayCursorService) InlayHintsState() (bool, uint64) {
	return true, 1
}
func (s *inlayCursorService) ContextCursor() (string, uint64) {
	return "inlay-replica-owner", s.cursor
}

type inlayCursorWorkspace struct {
	shellport.Workspace
	service *inlayCursorService
}

func (w *inlayCursorWorkspace) LSPMgr() shellport.LSPService { return w.service }
func (*inlayCursorWorkspace) RequestLSPInlayHints(context.Context, types.InsightSource, string, uint64, types.LSPInlayRange) (types.LSPInlaySet, error) {
	panic("context barrier test must deliver only its independently constructed result")
}
func (*inlayCursorWorkspace) InspectLSPInlayHint(context.Context, string, string, int, bool) (types.LSPInlayInspection, error) {
	panic("context barrier test must never dispatch original-object inspection")
}
func (*inlayCursorWorkspace) ReleaseLSPInlayHints(context.Context, string, uint64) error {
	return nil
}

// The LSP result and state-topic cursor are independent carriers. Positive and
// negative evidence must not escape the exact same publication barrier.
func TestInlayEveryAnswerWaitsForTheOriginalContextCursor(t *testing.T) {
	for _, answer := range []string{"hints", "null", "empty", "unsupported"} {
		t.Run(answer, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			path := filepath.Join(ws.RootPath, "barrier.go")
			if _, err := app.openEditorTab(path); err != nil {
				t.Fatal(err)
			}
			ed := app.EditorState.Editor
			snapshot := document.Snapshot{ID: "inlay-barrier-document", Path: path, Revision: 1,
				Text: []byte("abc\n"), Saved: []byte("abc\n"), AnalysisGeneration: "inlay-replica-owner",
				AnalysisLifetime: "inlay-barrier-open", DiskExists: true, DiskHash: sha256.Sum256([]byte("abc\n"))}
			if err := ed.InstallDocument(snapshot); err != nil {
				t.Fatal(err)
			}
			original := app.ws
			service := &inlayCursorService{LSPService: original.LSPMgr(), cursor: 7}
			app.ws = &inlayCursorWorkspace{Workspace: original, service: service}
			t.Cleanup(func() { app.resetInlayHints(); app.ws = original })
			app.Input.SetInput("barrier private draft")
			app.setFocusEditor()
			app.paintShellFrame()
			app.openInlayInspectorCmd() // admits a debounce, but dispatch is not run
			s := app.inlayInspector
			if s == nil || s.request == nil || s.shown || ed.HasInlayProjection() {
				t.Fatal("original view did not enter read-only loading")
			}
			r := s.request
			result := types.LSPInlaySet{ID: "independent-result", Digest: strings.Repeat("a", 64),
				ViewID: r.view.id, Sequence: r.sequence, WireVersion: 2, Range: r.span,
				Boundary: types.InsightBoundary{Source: r.source,
					Context:  types.InsightContext{Generation: 8},
					Producer: types.InsightProducer{InstanceID: "independent-provider"}},
				Answer: answer, ExpiresAt: time.Now().Add(time.Minute)}
			if answer == "hints" {
				result.Hints = []types.LSPInlayHint{{Index: 0, SourceByte: 1,
					Position: types.LSPInlayPosition{Line: 0, Character: 1}, Label: "TYPE"}}
			}
			app.handleInlayResult(inlayResultMsg{owner: r, result: result})
			app.syncInlayHints()
			if r.result == nil || r.installed || s.shown || ed.HasInlayProjection() {
				t.Fatal("answer was painted/reported before its original context cursor", answer)
			}
			service.cursor = 8
			app.syncInlayHints()
			if !r.installed || !s.shown || s.screen.InList() != (answer == "hints") ||
				s.screen.InDetail() || !ed.HasInlayProjection() {
				t.Fatal("exact context observation did not publish the accepted answer", answer)
			}
			if answer != "hints" {
				want := map[string]string{"null": "Provider returned null.", "empty": "Provider returned an empty array [].",
					"unsupported": "Inlay hints are unsupported for this source/provider."}[answer]
				if text := inlayVisualReader(s); !strings.Contains(text, want) ||
					!strings.Contains(text, "not proof that no hints exist or that the project is clean") {
					t.Fatal("published negative answer lost its original meaning and limits", answer, text)
				}
			}
			service.cursor = 9
			app.syncInlayHints()
			if app.inlayInspector != nil || ed.HasInlayProjection() || r.ctx.Err() == nil ||
				app.inlayViews[ed].request == r {
				t.Fatal("newer context retained old rendered/inspectable evidence", answer)
			}
			if ed.Value() != "abc\n" || ed.Modified() || ed.DocumentSnapshot().Revision != 1 ||
				app.Input.Value() != "barrier private draft" {
				t.Fatal("publication barrier modified source or private draft")
			}
		})
	}
}

func TestInlayWaitsForSynchronizedFileAndRetiresReplacedProducer(t *testing.T) {
	app, ed, port := inlayRetryFixture(t, nil)
	port.service.unsynchronized = true
	app.syncInlayHints()
	view := app.inlayViews[ed]
	if view == nil || view.request != nil || port.calls.Load() != 0 {
		t.Fatal("unsynchronized file admitted an inlay request")
	}
	port.service.unsynchronized = false
	app.syncInlayHints()
	first := view.request
	if first == nil {
		t.Fatal("synchronization did not admit inlays")
	}
	port.service.unsynchronized = true
	if cmd := app.startInlayRequest(inlayDueMsg{owner: first}); cmd != nil || view.request != nil || first.ctx.Err() == nil {
		t.Fatal("revoked synchronization admitted an old timer")
	}
	port.service.unsynchronized = false
	app.syncInlayHints()
	first = view.request
	replacement := types.InsightProducer{InstanceID: "replacement"}
	port.service.producer = &replacement
	if cmd := app.startInlayRequest(inlayDueMsg{owner: first}); cmd != nil {
		t.Fatal("old provider request started")
	}
	app.syncInlayHints()
	if view.request == nil || view.request == first || first.ctx.Err() == nil || view.request.producer != replacement {
		t.Fatal("new provider did not own a new request")
	}
	lost := types.InsightProducer{}
	port.service.producer = &lost
	app.syncInlayHints()
	if view.request != nil || ed.HasInlayProjection() || ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("server loss retained labels or changed source")
	}
}
