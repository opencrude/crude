package viewmodel

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/types"
)

type occurrenceCursorService struct {
	shellport.LSPService
	cursor         uint64
	producer       types.InsightProducer
	settings       types.LSPSettings
	unsynchronized bool
	settingsReads  int
}

func (s *occurrenceCursorService) ContextCursor() (string, uint64) {
	return "occurrence-owner", s.cursor
}
func (s *occurrenceCursorService) FileProducer(string) types.InsightProducer { return s.producer }
func (s *occurrenceCursorService) FileSynchronized(types.InsightSource, uint64) bool {
	return !s.unsynchronized
}
func (s *occurrenceCursorService) Settings() types.LSPSettings {
	s.settingsReads++
	return types.CloneLSPSettings(s.settings)
}
func (s *occurrenceCursorService) ReferencesEnabled() bool {
	return s.settings.Enabled && s.settings.References
}

type occurrenceCursorWorkspace struct {
	shellport.Workspace
	service *occurrenceCursorService
}

func (w *occurrenceCursorWorkspace) LSPMgr() shellport.LSPService { return w.service }
func (*occurrenceCursorWorkspace) RequestLSPHighlights(context.Context, types.InsightSource, int, int) (types.LSPHighlights, error) {
	panic("test controls the independently delivered response")
}

func occurrenceCursorFixture(t *testing.T) (*App, *editor.Editor, *occurrenceCursorService) {
	t.Helper()
	app, ws := newRealShellApp(t)
	path := filepath.Join(ws.RootPath, "occurrences.go")
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	ed := app.EditorState.Editor
	text := "abc abc\n"
	snap := document.Snapshot{ID: "occurrence-document", Path: path, Revision: 1, Text: []byte(text), Saved: []byte(text), AnalysisGeneration: "occurrence-owner", AnalysisLifetime: "occurrence-open", DiskExists: true, DiskHash: sha256.Sum256([]byte(text))}
	if err := ed.InstallDocument(snap); err != nil {
		t.Fatal(err)
	}
	original := app.ws
	settings := types.DefaultLSPSettings()
	settings.Enabled, settings.References = true, true
	service := &occurrenceCursorService{LSPService: original.LSPMgr(), cursor: 7, producer: types.InsightProducer{InstanceID: "provider"}, settings: settings}
	app.ws = &occurrenceCursorWorkspace{Workspace: original, service: service}
	t.Cleanup(func() { app.resetOccurrences(); app.ws = original })
	app.setFocusEditor()
	ed.SetCursorPosition(0, 1)
	app.View()
	return app, ed, service
}

func occurrenceCursorResult(r *occurrenceRequest, generation uint64) types.LSPHighlights {
	span := func(start, end int) types.LSPNavigationRange {
		return types.LSPNavigationRange{Start: types.LSPNavigationPosition{Line: 0, Character: start}, End: types.LSPNavigationPosition{Line: 0, Character: end}}
	}
	return types.LSPHighlights{Boundary: types.InsightBoundary{Source: r.source, Producer: r.producer, Context: types.InsightContext{Generation: generation}}, WireVersion: 1, Line: r.line, Col: r.col, Answer: types.LSPHighlightsFound,
		Highlights: []types.LSPHighlight{{Range: span(0, 3), Kind: types.LSPHighlightRead}, {Range: span(4, 7), Kind: types.LSPHighlightRead}}}
}

func TestOccurrencesWaitForContextAndRetireOnChange(t *testing.T) {
	for _, answer := range []string{types.LSPHighlightsFound, types.LSPHighlightsNull, types.LSPHighlightsEmpty, types.LSPHighlightsUnsupported} {
		t.Run(answer, func(t *testing.T) {
			app, ed, service := occurrenceCursorFixture(t)
			app.syncOccurrences()
			r := app.occurrence
			if r == nil {
				t.Fatal("resting caret did not schedule a request")
			}
			result := occurrenceCursorResult(r, 8)
			result.Answer = answer
			if answer != types.LSPHighlightsFound {
				result.Highlights = nil
			}
			app.handleOccurrenceResult(occurrenceResultMsg{owner: r, result: result})
			app.syncOccurrences()
			if r.result == nil || r.installed || ed.OccurrencesCoverCaret() {
				t.Fatal("a future context was presented before the shell observed it")
			}
			service.cursor = 8
			app.syncOccurrences()
			if !r.installed || ed.OccurrencesCoverCaret() != (answer == types.LSPHighlightsFound) {
				t.Fatal("the matching context did not present the right answer")
			}
			service.cursor = 9
			app.syncOccurrences()
			if app.occurrence == r || ed.OccurrencesCoverCaret() {
				t.Fatal("a newer context retained the previous answer")
			}
			if ed.Value() != "abc abc\n" || ed.Modified() || ed.HasSelection() {
				t.Fatal("occurrences modified the document")
			}
		})
	}
}

