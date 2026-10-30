package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/document"
	"crude/syntax"
	"crude/types"
)

func insightFixture(t *testing.T, name, source string) (*DocumentService, document.Snapshot, document.Owner) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewDocumentService(root, nil, nil)
	t.Cleanup(s.Close)
	owner := document.Owner{ClientID: "insight-author", Epoch: 1}
	snapshot, err := s.Open(owner, name)
	if err != nil {
		t.Fatal(err)
	}
	return s, snapshot, owner
}

func awaitInsight(t *testing.T, s *DocumentService, owner document.Owner, id string, revision uint64, state types.InsightState) types.DocumentInsight {
	t.Helper()
	wake, unsubscribe, err := s.Insight.Subscribe(owner, id)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		view, err := s.Insight.Snapshot(owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Revision == revision && view.State == state {
			return view
		}
		if view.Revision > revision || (view.Revision == revision && view.State != types.InsightPending) {
			t.Fatalf("unexpected insight: %+v; want revision %d state %s", view, revision, state)
		}
		select {
		case <-wake:
		case <-timeout.C:
			t.Fatalf("missing revision %d state %s; last=%+v", revision, state, view)
		}
	}
}

// Hold actual scheduler admission, not an elapsed-time assumption about native
// parsing. Document commands must remain usable while both native slots are busy.
func holdInsightAdmission(t *testing.T) func() {
	t.Helper()
	for range cap(insightParseSlots) {
		select {
		case insightParseSlots <- struct{}{}:
		case <-time.After(5 * time.Second):
			t.Fatal("could not reserve native execution slots")
		}
	}
	var once sync.Once
	release := func() {
		once.Do(func() {
			for range cap(insightParseSlots) {
				<-insightParseSlots
			}
		})
	}
	t.Cleanup(release)
	return release
}

func TestInsightCanonicalRevisionSharedAndDetachedSnapshots(t *testing.T) {
	s, snapshot, author := insightFixture(t, "main.go", "package main\nfunc main() {}\n")
	first := awaitInsight(t, s, author, snapshot.ID, 1, types.InsightCurrent)
	if first.Generation == "" || first.Syntax == nil || first.Syntax.HasError || len(first.Syntax.Problems) != 0 || first.Syntax.Identity.Language != syntax.Go {
		t.Fatalf("valid Go was not a completed clean native result: %+v", first)
	}
	for _, id := range []string{"peer-b", "peer-c"} {
		owner := document.Owner{ClientID: id, Epoch: 1}
		opened, err := s.Open(owner, snapshot.Path)
		if err != nil || opened.ID != snapshot.ID {
			t.Fatalf("shared open=%+v err=%v", opened, err)
		}
		got := awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightCurrent)
		if !reflect.DeepEqual(first, got) {
			t.Fatal("opening a peer recomputed or changed the canonical analysis")
		}
	}
	// Returned arrays are detached from the canonical result and other shells.
	first.Syntax.Spans[0].End = 0
	again, err := s.Insight.Snapshot(author, snapshot.ID)
	if err != nil || again.Syntax.Spans[0].End == 0 {
		t.Fatalf("snapshot aliases canonical spans: %+v %v", again, err)
	}
	receipt := documentEdit(t, s, author, snapshot.ID, "break-syntax", 1, 0, len(snapshot.Text), "package main\nfunc main( {\n")
	if !receipt.Accepted {
		t.Fatal(receipt)
	}
	broken := awaitInsight(t, s, author, snapshot.ID, receipt.Revision, types.InsightCurrent)
	if broken.Syntax == nil || !broken.Syntax.HasError || len(broken.Syntax.Problems) == 0 {
		t.Fatal("malformed syntax was presented as a zero-error result")
	}
	broken.Syntax.Problems[0].Start = ^uint32(0)
	again, err = s.Insight.Snapshot(author, snapshot.ID)
	if err != nil || again.Syntax.Problems[0].Start == ^uint32(0) {
		t.Fatal("snapshot aliases canonical problems", err)
	}
}

