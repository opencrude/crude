package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"crude/document"
	"crude/types"
)

type semanticTestCall struct{ mode, previous string }
type semanticTestClient struct {
	lifecycleClient
	lock                     sync.Mutex
	support                  types.LSPSemanticSupport
	unsupported              bool
	full, delta, rangeResult string
	calls                    []semanticTestCall
	entered                  chan struct{}
	release                  <-chan struct{}
}

func (c *semanticTestClient) RequestSemanticTokens(ctx context.Context, path, mode, previous string, _ types.LSPSemanticRange) (types.LSPSemanticResponse, error) {
	c.mu.Lock()
	wire := c.versions[path]
	c.mu.Unlock()
	c.lock.Lock()
	c.calls = append(c.calls, semanticTestCall{mode, previous})
	out := types.LSPSemanticResponse{WireVersion: wire, Support: c.support, Unsupported: c.unsupported, Boundary: types.InsightBoundary{Source: types.InsightSource{Path: "forged"}}}
	switch mode {
	case "full":
		out.JSON = []byte(c.full)
	case "full/delta":
		out.JSON = []byte(c.delta)
	case "range":
		out.JSON = []byte(c.rangeResult)
	}
	entered, release := c.entered, c.release
	c.lock.Unlock()
	if mode != "support" && entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if mode != "support" && release != nil {
		<-release
	} // deliberately ignore cancellation
	return out, nil
}

type semanticTestRuntime struct{ c *semanticTestClient }

func (r semanticTestRuntime) NewClient(_ types.LSPServerSpec, _ string, admission func() error) LSPClient {
	r.c.startAdmission = admission
	return r.c
}

type semanticFixture struct {
	w      *Workspace
	c      *semanticTestClient
	owner  document.Owner
	source types.InsightSource
	path   string
}

