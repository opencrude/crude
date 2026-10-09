package clientstate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}

func attach(t *testing.T, s *Store, client string) Attachment {
	t.Helper()
	a, err := s.Acquire(context.Background(), client, AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func fullDraft(text, ref string) Draft {
	return Draft{Text: text, RefIDs: []string{ref, "unresolved"}, Cursor: 4,
		SelectionFrom: 1, SelectionTo: 3, Scroll: 2, Revision: 17,
		Spans: []RefSpan{{ID: ref, Start: 0, End: 3}}}
}

func putFor(a Attachment, request string, revision uint64, draft Draft) Put {
	return Put{ClientID: a.ClientID, Epoch: a.Epoch, RequestID: request,
		Key:              Key{ClientID: a.ClientID, WorkspaceID: "workspace", SessionID: "session"},
		ExpectedRevision: revision, Draft: draft}
}

func requireError(t *testing.T, err, expected error) {
	t.Helper()
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v; want %v", err, expected)
	}
}

func TestCompleteSnapshotsAreClientPrivateAndDetached(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "clients.db"))
	a, b := attach(t, s, "a"), attach(t, s, "b")
	for _, p := range []Put{putFor(a, "same-request", 0, fullDraft("α [one]", "one")), putFor(b, "same-request", 0, fullDraft("β [two]", "two"))} {
		want := p.Draft
		want.RefIDs = append([]string(nil), want.RefIDs...)
		want.Spans = append([]RefSpan(nil), want.Spans...)
		record, err := s.Save(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		if record.Revision != 1 || !reflect.DeepEqual(record.Draft, want) {
			t.Fatalf("incomplete record: %+v", record)
		}
		record.Draft.RefIDs[0] = "mutated-return"
		p.Draft.Spans[0].ID = "mutated-input"
		loaded, err := s.Load(p.ClientID, p.Epoch, p.Key)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(loaded.Draft, want) {
			t.Fatalf("snapshot changed: %+v", loaded)
		}
	}
	key := putFor(b, "", 0, Draft{}).Key
	_, err := s.Load(a.ClientID, a.Epoch, key)
	requireError(t, err, ErrInvalid)
	key = putFor(a, "", 0, Draft{}).Key
	key.SessionID = "other"
	_, err = s.Load(a.ClientID, a.Epoch, key)
	requireError(t, err, ErrNotFound)
}