func TestInsightReplacementServiceHasDistinctPublicationGeneration(t *testing.T) {
	s, snapshot, owner := insightFixture(t, "main.go", "package main\n")
	first := awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightCurrent)
	s.Close()
	replacement := NewDocumentService(filepath.Dir(snapshot.Path), nil, nil)
	defer replacement.Close()
	owner.Epoch++
	reopened, err := replacement.Open(owner, snapshot.Path)
	if err != nil || reopened.ID != snapshot.ID || reopened.Revision != snapshot.Revision {
		t.Fatalf("replacement source=%+v err=%v", reopened, err)
	}
	second := awaitInsight(t, replacement, owner, reopened.ID, 1, types.InsightCurrent)
	if first.Generation == "" || second.Generation == "" || first.Generation == second.Generation {
		t.Fatalf("fresh service reused publication identity: %q -> %q", first.Generation, second.Generation)
	}
}

func TestInsightPendingEditsCloseReopenAndDetach(t *testing.T) {
	release := holdInsightAdmission(t)
	s, snapshot, owner := insightFixture(t, "main.go", "package main\n")
	for i := range 40 {
		r := documentEdit(t, s, owner, snapshot.ID, fmt.Sprintf("edit-%d", i), uint64(i+1), 0, 0, " ")
		if !r.Accepted {
			t.Fatal(r)
		}
	}
	pending := awaitInsight(t, s, owner, snapshot.ID, 41, types.InsightPending)
	if pending.Syntax != nil {
		t.Fatal("pending analysis retained obsolete clean/error evidence")
	}
	for range 20 {
		if err := s.CloseView(owner, snapshot.ID); err != nil {
			t.Fatal(err)
		}
		var err error
		snapshot, err = s.Open(owner, snapshot.Path)
		if err != nil || snapshot.Revision != 41 {
			t.Fatalf("reopen lost canonical source: %+v %v", snapshot, err)
		}
	}
	// Superseded sources and closed entry identities must not grow a work queue.
	s.Insight.mu.Lock()
	queued := len(s.Insight.queue)
	s.Insight.mu.Unlock()
	if queued > 1 {
		t.Fatalf("one live document retained %d queued analyses", queued)
	}
	reopened := awaitInsight(t, s, owner, snapshot.ID, 41, types.InsightPending)
	if reopened.Version <= pending.Version {
		t.Fatal("same-revision reopen reused a closed analysis generation")
	}
	s.Detach(owner)
	if _, err := s.Insight.Snapshot(owner, snapshot.ID); !errors.Is(err, document.ErrOwner) {
		t.Fatal("detached owner still has analysis authority", err)
	}
	s.ReleaseDetached(owner)
	owner.Epoch++
	if _, err := s.Open(owner, snapshot.Path); err != nil {
		t.Fatal(err)
	}
	release()
	current := awaitInsight(t, s, owner, snapshot.ID, 41, types.InsightCurrent)
	if current.Version <= reopened.Version || current.Syntax == nil || current.Syntax.Revision != 41 || current.Syntax.HasError {
		t.Fatalf("late queued generation replaced reopened source: %+v", current)
	}
}

func TestInsightRenameChangesLanguageWithoutSourceRevision(t *testing.T) {
	release := holdInsightAdmission(t)
	s, snapshot, owner := insightFixture(t, "source.go", "print('hello')\n")
	rename := func(name string) {
		t.Helper()
		target := filepath.Join(filepath.Dir(snapshot.Path), name)
		ops := NewFileOpsService(filepath.Dir(snapshot.Path), nil, nil)
		result := s.MutateFiles(t.Context(), FileOpRename, []string{snapshot.Path, target}, nil, func() FileOperationResultMsg {
			return ops.RenamePath(snapshot.Path, target)
		})
		if result.Err != nil {
			t.Fatal(result.Err)
		}
		var err error
		snapshot, err = s.Snapshot(snapshot.ID)
		if err != nil || snapshot.Path != target || snapshot.Revision != 1 {
			t.Fatalf("rename=%+v %v", snapshot, err)
		}
	}
	rename("source.txt")
	unavailable := awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightUnsupported)
	if unavailable.Syntax != nil || unavailable.Detail == "" || unavailable.Path != snapshot.Path {
		t.Fatal("unsupported language masquerades as clean native analysis")
	}
	rename("source.py")
	awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightPending)
	release()
	current := awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightCurrent)
	if current.Path != snapshot.Path || current.Syntax == nil || current.Syntax.Identity.Language != syntax.Python || current.Syntax.HasError {
		t.Fatalf("pre-rename Go analysis escaped the path/language fence: %+v", current)
	}
}