func newSemanticFixture(t *testing.T, text string) *semanticFixture {
	t.Helper()
	root := t.TempDir()
	c := &semanticTestClient{lifecycleClient: lifecycleClient{runtime: &lifecycleRuntime{}, reapableClient: reapableClient{done: make(chan struct{})}}, support: types.LSPSemanticSupport{Types: []string{"variable", "parameter"}, Modifiers: []string{"declaration"}, Full: true, Delta: true, Range: true}, full: `{"resultId":"first","data":[0,0,1,0,0]}`, delta: `{"resultId":"second","edits":[]}`, rangeResult: `{"data":[0,0,1,0,0]}`}
	m := NewLSPManager(nil, semanticTestRuntime{c})
	trustAnalysis(t, m, root)
	t.Cleanup(m.Close)
	docs := NewDocumentService(root, nil, m)
	t.Cleanup(docs.Close)
	path := filepath.Join(root, "sample.go")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	owner := document.Owner{ClientID: "semantic-client", Epoch: 1}
	snap, err := docs.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, m, &c.lifecycleClient, snap)
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[snap.ID])
	docs.mu.Unlock()
	return &semanticFixture{&Workspace{LiveDocuments: docs, LSPMgr: m}, c, owner, source, path}
}
func (f *semanticFixture) request(t *testing.T, seq uint64) (types.LSPSemanticSet, error) {
	t.Helper()
	return f.w.RequestLSPSemantic(t.Context(), f.owner, f.source, "view", seq, types.LSPSemanticRange{Start: types.LSPSemanticPosition{Line: 0}, End: types.LSPSemanticPosition{Line: 0, Character: 1}})
}
func TestSemanticCanonicalDeltaFallbackAndRelease(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	before, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.request(t, 1)
	if err != nil || first.Boundary.Source != f.source || !first.Full || len(first.Spans) != 1 || first.Spans[0] != (types.LSPSemanticSpan{Start: 0, End: 1, Type: 0}) {
		t.Fatal(first, err)
	}
	f.c.lock.Lock()
	f.c.delta = `{"edits":[{"start":100,"deleteCount":1}]}`
	f.c.full = `{"resultId":"fresh","data":[0,1,1,1,1]}`
	f.c.lock.Unlock()
	second, err := f.request(t, 2)
	if err != nil || len(second.Spans) != 1 || second.Spans[0] != (types.LSPSemanticSpan{Start: 1, End: 2, Type: 1, Modifiers: 1}) {
		t.Fatal(second, err)
	}
	f.c.lock.Lock()
	calls := append([]semanticTestCall(nil), f.c.calls...)
	f.c.lock.Unlock()
	if !reflect.DeepEqual(calls, []semanticTestCall{{"support", ""}, {"full", ""}, {"support", ""}, {"full/delta", "first"}, {"full", ""}}) {
		t.Fatal("wrong provider sequence", calls)
	}
	if _, err := f.request(t, 1); err == nil {
		t.Fatal("older view sequence accepted")
	}
	if err := f.w.LiveDocuments.ReleaseLSPSemantic(f.owner, "view", 2); err != nil {
		t.Fatal(err)
	}
	f.w.LiveDocuments.mu.Lock()
	retired := f.w.LiveDocuments.semanticViews[inlayViewKey{f.owner, "view"}]
	f.w.LiveDocuments.mu.Unlock()
	if retired == nil || retired.base != nil || retired.loading {
		t.Fatal("released view retained evidence")
	}
	if _, err := f.request(t, 2); err == nil {
		t.Fatal("released sequence was re-admitted")
	}

	after, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("semantic observation changed source/history", err)
	}
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != "abc\n" {
		t.Fatal("semantic observation wrote disk", err)
	}
}
func TestSemanticRangeOnlyAndUnsupportedStayReadOnly(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	f.c.lock.Lock()
	f.c.support.Full = false
	f.c.support.Delta = false
	f.c.lock.Unlock()
	out, err := f.request(t, 1)
	if err != nil || out.Full || out.Unsupported || len(out.Spans) != 1 {
		t.Fatal(out, err)
	}
	f.c.lock.Lock()
	f.c.unsupported = true
	f.c.lock.Unlock()
	out, err = f.request(t, 2)
	if err != nil || !out.Unsupported || len(out.Spans) != 0 {
		t.Fatal(out, err)
	}
}
func TestSemanticObsoleteAnswerCannotPublish(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	entered, release := make(chan struct{}, 1), make(chan struct{})
	f.c.lock.Lock()
	f.c.entered, f.c.release = entered, release
	f.c.lock.Unlock()
	done := make(chan error, 1)
	go func() { _, err := f.request(t, 1); done <- err }()
	<-entered
	if err := f.w.LiveDocuments.ReleaseLSPSemantic(f.owner, "view", 1); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err == nil || !errors.Is(err, context.Canceled) {
		t.Fatal("released old answer published", err)
	}
}

// Tokens from a range-only provider must touch the requested range. In
// particular the next source character is not evidence for this viewport.
func TestSemanticRangeRejectsOutsideRequest(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	f.c.lock.Lock()
	f.c.support.Full = false
	f.c.support.Delta = false
	f.c.rangeResult = `{"data":[0,1,1,0,0]}`
	f.c.lock.Unlock()
	if result, err := f.request(t, 1); err == nil {
		t.Fatal("out-of-range token became presentation", result)
	}
}

// A provider can ignore cancellation. Replacing one view repeatedly must not
// create unbounded concurrent source copies or RPCs; slots retire only when
// the original work really returns.
func TestSemanticReplacementKeepsRunningRequestsBounded(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	entered, release := make(chan struct{}, 32), make(chan struct{})
	f.c.lock.Lock()
	f.c.entered, f.c.release = entered, release
	f.c.lock.Unlock()
	var workers sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { unblock(); workers.Wait() }()
	done := make(chan error, 32)
	launch := func(seq uint64) {
		workers.Add(1)
		go func() { defer workers.Done(); _, err := f.request(t, seq); done <- err }()
	}
	for seq := uint64(1); seq <= 16; seq++ {
		launch(seq)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("provider did not receive request", seq)
		}
	}
	launch(17)
	select {
	case err := <-done:
		if !errors.Is(err, ErrSemanticBusy) {
			t.Fatal("excess request must report known non-admission", err)
		}
	case <-entered:
		t.Fatal("cancelled work did not retain its admission slot")
	case <-time.After(5 * time.Second):
		t.Fatal("request cap did not return")
	}
	// A refused sequence was not retained; it can be admitted once the
	// previously cancelled provider calls actually return.
	unblock()
	workers.Wait()
	f.c.lock.Lock()
	f.c.entered, f.c.release = nil, nil
	f.c.lock.Unlock()
	if result, err := f.request(t, 17); err != nil || result.Sequence != 17 {
		t.Fatal("capacity did not recover for the refused sequence", result, err)
	}
}

