package viewmodel

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/shellport"
	"crude/types"
)

type semanticCursorService struct {
	shellport.LSPService
	cursor         uint64
	producer       types.InsightProducer
	unsynchronized bool
}

func (s *semanticCursorService) ServerReady() bool                         { return true }
func (s *semanticCursorService) ContextCursor() (string, uint64)           { return "semantic-owner", s.cursor }
func (s *semanticCursorService) FileProducer(string) types.InsightProducer { return s.producer }
func (s *semanticCursorService) FileSynchronized(types.InsightSource, uint64) bool {
	return !s.unsynchronized
}

type semanticCursorWorkspace struct {
	shellport.Workspace
	service *semanticCursorService
}

func (w *semanticCursorWorkspace) LSPMgr() shellport.LSPService { return w.service }
func (*semanticCursorWorkspace) RequestLSPSemantic(context.Context, types.InsightSource, string, uint64, types.LSPSemanticRange) (types.LSPSemanticSet, error) {
	panic("barrier test must not send a request")
}
func (*semanticCursorWorkspace) ReleaseLSPSemantic(context.Context, string, uint64) error { return nil }

// The text and context replicas are independent. No semantic answer, including
// an empty answer, can paint before its exact context publication arrives.
func TestSemanticAnswerWaitsForExactContextAndRetiresAfterChange(t *testing.T) {
	for _, answer := range []string{"spans", "empty", "null", "unsupported"} {
		t.Run(answer, func(t *testing.T) {
			app, ws := newRealShellApp(t)
			path := filepath.Join(ws.RootPath, "barrier.go")
			if _, err := app.openEditorTab(path); err != nil {
				t.Fatal(err)
			}
			ed := app.EditorState.Editor
			text := "abc\n"
			snap := document.Snapshot{ID: "semantic-document", Path: path, Revision: 1, Text: []byte(text), Saved: []byte(text), AnalysisGeneration: "semantic-owner", AnalysisLifetime: "semantic-open", DiskExists: true, DiskHash: sha256.Sum256([]byte(text))}
			if err := ed.InstallDocument(snap); err != nil {
				t.Fatal(err)
			}
			original := app.ws
			service := &semanticCursorService{LSPService: original.LSPMgr(), cursor: 7, producer: types.InsightProducer{InstanceID: "provider"}}
			app.ws = &semanticCursorWorkspace{Workspace: original, service: service}
			t.Cleanup(func() { app.resetSemanticTokens(); app.ws = original })
			app.setFocusEditor()
			app.View()
			app.syncSemanticTokens()
			v := app.semanticViews[ed]
			if v == nil || v.request == nil {
				t.Fatal("semantic request not admitted")
			}
			r := v.request
			set := types.LSPSemanticSet{Boundary: types.InsightBoundary{Source: r.source, Context: types.InsightContext{Generation: 8}, Producer: types.InsightProducer{InstanceID: "provider"}}, WireVersion: 1, ViewID: v.id, Sequence: r.sequence, Range: r.span, Full: true}
			switch answer {
			case "spans":
				set.Types = []string{"function"}
				set.Spans = []types.LSPSemanticSpan{{Start: 0, End: 1, Type: 0}}
			case "null":
				set.Null = true
			case "unsupported":
				set.Unsupported = true
			}
			app.handleSemanticResult(semanticResultMsg{owner: r, result: set})
			app.syncSemanticTokens()
			if r.result == nil || r.installed {
				t.Fatal("future context painted prematurely", answer)
			}
			service.cursor = 8
			app.syncSemanticTokens()
			if !r.installed {
				t.Fatal("exact context did not install", answer)
			}
			service.cursor = 9
			app.syncSemanticTokens()
			if v.request == r || r.ctx.Err() == nil {
				t.Fatal("changed context retained old semantic result", answer)
			}
			if ed.Value() != text || ed.Modified() {
				t.Fatal("semantic state mutated document")
			}
		})
	}
}

// A replacement for this file must retire the old view even when the context
// cursor has not yet caught up with the producer snapshot.
func TestSemanticProducerReplacementRetiresOldRequest(t *testing.T) {
	app, ed, port := semanticBusyFixture(t)
	app.syncSemanticTokens()
	view := app.semanticViews[ed]
	if view == nil || view.request == nil {
		t.Fatal("semantic view not prepared")
	}
	old := view.request
	port.service.producer = types.InsightProducer{InstanceID: "replacement"}
	if cmd := app.startSemanticRequest(semanticDueMsg{owner: old}); cmd != nil {
		t.Fatal("retired provider request started")
	}
	app.syncSemanticTokens()
	if view.request == nil || view.request == old || old.ctx.Err() == nil || view.request.producer.InstanceID != "replacement" {
		t.Fatal("replacement producer did not own a new request")
	}
	if cmd := app.handleSemanticResult(semanticResultMsg{owner: old, result: types.LSPSemanticSet{}}); cmd != nil || old.installed {
		t.Fatal("old provider answer was accepted")
	}
	port.service.producer = types.InsightProducer{}
	app.syncSemanticTokens()
	if view.request != nil || ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("server loss retained colors or changed source")
	}
}

// Automatic color reads wait for the exact file's synchronized source and
// context instead of occupying a core job while an unrelated server starts.
func TestSemanticWaitsForFileSynchronizationBeforeRequest(t *testing.T) {
	app, ed, port := semanticBusyFixture(t)
	port.service.unsynchronized = true
	app.syncSemanticTokens()
	view := app.semanticViews[ed]
	if view == nil || view.request != nil || port.calls.Load() != 0 {
		t.Fatal("unsynchronized file admitted a semantic request")
	}
	port.service.unsynchronized = false
	app.syncSemanticTokens()
	first := view.request
	if first == nil {
		t.Fatal("file synchronization did not make colors eligible")
	}
	// A debounce can fire after the synchronization snapshot was revoked.
	// It must not start an obsolete RPC or strand the view permanently.
	port.service.unsynchronized = true
	if cmd := app.startSemanticRequest(semanticDueMsg{owner: first}); cmd != nil || view.request != nil || first.ctx.Err() == nil {
		t.Fatal("revoked file admitted a delayed semantic request")
	}
	port.service.unsynchronized = false
	app.syncSemanticTokens()
	first = view.request
	if first == nil {
		t.Fatal("file did not recover after the delayed request retired")
	}
	port.service.unsynchronized = true
	port.service.cursor++
	app.syncSemanticTokens()
	if view.request != nil || first.ctx.Err() == nil {
		t.Fatal("new context left an obsolete semantic request running")
	}
	port.service.unsynchronized = false
	app.syncSemanticTokens()
	if view.request == nil || view.request == first || view.request.contextGeneration != port.service.cursor {
		t.Fatal("colors did not recover after the file synchronized")
	}
	if ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("synchronization changed source")
	}
}
