package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/document"
	"crude/types"
)

// editorSource exercises the canonical owner, including its send-intent path.
// It is not a raw manager API adapter and never invents source identities.
func editorSource(t *testing.T, m *LSPManager, path, text string) document.Snapshot {
	t.Helper()
	if m.sources == nil {
		probeSources(t, m, filepath.Dir(path))
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		createFile(t, path, text)
	}
	owner := document.Owner{ClientID: "fixture-editor", Epoch: 1}
	snapshot, err := m.sources.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot.Text) != text {
		documentEdit(t, m.sources, owner, snapshot.ID, "replace-"+text, snapshot.Revision, 0, len(snapshot.Text), text)
		snapshot, err = m.sources.Snapshot(snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func closeEditor(t *testing.T, m *LSPManager, path string) {
	t.Helper()
	if err := m.sources.CloseView(document.Owner{ClientID: "fixture-editor", Epoch: 1}, path); err != nil {
		t.Fatal(err)
	}
}

func waitDiagnosticSend(t *testing.T, m *LSPManager, c *lifecycleClient, snapshot document.Snapshot) {
	t.Helper()
	awaitLSP(t, func() bool {
		c.mu.Lock()
		b := c.boundaries[snapshot.Path]
		c.mu.Unlock()
		m.mu.Lock()
		doc := m.documents[snapshot.Path]
		ready := doc != nil && doc.opened && !doc.syncPending && doc.syncedRevision == doc.revision &&
			doc.syncedContext == m.contextGeneration && sameInsightBoundary(doc.boundary, b)
		m.mu.Unlock()
		return ready && b.Source.Revision == snapshot.Revision
	})
}

func diagnosticFixture(t *testing.T) (*LSPManager, *lifecycleClient, document.Snapshot) {
	t.Helper()
	m, r, root := lifecycleManager(t)
	snapshot := editorSource(t, m, filepath.Join(root, "main.go"), "package main\r\nvar text = \"😀\"\r\n")
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	c := r.snapshot()[0]
	waitDiagnosticSend(t, m, c, snapshot)
	return m, c, snapshot
}

func requireDiagnostic(t *testing.T, m *LSPManager, path, message string) types.InsightDiagnostics {
	t.Helper()
	batch, ok := m.FileDiagnostics(path)
	if !ok || batch.ID == "" || !batch.HasExactSource() || len(batch.Diagnostics) != 1 || batch.Diagnostics[0].Message != message {
		t.Fatalf("diagnostic fact = %+v, present=%v; want exact %q", batch, ok, message)
	}
	return batch
}

func TestInsightDiagnosticsShareCanonicalFactAcrossConsumers(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	unsaved := "package main\r\nvar text = \"😀未保存\"\r\n"
	current := editorSource(t, m, initial.Path, unsaved)
	second, err := m.sources.Open(document.Owner{ClientID: "second-shell", Epoch: 3}, initial.Path)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != current.ID || second.Revision != current.Revision || string(second.Text) != unsaved {
		t.Fatalf("second shell did not borrow canonical source: %+v", second)
	}
	// Disk still contains the initial, deliberately different source.
	waitDiagnosticSend(t, m, c, current)
	p := c.publication(current.Path, []LSPDiagnostic{{Line: 1, Col: 12, EndLine: 1, EndCol: 14, Message: "unsaved emoji finding"}})
	c.emit(p)
	editor := requireDiagnostic(t, m, current.Path, "unsaved emoji finding")
	want := []LSPDiagnostic{{Line: 1, Col: 12, EndLine: 1, EndCol: 14, Severity: types.DiagError, Message: "unsaved emoji finding"}}
	if !reflect.DeepEqual(editor.Diagnostics, want) || editor.State != types.InsightProvisional || len(editor.Boundary.Context.UnknownInputs) == 0 {
		t.Fatalf("normalization or missing-context qualification = %+v", editor)
	}
	source := editor.Boundary.Source
	if source.DocumentID != current.ID || source.Revision != current.Revision || source.WorkspaceGeneration != current.AnalysisGeneration ||
		source.Lifetime != current.AnalysisLifetime || source.Kind != "canonical" || source.Path != current.Path {
		t.Fatalf("publication invented a source owner: %+v canonical=%+v", source, current)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	diags, quiet, view, err := m.ProbeDiagnostics(ctx, current.Path)
	if err != nil || !quiet || !view.BufferDiffers || view.SourceText != unsaved || view.Source != source ||
		diags.ID != editor.ID || !reflect.DeepEqual(diags.Diagnostics, want) {
		t.Fatalf("agent did not consume the editor's exact fact: diags=%+v quiet=%v view=%+v err=%v", diags, quiet, view, err)
	}
	global := m.AllDiagnostics()[current.Path]
	if global.ID != editor.ID || !reflect.DeepEqual(global.Diagnostics, want) {
		t.Fatal("global projection created a second answer")
	}
	if errors, warnings, hints := m.ProjectDiagnosticCounts(); errors != 1 || warnings != 0 || hints != 0 {
		t.Fatalf("omitted severity not counted once: %d %d %d", errors, warnings, hints)
	}
}

func TestInsightDiagnosticsEditAndEqualTextUndoFenceCallbacks(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	old := c.publication(initial.Path, []LSPDiagnostic{{Severity: 1, Message: "old source"}})
	c.emit(old)
	before := requireDiagnostic(t, m, initial.Path, "old source")
	owner := document.Owner{ClientID: "fixture-editor", Epoch: 1}
	changed := documentEdit(t, m.sources, owner, initial.ID, "temporary", initial.Revision, 0, 7, "changed")
	delayed := old
	delayed.Sequence += 50
	delayed.Diagnostics = nil
	c.emit(delayed)
	if batch, _ := m.FileDiagnostics(initial.Path); batch.ID != before.ID || batch.State != types.InsightStale || batch.HasExactSource() {
		t.Fatalf("canonical edit allowed a delayed old-source clear: %+v", batch)
	}
	if _, err := m.sources.Undo(owner, initial.ID, "equal-text-undo", changed.Revision); err != nil {
		t.Fatal(err)
	}
	current, err := m.sources.Snapshot(initial.ID)
	if err != nil || string(current.Text) != "package main\r\nvar text = \"😀\"\r\n" || current.Revision <= initial.Revision {
		t.Fatalf("undo did not restore bytes under a newer revision: %+v %v", current, err)
	}
	old.Sequence += 100 // A newer callback sequence cannot revive an old source.
	old.Diagnostics = nil
	c.emit(old)
	stale, _ := m.FileDiagnostics(initial.Path)
	if stale.ID != before.ID || stale.State != types.InsightStale || stale.HasExactSource() || stale.Diagnostics[0].Message != "old source" {
		t.Fatalf("equal-text undo revived old clean publication: %+v", stale)
	}
	waitDiagnosticSend(t, m, c, current)
	next := c.publication(initial.Path, []LSPDiagnostic{{Severity: 2, Message: "new source"}})
	if next.WireVersion == before.WireVersion || next.Boundary.Source.Revision != current.Revision {
		t.Fatal("canonical revision did not receive a new wire association")
	}
	c.emit(next)
	after := requireDiagnostic(t, m, initial.Path, "new source")
	if after.ID == before.ID {
		t.Fatal("new publication reused old fact identity")
	}
}

func TestInsightDiagnosticsCloseReopenAndSameProviderReplacement(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	old := c.publication(initial.Path, []LSPDiagnostic{{Severity: 1, Message: "before close"}})
	c.emit(old)
	before := requireDiagnostic(t, m, initial.Path, "before close")
	closeEditor(t, m, initial.Path)
	awaitLSP(t, func() bool { return slices.Contains(c.snapshot(), "close:main.go") })
	editorSource(t, m, initial.Path, string(initial.Text))
	waitDiagnosticSend(t, m, c, initial)
	reopened := c.publication(initial.Path, []LSPDiagnostic{{Severity: 2, Message: "after reopen"}})
	if reopened.WireVersion <= old.WireVersion {
		t.Fatal("reopen reused an old wire version")
	}
	c.emit(old)
	if batch, _ := m.FileDiagnostics(initial.Path); batch.HasExactSource() {
		t.Fatal("closed transport version became current")
	}
	c.emit(reopened)
	requireDiagnostic(t, m, initial.Path, "after reopen")
	r := &lifecycleRuntime{}
	m.SetRuntime(r)
	awaitLSP(t, func() bool { return len(r.snapshot()) == 1 })
	next := r.snapshot()[0]
	waitDiagnosticSend(t, m, next, initial)
	replacement := next.publication(initial.Path, []LSPDiagnostic{{Severity: 1, Message: "replacement"}})
	if replacement.Boundary.Producer.Provider != before.Boundary.Producer.Provider ||
		replacement.Boundary.Producer.InstanceID == before.Boundary.Producer.InstanceID {
		t.Fatal("same-provider replacement did not receive a fresh instance")
	}
	c.emit(reopened)
	if batch, _ := m.FileDiagnostics(initial.Path); batch.HasExactSource() {
		t.Fatal("old instance became current in replacement")
	}
	next.emit(replacement)
	requireDiagnostic(t, m, initial.Path, "replacement")
}

func TestInsightDiagnosticsContextChangesRetainStaleEvidence(t *testing.T) {
	for _, change := range []string{"settings", "watched-input", "other-source"} {
		t.Run(change, func(t *testing.T) {
			m, c, initial := diagnosticFixture(t)
			p := c.publication(initial.Path, []LSPDiagnostic{{Severity: 1, Message: "context finding"}})
			c.emit(p)
			before := requireDiagnostic(t, m, initial.Path, "context finding")
			switch change {
			case "settings":
				settings := m.Settings()
				settings.ServerThreads++
				m.SetSettings(settings)
			case "watched-input":
				m.NotifyWatchedFilesChanged(t.Context(), filepath.Join(filepath.Dir(initial.Path), "go.mod"), 2)
			case "other-source":
				editorSource(t, m, filepath.Join(filepath.Dir(initial.Path), "other.go"), "package main\n")
			}
			p.Sequence += 100
			p.Diagnostics = nil
			c.emit(p)
			after, ok := m.FileDiagnostics(initial.Path)
			if !ok || after.ID != before.ID || after.State != types.InsightStale || after.HasExactSource() ||
				len(after.Diagnostics) != 1 || after.Diagnostics[0].Message != "context finding" {
				t.Fatalf("context change manufactured clean/current output: %+v", after)
			}
		})
	}
}

func TestInsightDiagnosticsDistinguishEmptyMissingUnversionedAndLimited(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	pending, ok := m.FileDiagnostics(initial.Path)
	if !ok || pending.ID != "" || pending.State != types.InsightPending || pending.HasExactSource() {
		t.Fatalf("no publication presented as an analyzed empty batch: %+v %v", pending, ok)
	}
	c.emit(c.publication(initial.Path, nil))
	empty, _ := m.FileDiagnostics(initial.Path)
	if empty.ID == "" || !empty.HasExactSource() || len(empty.Diagnostics) != 0 || empty.State != types.InsightProvisional {
		t.Fatalf("versioned empty missing explicit observed/context qualification: %+v", empty)
	}
	unversioned := c.publication(initial.Path, nil)
	unversioned.ReportedVersion = nil
	c.emit(unversioned)
	uncertain, _ := m.FileDiagnostics(initial.Path)
	if uncertain.ID == empty.ID || uncertain.ID == "" || uncertain.Binding != "unversioned" || uncertain.HasExactSource() {
		t.Fatalf("unversioned empty inherited exact clean status: %+v", uncertain)
	}
	limited := c.publication(initial.Path, nil)
	limited.Limited, limited.Detail = true, "producer omitted results"
	c.emit(limited)
	bounded, _ := m.FileDiagnostics(initial.Path)
	if bounded.ID == "" || bounded.State != types.InsightLimited || bounded.HasExactSource() || !bounded.Limited {
		t.Fatalf("limited empty became a clean answer: %+v", bounded)
	}
	if view := m.ProjectInsights(); view.DiagnosticsLimited != 1 || !strings.Contains(view.CoverageDetail, "unknown") {
		t.Fatalf("limited/missing context was hidden: %+v", view)
	}
}

func TestInsightDiagnosticsRejectMalformedAndMismatchedPublications(t *testing.T) {
	for _, bad := range []string{"source", "wire", "reported-version", "negative-range", "reversed-range", "severity", "invalid-output", "out-of-source-line", "out-of-source-column", "split-surrogate"} {
		t.Run(bad, func(t *testing.T) {
			m, c, initial := diagnosticFixture(t)
			c.emit(c.publication(initial.Path, []LSPDiagnostic{{Severity: 1, Message: "retained warning"}}))
			before := requireDiagnostic(t, m, initial.Path, "retained warning")
			p := c.publication(initial.Path, nil)
			switch bad {
			case "source":
				p.Boundary.Source.Revision++
			case "wire":
				p.WireVersion++
			case "reported-version":
				v := p.WireVersion + 1
				p.ReportedVersion = &v
			case "negative-range":
				p.Diagnostics = []LSPDiagnostic{{Line: -1, Severity: 1}}
			case "reversed-range":
				p.Diagnostics = []LSPDiagnostic{{Line: 2, EndLine: 1, Severity: 1}}
			case "severity":
				p.Diagnostics = []LSPDiagnostic{{Severity: 5}}
			case "invalid-output":
				p.Detail = string([]byte{0xff})
			case "out-of-source-line":
				p.Diagnostics = []LSPDiagnostic{{Line: 99, EndLine: 99, Severity: 1, Message: "invalid line"}}
			case "out-of-source-column":
				p.Diagnostics = []LSPDiagnostic{{Line: 1, Col: 1000, EndLine: 1, EndCol: 1001, Severity: 1, Message: "invalid column"}}
			case "split-surrogate":
				p.Diagnostics = []LSPDiagnostic{{Line: 1, Col: 12, EndLine: 1, EndCol: 13, Severity: 1, Message: "half of emoji"}}
			}
			c.emit(p)
			after, _ := m.FileDiagnostics(initial.Path)
			if after.ID != before.ID || len(after.Diagnostics) != 1 || after.Diagnostics[0].Message != "retained warning" {
				t.Fatalf("rejected input replaced evidence with a clean answer: %+v", after)
			}
			if bad == "out-of-source-line" || bad == "out-of-source-column" || bad == "split-surrogate" {
				if after.State != types.InsightStale || after.HasExactSource() || after.Boundary.Source != before.Boundary.Source {
					t.Fatalf("malformed source range retained an exact current answer: %+v", after)
				}
			}
		})
	}
}

type earlyPublicationClient struct {
	lifecycleClient
	entered chan struct{}
	release chan struct{}
}

func (c *earlyPublicationClient) OpenFile(ctx context.Context, path, language, text string, b types.InsightBoundary) (int, error) {
	v, err := c.lifecycleClient.OpenFile(ctx, path, language, text, b)
	c.emit(c.publication(path, []LSPDiagnostic{{Line: 0, Col: 0, EndLine: 0, EndCol: 7, Severity: 1, Message: "callback before receipt"}}))
	close(c.entered)
	select {
	case <-c.release:
		return v, err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

type earlyPublicationRuntime struct{ client *earlyPublicationClient }

func (r earlyPublicationRuntime) NewClient(types.LSPServerSpec, string) LSPClient { return r.client }

func TestInsightDiagnosticsStageCallbackBeforeSendReceiptExactlyOnce(t *testing.T) {
	c := &earlyPublicationClient{lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}},
		entered: make(chan struct{}), release: make(chan struct{})}
	root := t.TempDir()
	m := NewLSPManager(nil, earlyPublicationRuntime{c})
	t.Cleanup(m.Close)
	m.SetWorkspaceRoot(root)
	initial := editorSource(t, m, filepath.Join(root, "main.go"), "package main\n")
	select {
	case <-c.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("early callback never reached manager")
	}
	m.mu.Lock()
	doc := m.documents[initial.Path]
	staged := doc.syncPending && doc.pendingPublication != nil
	m.mu.Unlock()
	pending, _ := m.FileDiagnostics(initial.Path)
	if !staged || pending.ID != "" {
		close(c.release)
		t.Fatalf("callback was lost or published before actual send receipt: staged=%v batch=%+v", staged, pending)
	}
	close(c.release)
	awaitLSP(t, func() bool { b, _ := m.FileDiagnostics(initial.Path); return b.ID != "" })
	first := requireDiagnostic(t, m, initial.Path, "callback before receipt")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	answer, quiet, view, err := m.ProbeDiagnostics(ctx, initial.Path)
	if err != nil || !quiet || answer.ID != first.ID || answer.Sequence != first.Sequence {
		t.Fatalf("callback/await created competing facts: view=%+v quiet=%v err=%v", view, quiet, err)
	}
}

func TestNotifyFileSaveFlushesLatestChangeBeforeSaveBoundary(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	editorSource(t, m, initial.Path, "old")
	editorSource(t, m, initial.Path, "latest")
	m.NotifyFileSave(t.Context(), initial.Path, "saved", DocumentSaveAfterDelay, true)
	m.NotifyFileSave(t.Context(), initial.Path, "saved", DocumentSaveAfterDelay, false)
	calls := c.snapshot()
	want := []string{"change:main.go:latest", "willSave:saved", "didSave:saved"}
	if len(calls) < 4 || !slices.Equal(calls[len(calls)-3:], want) {
		t.Fatalf("save overtook canonical send intent: calls=%v want suffix=%v", calls, want)
	}
}

func TestInsightDiagnosticsReplaceAndAggregateOnlyCurrentObservedFacts(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	other := editorSource(t, m, filepath.Join(filepath.Dir(initial.Path), "other.go"), "package main\n")
	waitDiagnosticSend(t, m, c, initial)
	waitDiagnosticSend(t, m, c, other)
	c.emit(c.publication(initial.Path, []LSPDiagnostic{
		{Severity: types.DiagError, Message: "first error"},
		{Severity: types.DiagError, Message: "second error"},
		{Severity: types.DiagWarning, Message: "first warning"},
	}))
	c.emit(c.publication(other.Path, []LSPDiagnostic{
		{Severity: types.DiagWarning, Message: "second warning"},
		{Severity: types.DiagInfo, Message: "information"},
		{Severity: types.DiagHint, Message: "hint"},
	}))
	if errors, warnings, hints := m.ProjectDiagnosticCounts(); errors != 2 || warnings != 2 || hints != 2 {
		t.Fatalf("observed totals=%d %d %d", errors, warnings, hints)
	}
	c.emit(c.publication(initial.Path, nil))
	if empty, _ := m.FileDiagnostics(initial.Path); empty.ID == "" || len(empty.Diagnostics) != 0 || !empty.HasExactSource() {
		t.Fatalf("replacement empty was silently removed: %+v", empty)
	}
	if errors, warnings, hints := m.ProjectDiagnosticCounts(); errors != 0 || warnings != 1 || hints != 2 {
		t.Fatalf("empty publication did not replace observed totals=%d %d %d", errors, warnings, hints)
	}
	m.InvalidateFile(other.Path)
	if errors, warnings, hints := m.ProjectDiagnosticCounts(); errors != 0 || warnings != 0 || hints != 0 {
		t.Fatalf("stale facts still count as current errors=%d warnings=%d hints=%d", errors, warnings, hints)
	}
}

func TestInsightDiagnosticsSeverityPolicyDoesNotRewriteSharedFact(t *testing.T) {
	m, c, initial := diagnosticFixture(t)
	c.emit(c.publication(initial.Path, []LSPDiagnostic{{Severity: types.DiagWarning, Message: "retained below filter"}}))
	before := requireDiagnostic(t, m, initial.Path, "retained below filter")
	settings := m.Settings()
	settings.MinSeverity = "error"
	settings.DiagInEditor, settings.DiagInTree = false, false
	m.SetSettings(settings)
	batch := requireDiagnostic(t, m, initial.Path, "retained below filter")
	if batch.Diagnostics[0].Severity != types.DiagWarning || batch.ID != before.ID ||
		batch.State != before.State || batch.Boundary.Producer != before.Boundary.Producer || len(c.runtime.snapshot()) != 1 {
		t.Fatal("view policy restarted a producer or changed its authoritative fact")
	}
	if errors, warnings, hints := m.ProjectDiagnosticCounts(); errors != 0 || warnings != 0 || hints != 0 {
		t.Fatalf("policy did not filter projection: %d %d %d", errors, warnings, hints)
	}
}