func TestSemanticDeltaUsesPreviousResultAfterCanonicalEdit(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	first, err := f.request(t, 1)
	if err != nil || len(first.Spans) != 1 {
		t.Fatal(first, err)
	}
	documentEdit(t, f.w.LiveDocuments, f.owner, f.source.DocumentID, "semantic-edit", f.source.Revision, 3, 3, "X")
	snapshot, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, f.w.LSPMgr, &f.c.lifecycleClient, snapshot)
	f.w.LiveDocuments.mu.Lock()
	f.source = f.w.LiveDocuments.analysisIdentityLocked(f.w.LiveDocuments.docs[snapshot.ID])
	f.w.LiveDocuments.mu.Unlock()
	second, err := f.request(t, 2)
	if err != nil || second.Boundary.Source != f.source || len(second.Spans) != 1 {
		t.Fatal(second, err)
	}
	f.c.lock.Lock()
	calls := append([]semanticTestCall(nil), f.c.calls...)
	f.c.lock.Unlock()
	if !reflect.DeepEqual(calls, []semanticTestCall{{"support", ""}, {"full", ""}, {"support", ""}, {"full/delta", "first"}}) {
		t.Fatalf("delta missing after source edit, contexts %d -> %d: %+v", first.Boundary.Context.Generation, second.Boundary.Context.Generation, calls)
	}
}

// Release and request travel on independent read jobs. If release wins their
// race, the later request must not resurrect the already retired view.
func TestSemanticReleaseBeforeRequestCannotResurrectView(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	if err := f.w.LiveDocuments.ReleaseLSPSemantic(f.owner, "view", 1); err != nil {
		t.Fatal(err)
	}
	if result, err := f.request(t, 1); err == nil {
		t.Fatalf("released request resurrected a view: %+v", result)
	}
	f.c.lock.Lock()
	calls := len(f.c.calls)
	f.c.lock.Unlock()
	if calls != 0 {
		t.Fatal("retired view contacted provider", calls)
	}
	if result, err := f.request(t, 2); err != nil || result.Sequence != 2 {
		t.Fatal("a newer live view was blocked", result, err)
	}
	if err := f.w.LiveDocuments.ReleaseLSPSemantic(f.owner, "view", 4); err != nil {
		t.Fatal(err)
	}
	if result, err := f.request(t, 3); err == nil {
		t.Fatal("older request overtook future release", result)
	}
	if err := f.w.LiveDocuments.ReleaseLSPSemantic(f.owner, "view", 1); err != nil {
		t.Fatal(err)
	}
	if result, err := f.request(t, 4); err == nil {
		t.Fatal("old release regressed tombstone", result)
	}
	if result, err := f.request(t, 5); err != nil || result.Sequence != 5 {
		t.Fatal("newer request blocked", result, err)
	}
}

func TestSemanticPeerEditRejectsLateProviderAnswer(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	peer := document.Owner{ClientID: "semantic-peer", Epoch: 1}
	if _, err := f.w.LiveDocuments.Open(peer, f.path); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	f.c.lock.Lock()
	f.c.entered, f.c.release = entered, release
	f.c.lock.Unlock()
	done := make(chan error, 1)
	go func() { _, err := f.request(t, 1); done <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not reached")
	}
	documentEdit(t, f.w.LiveDocuments, peer, f.source.DocumentID, "peer-change", f.source.Revision, 3, 3, "X")
	current, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	waitDiagnosticSend(t, f.w.LSPMgr, &f.c.lifecycleClient, current)
	unblock()
	if err := <-done; err == nil {
		t.Fatal("old semantic result survived a shared edit")
	}
	after, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil || string(after.Text) != "abcX\n" || after.Revision != current.Revision {
		t.Fatal("late semantic result changed shared source", after, err)
	}
}