func TestInsightShutdownCancelsWaitingNativeWork(t *testing.T) {
	holdInsightAdmission(t)
	s, snapshot, owner := insightFixture(t, "main.go", "package main\n")
	awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightPending)
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workspace document shutdown blocked behind unrelated native execution")
	}
	if _, err := s.Insight.Snapshot(owner, snapshot.ID); !errors.Is(err, context.Canceled) {
		t.Fatal("shutdown retained an observable result", err)
	}
}

func TestInsightOversizedProjectionIsExplicitAndRecovers(t *testing.T) {
	// The source is well within the shared text limit but its alternating token
	// roles exceed the derived-view budget. No partial result may look clean.
	source := "package main\n" + strings.Repeat("var value = 123\n", 20000)
	s, snapshot, owner := insightFixture(t, "large.go", source)
	limited := awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightLimited)
	if limited.Syntax != nil || limited.Detail == "" {
		t.Fatalf("limited result lost its explicit unavailable state: %+v", limited)
	}
	release := holdInsightAdmission(t)
	receipt := documentEdit(t, s, owner, snapshot.ID, "shrink-source", 1, 0, len(source), "package main\n")
	if !receipt.Accepted {
		t.Fatal(receipt)
	}
	pending := awaitInsight(t, s, owner, snapshot.ID, receipt.Revision, types.InsightPending)
	if pending.Syntax != nil || pending.Detail != "" {
		t.Fatal("new source inherited prior revision's limitation")
	}
	peer := document.Owner{ClientID: "remaining-peer", Epoch: 1}
	if _, err := s.Open(peer, snapshot.Path); err != nil {
		t.Fatal(err)
	}
	s.Detach(owner)
	s.ReleaseDetached(owner)
	release()
	current := awaitInsight(t, s, peer, snapshot.ID, receipt.Revision, types.InsightCurrent)
	if current.Syntax == nil || current.Syntax.HasError || current.Detail != "" {
		t.Fatalf("remaining peer did not recover current analysis: %+v", current)
	}
}

func TestInsightMetadataDoesNotReparseAndLastViewWakesRawSubscriber(t *testing.T) {
	s, snapshot, owner := insightFixture(t, "main.go", "package main\n")
	first := awaitInsight(t, s, owner, snapshot.ID, 1, types.InsightCurrent)
	wake, unsubscribe, err := s.Insight.Subscribe(owner, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	// Savepoint/metadata invalidations share this real notification boundary.
	// They must not enqueue or republish unchanged source.
	for range 20 {
		s.changed(snapshot.ID)
	}
	again, err := s.Insight.Snapshot(owner, snapshot.ID)
	if err != nil || again.Version != first.Version {
		t.Fatalf("metadata notification invalidated source: %+v %v", again, err)
	}
	select {
	case <-wake:
		t.Fatal("unchanged source woke an analysis subscriber")
	default:
	}
	if err := s.CloseView(owner, snapshot.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("closed view left raw insight subscriber waiting indefinitely")
	}
	if _, err := s.Insight.Snapshot(owner, snapshot.ID); !errors.Is(err, document.ErrOwner) {
		t.Fatal("released owner retained syntax read authority", err)
	}
	s.Insight.mu.Lock()
	defer s.Insight.mu.Unlock()
	if len(s.Insight.entries) != 0 || len(s.Insight.queue) != 0 || s.Insight.bytes != 0 {
		t.Fatal("closed document retained native projection budget")
	}
}