func TestOccurrencesRetireWhenProviderOrPreferenceChanges(t *testing.T) {
	app, ed, service := occurrenceCursorFixture(t)
	app.syncOccurrences()
	r := app.occurrence
	if r == nil {
		t.Fatal("no initial request")
	}
	app.handleOccurrenceResult(occurrenceResultMsg{owner: r, result: occurrenceCursorResult(r, 7)})
	app.syncOccurrences()
	if !ed.OccurrencesCoverCaret() {
		t.Fatal("initial occurrences did not paint")
	}
	ed.SetCursorPosition(0, 2)
	app.syncOccurrences()
	if !ed.OccurrencesCoverCaret() {
		t.Fatal("moving inside the symbol removed the marks")
	}
	service.producer = types.InsightProducer{InstanceID: "replacement"}
	app.syncOccurrences()
	next := app.occurrence
	if ed.OccurrencesCoverCaret() || next == nil || next == r || next.producer != service.producer {
		t.Fatal("provider replacement retained old marks or did not schedule fresh ones")
	}
	app.handleOccurrenceResult(occurrenceResultMsg{owner: r, result: occurrenceCursorResult(r, 7)})
	if ed.OccurrencesCoverCaret() {
		t.Fatal("an old provider revived its marks")
	}
	app.handleOccurrenceResult(occurrenceResultMsg{owner: next, result: occurrenceCursorResult(next, 7)})
	app.syncOccurrences()
	if !ed.OccurrencesCoverCaret() {
		t.Fatal("replacement provider's marks were not shown")
	}
	service.settings.References = false
	app.syncOccurrences()
	if ed.OccurrencesCoverCaret() || app.occurrence != nil {
		t.Fatal("turning off references left marks visible")
	}
}

func TestOccurrencesWaitForSynchronizedSource(t *testing.T) {
	app, _, service := occurrenceCursorFixture(t)
	service.unsynchronized = true
	app.syncOccurrences()
	if app.occurrence != nil {
		t.Fatal("unsynchronized source scheduled a provider request")
	}
	service.unsynchronized = false
	app.syncOccurrences()
	r := app.occurrence
	if r == nil {
		t.Fatal("synchronized source did not schedule a request")
	}
	service.unsynchronized = true
	if cmd := app.startOccurrenceRequest(occurrenceDueMsg{owner: r}); cmd != nil || app.occurrence != nil {
		t.Fatal("a delayed request survived a synchronization change")
	}
}

// Closing the shell must cancel even the debounced request that has not yet
// acquired a client-I/O slot. It must not retain an editor or later install.
func TestShutdownRetiresPendingOccurrences(t *testing.T) {
	app, _, _ := occurrenceCursorFixture(t)
	app.syncOccurrences()
	r := app.occurrence
	if r == nil {
		t.Fatal("no pending occurrence request")
	}
	app.StopAsyncIO()
	if r.ctx.Err() == nil || app.occurrence != nil {
		t.Fatal("shutdown left the occurrence request alive")
	}
}

// A resting caret is reconciled on every input and tick. It must not clone the
// full language catalog each time just to check the references preference.
func TestOccurrencesSteadyStateDoesNotCopySettings(t *testing.T) {
	app, _, service := occurrenceCursorFixture(t)
	if len(service.settings.Languages) == 0 {
		t.Fatal("fixture needs configured languages")
	}
	app.syncOccurrences()
	if app.occurrence == nil {
		t.Fatal("no occurrence request")
	}
	request, reads := app.occurrence, service.settingsReads
	for range 100 {
		if cmd := app.syncOccurrences(); cmd != nil || app.occurrence != request {
			t.Fatal("a stable caret scheduled another occurrence request")
		}
	}
	// Count the actual clone owner. Process-wide allocation counters also
	// include unrelated real-shell jobs and made this check fail in a group.
	if service.settingsReads != reads {
		t.Fatalf("stable occurrences copied settings %d times", service.settingsReads-reads)
	}
}