// A stream of closed views can fill the bounded sequence tombstones. A new
// editor must see a definite non-admission, then work when those records expire;
// otherwise the unchanged editor never gets another chance to show colors.
func TestSemanticReleasedViewCapacityRecovers(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	for i := 0; i < inlayMaxSequences; i++ {
		if err := f.w.LiveDocuments.ReleaseLSPSemantic(f.owner, fmt.Sprintf("retired-%d", i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := f.request(t, 1); !errors.Is(err, ErrSemanticBusy) {
		t.Fatalf("full sequence capacity must be retryable, got result=%+v err=%v", result, err)
	}
	f.c.lock.Lock()
	calls := len(f.c.calls)
	f.c.lock.Unlock()
	if calls != 0 {
		t.Fatal("refused view reached provider", calls)
	}
	docs := f.w.LiveDocuments
	docs.mu.Lock()
	for _, v := range docs.semanticViews {
		v.expires = time.Now().Add(-time.Second)
	}
	docs.mu.Unlock()
	if result, err := f.request(t, 1); err != nil || len(result.Spans) != 1 {
		t.Fatal("view did not recover after records expired", result, err)
	}
}

// A file's synchronization is published after its producer acknowledges the
// exact source/context, independently of a concurrent shell's selected file.
func TestSemanticFileSynchronizationPublishesReadiness(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	m := f.w.LSPMgr
	_, old := m.ContextCursor()
	if !m.FileSynchronized(f.source, old) {
		t.Fatal("connected source did not expose synchronization")
	}
	changed := f.source
	changed.Lifetime = "different-lifetime"
	if m.FileSynchronized(changed, old) || m.FileSynchronized(f.source, old+1) {
		t.Fatal("another source/context appeared synchronized")
	}
	m.documentSend.Lock()
	locked := true
	defer func() {
		if locked {
			m.documentSend.Unlock()
		}
	}()
	m.mu.Lock()
	m.advanceContextLocked("test project context")
	current := m.contextGeneration
	m.signalLocked()
	m.mu.Unlock()
	version, notice := m.Changed()
	if m.FileSynchronized(f.source, current) {
		t.Fatal("unsent context appeared synchronized")
	}
	m.documentSend.Unlock()
	locked = false
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !m.FileSynchronized(f.source, current) {
		select {
		case <-notice:
			_, notice = m.Changed()
		case <-deadline.C:
			t.Fatal("source synchronization did not complete")
		}
	}
	if m.Version() <= version {
		t.Fatal("source synchronization did not publish a new snapshot version")
	}
}

// Closing one shell's file cannot revive its old color request when another
// shell keeps the canonical source and server alive during the reopen.
func TestSemanticSequenceSurvivesPeerRetainedReopen(t *testing.T) {
	f := newSemanticFixture(t, "abc\n")
	docs := f.w.LiveDocuments
	if _, err := docs.Open(document.Owner{ClientID: "peer", Epoch: 1}, f.path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.request(t, 1); err != nil {
		t.Fatal(err)
	}
	if err := docs.CloseView(f.owner, f.source.DocumentID); err != nil {
		t.Fatal(err)
	}
	if _, err := docs.Open(f.owner, f.path); err != nil {
		t.Fatal(err)
	}
	docs.mu.Lock()
	source := docs.analysisIdentityLocked(docs.docs[f.source.DocumentID])
	docs.mu.Unlock()
	if source != f.source {
		t.Fatal("peer did not retain the same canonical source", source, f.source)
	}
	f.c.lock.Lock()
	before := len(f.c.calls)
	f.c.lock.Unlock()
	if _, err := f.request(t, 1); err == nil {
		t.Fatal("old semantic request revived after reopen")
	}
	f.c.lock.Lock()
	after := len(f.c.calls)
	f.c.lock.Unlock()
	if after != before {
		t.Fatal("old semantic request reached provider after reopen")
	}
	if next, err := f.request(t, 2); err != nil || next.Sequence != 2 {
		t.Fatal("newer semantic request did not recover", err)
	}
	f.c.lock.Lock()
	calls := append([]semanticTestCall(nil), f.c.calls[before:]...)
	f.c.lock.Unlock()
	if len(calls) != 2 || calls[0].mode != "support" || calls[1].mode != "full" {
		t.Fatal("reopened view reused the closed view's delta base", calls)
	}
}
