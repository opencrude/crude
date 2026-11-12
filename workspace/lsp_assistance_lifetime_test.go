package workspace

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"crude/document"
	"crude/types"
)

func TestCompletionReleaseBeforeAdmissionFencesEarlierSequences(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	s := f.w.LiveDocuments
	if err := s.ReleaseLSPCompletions(f.owner, "race", 3); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{1, 3} {
		if result, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "race", seq, 0, 1, types.LSPCompletionContext{}); err == nil {
			t.Fatal("released completion reached provider", result)
		}
	}
	f.client.completionMu.Lock()
	calls := len(f.client.calls)
	f.client.completionMu.Unlock()
	if calls != 0 {
		t.Fatal("released completion contacted provider", calls)
	}
	current := f.request(t, `[{"label":"x"}]`, "race", 4, 0, 1)
	if err := s.ReleaseLSPCompletions(f.owner, "race", 3); err != nil {
		t.Fatal(err)
	}
	if out := f.inspect(t, current, 0, false); out.ItemID != current.Entries[0].ID {
		t.Fatal("old release retired a newer row")
	}
	// A newer release can arrive before its own request, while an older result
	// is retained. It revokes that result and fences every earlier sequence.
	if err := s.ReleaseLSPCompletions(f.owner, "race", 6); err != nil {
		t.Fatal(err)
	}
	if _, err := f.w.InspectLSPCompletion(t.Context(), f.owner, current.ID, current.Digest, current.Entries[0].ID, false); err == nil {
		t.Fatal("newer release retained an old result")
	}
	for _, seq := range []uint64{5, 6} {
		if result, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "race", seq, 0, 1, types.LSPCompletionContext{}); err == nil {
			t.Fatal("release ordering admitted an old completion", result)
		}
	}
	if next := f.request(t, "[]", "race", 7, 0, 1); next.Sequence != 7 {
		t.Fatal("new completion did not recover", next.Sequence)
	}
}

func TestInlayReleaseBeforeAdmissionFencesEarlierSequences(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	s := f.w.LiveDocuments
	span := inlayAcceptanceRange(0, 0, 0, 3)
	if err := s.ReleaseLSPInlayHints(f.owner, "race", 3); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{1, 3} {
		if result, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "race", seq, span); err == nil {
			t.Fatal("released inlay reached provider", result)
		}
	}
	f.client.inlayMu.Lock()
	calls := len(f.client.calls)
	f.client.inlayMu.Unlock()
	if calls != 0 {
		t.Fatal("released inlay contacted provider", calls)
	}
	current := f.request(t, `[{"position":{"line":0,"character":1},"label":"hint"}]`, "race", 4, span)
	if err := s.ReleaseLSPInlayHints(f.owner, "race", 3); err != nil {
		t.Fatal(err)
	}
	if out := f.inspect(t, current, 0, false); out.Hint.Label != "hint" {
		t.Fatal("old release retired a newer label")
	}
	if err := s.ReleaseLSPInlayHints(f.owner, "race", 6); err != nil {
		t.Fatal(err)
	}
	if _, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, current.ID, current.Digest, 0, false); err == nil {
		t.Fatal("newer release retained an old label")
	}
	for _, seq := range []uint64{5, 6} {
		if result, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "race", seq, span); err == nil {
			t.Fatal("release ordering admitted an old inlay", result)
		}
	}
	if next := f.request(t, "[]", "race", 7, span); next.Sequence != 7 {
		t.Fatal("new inlay did not recover", next.Sequence)
	}
}