func TestReceiptFailureRollsBackDraftPublication(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "clients.db"))
	a := attach(t, s, "client")
	p := putFor(a, "first", 0, Draft{Text: "original"})
	original, err := s.Save(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TRIGGER reject_receipt BEFORE INSERT ON draft_receipts
BEGIN SELECT RAISE(ABORT, 'injected receipt failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	p = putFor(a, "failed", original.Revision, Draft{Text: "must rollback"})
	if _, err = s.Save(context.Background(), p); err == nil {
		t.Fatal("injected persistence failure was ignored")
	}
	loaded, err := s.Load(a.ClientID, a.Epoch, p.Key)
	if err != nil || !reflect.DeepEqual(loaded, original) {
		t.Fatalf("non-atomic draft publication: %+v %v", loaded, err)
	}
	_, err = s.LookupReceipt(context.Background(), a.ClientID, a.Epoch, p.RequestID)
	requireError(t, err, ErrNotFound)
}

func TestAttachmentTakeoverFencesEveryOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.db")
	s, other := openTestStore(t, path), openTestStore(t, path)
	a := attach(t, s, "client")
	p := putFor(a, "saved", 0, Draft{Text: "kept"})
	if _, err := s.Save(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	_, err := other.Acquire(context.Background(), a.ClientID, AcquireOptions{})
	requireError(t, err, ErrAttached)
	_, err = other.Acquire(context.Background(), a.ClientID, AcquireOptions{Takeover: true, ExpectedEpoch: a.Epoch + 1})
	requireError(t, err, ErrStaleEpoch)
	b, err := other.Acquire(context.Background(), a.ClientID, AcquireOptions{Takeover: true, ExpectedEpoch: a.Epoch})
	if err != nil || b.Epoch != a.Epoch+1 {
		t.Fatalf("takeover: %+v %v", b, err)
	}
	_, err = s.Load(a.ClientID, a.Epoch, p.Key)
	requireError(t, err, ErrStaleEpoch)
	_, err = s.Save(context.Background(), p) // Even exact historical replay needs current auth.
	requireError(t, err, ErrStaleEpoch)
	_, err = s.LookupReceipt(context.Background(), a.ClientID, a.Epoch, p.RequestID)
	requireError(t, err, ErrStaleEpoch)
	requireError(t, s.Release(context.Background(), a.ClientID, a.Epoch), ErrStaleEpoch)
	if _, err = other.Load(b.ClientID, b.Epoch, p.Key); err != nil {
		t.Fatal(err)
	}
	if err = other.Release(context.Background(), b.ClientID, b.Epoch); err != nil {
		t.Fatal(err)
	}
	_, err = other.Load(b.ClientID, b.Epoch, p.Key)
	requireError(t, err, ErrStaleEpoch)
	c := attach(t, s, a.ClientID)
	if c.Epoch != b.Epoch+1 {
		t.Fatalf("epoch reused: %+v", c)
	}
}

func TestDurableReceiptsReconnectWithoutReplayingWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	a := attach(t, s, "client")
	p := putFor(a, "first", 0, fullDraft("[ref] original", "ref"))
	first, err := s.Save(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	secondPut := putFor(a, "second", first.Revision, Draft{Text: "newer", Cursor: 5, Revision: 99})
	second, err := s.Save(context.Background(), secondPut)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openTestStore(t, path)
	_, err = s.Acquire(context.Background(), a.ClientID, AcquireOptions{})
	requireError(t, err, ErrAttached)
	b, err := s.Acquire(context.Background(), a.ClientID, AcquireOptions{Takeover: true, ExpectedEpoch: a.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := s.LookupReceipt(context.Background(), b.ClientID, b.Epoch, "first")
	if err != nil || !reflect.DeepEqual(receipt, first) {
		t.Fatalf("receipt: %+v %v", receipt, err)
	}
	p.Epoch = b.Epoch
	replay, err := s.Save(context.Background(), p)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	mismatch := p
	mismatch.Draft.Text += "!"
	_, err = s.Save(context.Background(), mismatch)
	requireError(t, err, ErrRequestMismatch)
	mismatch = p
	mismatch.ExpectedRevision++
	_, err = s.Save(context.Background(), mismatch)
	requireError(t, err, ErrRequestMismatch)
	mismatch = p
	mismatch.Key.SessionID = "another"
	_, err = s.Save(context.Background(), mismatch)
	requireError(t, err, ErrRequestMismatch)
	stale := putFor(b, "stale-cas", first.Revision, Draft{Text: "must not overwrite"})
	_, err = s.Save(context.Background(), stale)
	requireError(t, err, ErrConflict)
	_, err = s.LookupReceipt(context.Background(), b.ClientID, b.Epoch, stale.RequestID)
	requireError(t, err, ErrNotFound)
	loaded, err := s.Load(b.ClientID, b.Epoch, p.Key)
	if err != nil || !reflect.DeepEqual(loaded, second) {
		t.Fatalf("historical replay/stale CAS lost newer state: %+v %v", loaded, err)
	}
}

func TestConcurrentWritersAcrossStoresOnlyOneWinsCAS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.db")
	stores := []*Store{openTestStore(t, path), openTestStore(t, path)}
	a := attach(t, stores[0], "client")
	const writers = 12
	type outcome struct {
		record Record
		err    error
	}
	out := make(chan outcome, writers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := stores[i%2].Save(context.Background(), putFor(a, fmt.Sprintf("request-%d", i), 0, Draft{Text: fmt.Sprintf("writer-%d", i)}))
			out <- outcome{r, err}
		}()
	}
	close(start)
	wg.Wait()
	close(out)
	wins := 0
	var winner Record
	for o := range out {
		if o.err == nil {
			wins++
			winner = o.record
		} else {
			requireError(t, o.err, ErrConflict)
		}
	}
	if wins != 1 {
		t.Fatalf("CAS winners = %d", wins)
	}
	loaded, err := stores[1].Load(a.ClientID, a.Epoch, winner.Key)
	if err != nil || !reflect.DeepEqual(loaded, winner) {
		t.Fatalf("winner lost: %+v %v", loaded, err)
	}
}

func TestConcurrentIdenticalRequestsCommitOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clients.db")
	s, other := openTestStore(t, path), openTestStore(t, path)
	a := attach(t, s, "client")
	p := putFor(a, "same", 0, Draft{Text: "only once"})
	results := make(chan Record, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Store{s, other} {
		wg.Add(1)
		go func() { defer wg.Done(); r, err := store.Save(context.Background(), p); results <- r; errs <- err }()
	}
	wg.Wait()
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if r := <-results; r.Revision != 1 {
			t.Fatalf("duplicate commit: %+v", r)
		}
	}
}

func TestValidationAndCanceledSaveDoNotCreateReceipts(t *testing.T) {
	s := openTestStore(t, filepath.Join(t.TempDir(), "clients.db"))
	for _, id := range []string{"", " space", "line\nbreak", strings.Repeat("a", MaxIDBytes+1), string([]byte{0xff})} {
		_, err := s.Acquire(context.Background(), id, AcquireOptions{})
		requireError(t, err, ErrInvalid)
	}
	a := attach(t, s, "client")
	for i, draft := range []Draft{
		{Text: strings.Repeat("x", MaxDraftBytes+1)},
		{Text: strings.Repeat("\x00", MaxDraftBytes/2)}, // JSON escaping exceeds the encoded bound.
		{Text: "x", Cursor: 2}, {Text: "x", SelectionFrom: -1}, {Scroll: -1},
		{Text: "x", Spans: []RefSpan{{ID: "ref", Start: 0, End: 2}}},
		{RefIDs: []string{""}}, {Text: string([]byte{0xff})},
	} {
		_, err := s.Save(context.Background(), putFor(a, fmt.Sprintf("invalid-%d", i), 0, draft))
		requireError(t, err, ErrInvalid)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := putFor(a, "canceled", 0, Draft{Text: "not saved"})
	_, err := s.Save(ctx, p)
	requireError(t, err, context.Canceled)
	_, err = s.LookupReceipt(context.Background(), a.ClientID, a.Epoch, p.RequestID)
	requireError(t, err, ErrNotFound)
	_, err = s.Load(a.ClientID, a.Epoch, p.Key)
	requireError(t, err, ErrNotFound)
}