func TestCompletionReplacementCountsProviderWorkUntilReturn(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	entered, release := make(chan completionAcceptanceCall, completionMaxViews+1), make(chan struct{})
	f.client.completionMu.Lock()
	f.client.entered, f.client.release = entered, release
	f.client.completionMu.Unlock()
	var workers sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { unblock(); workers.Wait() }()
	done := make(chan error, completionMaxViews+1)
	for seq := uint64(1); seq <= completionMaxViews; seq++ {
		workers.Add(1)
		go func(n uint64) {
			defer workers.Done()
			_, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "one-view", n, 0, 1, types.LSPCompletionContext{})
			done <- err
		}(seq)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("provider did not receive request", seq)
		}
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "one-view", completionMaxViews+1, 0, 1, types.LSPCompletionContext{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCompletionBusy) {
			t.Fatal("excess completion did not receive a definite busy refusal", err)
		}
	case <-entered:
		t.Fatal("cancelled work freed a slot before returning")
	case <-time.After(5 * time.Second):
		t.Fatal("capacity refusal did not return")
	}
	unblock()
	workers.Wait()
	f.client.completionMu.Lock()
	f.client.entered, f.client.release = nil, nil
	f.client.completionMu.Unlock()
	if next := f.request(t, "[]", "one-view", completionMaxViews+1, 0, 1); next.Sequence != completionMaxViews+1 {
		t.Fatal("refused completion did not recover")
	}
}

func TestInlayReplacementCountsProviderWorkUntilReturn(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	span := inlayAcceptanceRange(0, 0, 0, 3)
	entered, release := make(chan inlayAcceptanceCall, inlayMaxViews+1), make(chan struct{})
	f.client.inlayMu.Lock()
	f.client.entered, f.client.release = entered, release
	f.client.inlayMu.Unlock()
	var workers sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer func() { unblock(); workers.Wait() }()
	done := make(chan error, inlayMaxViews+1)
	for seq := uint64(1); seq <= inlayMaxViews; seq++ {
		workers.Add(1)
		go func(n uint64) {
			defer workers.Done()
			_, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "one-view", n, span)
			done <- err
		}(seq)
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("provider did not receive request", seq)
		}
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		_, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "one-view", inlayMaxViews+1, span)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrInlayBusy) {
			t.Fatal("excess inlay did not receive a definite busy refusal", err)
		}
	case <-entered:
		t.Fatal("cancelled work freed a slot before returning")
	case <-time.After(5 * time.Second):
		t.Fatal("capacity refusal did not return")
	}
	unblock()
	workers.Wait()
	f.client.inlayMu.Lock()
	f.client.entered, f.client.release = nil, nil
	f.client.inlayMu.Unlock()
	if next := f.request(t, "[]", "one-view", inlayMaxViews+1, span); next.Sequence != inlayMaxViews+1 {
		t.Fatal("refused inlay did not recover")
	}
}

// Retired views are bounded, but an exhausted tombstone table is not a license
// to admit an unfenced request. Expiry makes capacity available without an edit.
func TestCompletionReleasedSequenceCapacityIsRetryable(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	s := f.w.LiveDocuments
	for i := 0; i < completionMaxSequences; i++ {
		if err := s.ReleaseLSPCompletions(f.owner, fmt.Sprintf("view-%d", i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReleaseLSPCompletions(f.owner, "new", 1); !errors.Is(err, ErrCompletionBusy) {
		t.Fatal("release did not report full sequence table", err)
	}
	if _, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "new", 1, 0, 1, types.LSPCompletionContext{}); !errors.Is(err, ErrCompletionBusy) {
		t.Fatal("untracked request was admitted", err)
	}
	s.mu.Lock()
	s.completionViews[completionViewKey{f.owner, "view-0"}].expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if err := s.ReleaseLSPCompletions(f.owner, "new", 1); err != nil {
		t.Fatal("release did not recover", err)
	}
	if _, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "new", 1, 0, 1, types.LSPCompletionContext{}); err == nil {
		t.Fatal("released sequence resurrected after capacity recovery")
	}
	if got := f.request(t, "[]", "new", 2, 0, 1); got.Sequence != 2 {
		t.Fatal("newer request did not recover")
	}
}
func TestInlayReleasedSequenceCapacityIsRetryable(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	s := f.w.LiveDocuments
	span := inlayAcceptanceRange(0, 0, 0, 3)
	for i := 0; i < inlayMaxSequences; i++ {
		if err := s.ReleaseLSPInlayHints(f.owner, fmt.Sprintf("view-%d", i), 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.ReleaseLSPInlayHints(f.owner, "new", 1); !errors.Is(err, ErrInlayBusy) {
		t.Fatal("release did not report full sequence table", err)
	}
	if _, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "new", 1, span); !errors.Is(err, ErrInlayBusy) {
		t.Fatal("untracked request was admitted", err)
	}
	s.mu.Lock()
	s.inlayViews[inlayViewKey{f.owner, "view-0"}].expires = time.Now().Add(-time.Second)
	s.mu.Unlock()
	if err := s.ReleaseLSPInlayHints(f.owner, "new", 1); err != nil {
		t.Fatal("release did not recover", err)
	}
	if _, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "new", 1, span); err == nil {
		t.Fatal("released sequence resurrected after capacity recovery")
	}
	if got := f.request(t, "[]", "new", 2, span); got.Sequence != 2 {
		t.Fatal("newer request did not recover")
	}
}

// When a peer retains the file, close/reopen can preserve the same canonical
// source identity. The old owner must still fence a delayed private request.
func TestCompletionReleasedSequenceSurvivesPeerRetainedReopen(t *testing.T) {
	f := newCompletionAcceptanceFixture(t, "abc\n")
	docs := f.w.LiveDocuments
	if _, err := docs.Open(document.Owner{ClientID: "peer", Epoch: 1}, f.path); err != nil {
		t.Fatal(err)
	}
	original := f.request(t, `[{"label":"old"}]`, "view", 1, 0, 1)
	if err := docs.ReleaseLSPCompletions(f.owner, "view", 2); err != nil {
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
	if _, err := f.w.InspectLSPCompletion(t.Context(), f.owner, original.ID, original.Digest, original.Entries[0].ID, false); err == nil {
		t.Fatal("close/reopen retained old completion evidence")
	}
	f.client.completionMu.Lock()
	before := len(f.client.calls)
	f.client.completionMu.Unlock()
	if _, err := f.w.RequestLSPCompletions(t.Context(), f.owner, f.source, "view", 2, 0, 1, types.LSPCompletionContext{}); err == nil {
		t.Fatal("released completion revived after reopen")
	}
	f.client.completionMu.Lock()
	after := len(f.client.calls)
	f.client.completionMu.Unlock()
	if after != before {
		t.Fatal("released completion reached provider after reopen")
	}
	if got := f.request(t, "[]", "view", 3, 0, 1); got.Sequence != 3 {
		t.Fatal("newer completion did not recover")
	}
}
func TestInlayReleasedSequenceSurvivesPeerRetainedReopen(t *testing.T) {
	f := newInlayAcceptanceFixture(t, "abc\n")
	docs := f.w.LiveDocuments
	span := inlayAcceptanceRange(0, 0, 0, 3)
	if _, err := docs.Open(document.Owner{ClientID: "peer", Epoch: 1}, f.path); err != nil {
		t.Fatal(err)
	}
	original := f.request(t, `[{"position":{"line":0,"character":1},"label":"old"}]`, "view", 1, span)
	if err := docs.ReleaseLSPInlayHints(f.owner, "view", 2); err != nil {
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
	if _, err := f.w.InspectLSPInlayHint(t.Context(), f.owner, original.ID, original.Digest, 0, false); err == nil {
		t.Fatal("close/reopen retained old inlay evidence")
	}
	f.client.inlayMu.Lock()
	before := len(f.client.calls)
	f.client.inlayMu.Unlock()
	if _, err := f.w.RequestLSPInlayHints(t.Context(), f.owner, f.source, "view", 2, span); err == nil {
		t.Fatal("released inlay revived after reopen")
	}
	f.client.inlayMu.Lock()
	after := len(f.client.calls)
	f.client.inlayMu.Unlock()
	if after != before {
		t.Fatal("released inlay reached provider after reopen")
	}
	if got := f.request(t, "[]", "view", 3, span); got.Sequence != 3 {
		t.Fatal("newer inlay did not recover")
	}
}
