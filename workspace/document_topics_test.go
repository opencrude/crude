package workspace

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/document"
)

func TestDocumentTopicOrderedDeltasSavepointAndGap(t *testing.T) {
	s, snapshot, a, b := documentFixture(t, "abc")
	wake, unsubscribe, err := s.SubscribeDocument(b, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	initial, saved, err := s.TopicDocument(b, snapshot.ID, 0, nil)
	if err != nil || !initial.Reset || !saved || string(initial.Snapshot.Text) != "abc" {
		t.Fatalf("initial=%+v %v", initial, err)
	}
	documentEdit(t, s, a, snapshot.ID, "first", 1, 0, 0, "X")
	documentEdit(t, s, a, snapshot.ID, "second", 2, 4, 4, "Y")
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("direct subscriber not notified")
	}
	delta, saved, err := s.TopicDocument(b, snapshot.ID, 1, initial.Snapshot.Saved)
	if err != nil || delta.Reset || saved || len(delta.Snapshot.Text) != 0 || len(delta.Snapshot.Saved) != 0 || len(delta.Changes) != 2 {
		t.Fatalf("delta=%+v saved=%v err=%v", delta, saved, err)
	}
	text := initial.Snapshot.Text
	for _, change := range delta.Changes {
		text, _, err = document.Apply(text, change.Edits)
		if err != nil {
			t.Fatal(err)
		}
	}
	if string(text) != "XabcY" || delta.Snapshot.Revision != 3 || !delta.Snapshot.Dirty {
		t.Fatalf("wrong bytes/metadata: %q %+v", text, delta)
	}
	if _, err := s.Save(t.Context(), a, snapshot.ID, 3, DocumentSaveManual); err != nil {
		t.Fatal(err)
	}
	metadata, saved, err := s.TopicDocument(b, snapshot.ID, 3, initial.Snapshot.Saved)
	if err != nil || !saved || len(metadata.Changes) != 0 || len(metadata.Snapshot.Text) != 0 || string(metadata.Snapshot.Saved) != "XabcY" || metadata.Snapshot.Dirty {
		t.Fatalf("save=%+v saved=%v %v", metadata, saved, err)
	}
	metadata.Snapshot.Saved[0] = '!'
	actual, _ := s.Snapshot(snapshot.ID)
	if !bytes.Equal(actual.Saved, []byte("XabcY")) {
		t.Fatal("savepoint aliases authority")
	}
	s.mu.Lock()
	s.docs[snapshot.ID].journal = nil
	s.mu.Unlock()
	gap, _, err := s.TopicDocument(b, snapshot.ID, 1, nil)
	if err != nil || !gap.Reset || string(gap.Snapshot.Text) != "XabcY" {
		t.Fatalf("gap=%+v %v", gap, err)
	}
	s.Detach(b)
	if _, _, err := s.TopicDocument(b, snapshot.ID, 3, nil); err != document.ErrOwner {
		t.Fatalf("detached read=%v", err)
	}
}

func TestDocumentTopicStaleOwnerAndUnsubscribeFences(t *testing.T) {
	s, snapshot, a, b := documentFixture(t, "abc")
	old, unsubscribeOld, err := s.SubscribeDocument(b, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribeOld()
	replacement := document.Owner{ClientID: b.ClientID, Epoch: b.Epoch + 1}
	if _, err := s.Open(replacement, snapshot.Path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SubscribeDocument(b, snapshot.ID); err != document.ErrOwner {
		t.Fatalf("stale owner subscribed: %v", err)
	}
	if _, _, err := s.TopicDocument(b, snapshot.ID, snapshot.Revision, snapshot.Saved); err != document.ErrOwner {
		t.Fatalf("stale owner read topic: %v", err)
	}
	wake, unsubscribe, err := s.SubscribeDocument(replacement, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	unsubscribeOld()
	unsubscribeOld()
	documentEdit(t, s, a, snapshot.ID, "replacement", snapshot.Revision, 0, 0, "X")
	select {
	case <-old:
		t.Fatal("removed subscription was notified")
	default:
	}
	select {
	case <-wake:
	case <-time.After(time.Second):
		t.Fatal("old unsubscribe removed replacement topic")
	}
	update, _, err := s.TopicDocument(replacement, snapshot.ID, snapshot.Revision, snapshot.Saved)
	if err != nil || update.Reset || update.Snapshot.Revision != snapshot.Revision+1 || len(update.Changes) != 1 {
		t.Fatalf("replacement missed accepted revision: %+v %v", update, err)
	}
	s.Close()
	// Close and repeated exact-subscription release are safe in either order.
	unsubscribe()
	unsubscribe()
	s.notifyDocument(snapshot.ID)
	if _, _, err := s.SubscribeDocument(replacement, snapshot.ID); err != context.Canceled {
		t.Fatalf("closed service subscribed: %v", err)
	}
	if _, _, err := s.TopicDocument(replacement, snapshot.ID, update.Snapshot.Revision, nil); err != context.Canceled {
		t.Fatalf("closed service read topic: %v", err)
	}
}

func TestDocumentTopicConcurrentAdmissionReadAndRelease(t *testing.T) {
	s, snapshot, a, b := documentFixture(t, "abc")
	const count = 40
	var workers sync.WaitGroup
	errors := make(chan error, 3)
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := range count {
			if _, err := s.Apply(document.Change{Owner: a, DocumentID: snapshot.ID, RequestID: fmt.Sprintf("concurrent-%d", i), BaseRevision: uint64(i + 1), Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}}}); err != nil {
				errors <- err
				return
			}
		}
	}()
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			var revision uint64
			for range count {
				_, unsubscribe, err := s.SubscribeDocument(b, snapshot.ID)
				if err != nil {
					errors <- err
					return
				}
				update, _, err := s.TopicDocument(b, snapshot.ID, revision, snapshot.Saved)
				unsubscribe()
				unsubscribe()
				if err != nil {
					errors <- err
					return
				}
				if update.Snapshot.Revision < revision {
					errors <- fmt.Errorf("topic revision regressed: %d < %d", update.Snapshot.Revision, revision)
					return
				}
				revision = update.Snapshot.Revision
			}
		}()
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	final, _, err := s.TopicDocument(b, snapshot.ID, 0, nil)
	if err != nil || final.Snapshot.Revision != count+1 || string(final.Snapshot.Text) != strings.Repeat("X", count)+"abc" {
		t.Fatalf("accepted concurrent bytes/revisions lost: %+v %v", final, err)
	}
}

func TestDocumentTopicExecutionApplyAndUndo(t *testing.T) {
	s, _, root := executionFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	owner := document.Owner{ClientID: "human", Epoch: 1}
	snapshot, err := s.documents.Open(owner, "live.txt")
	if err != nil {
		t.Fatal(err)
	}
	dirty := documentEdit(t, s.documents, owner, snapshot.ID, "dirty-topic", snapshot.Revision, 0, 3, "ONE")
	execution := executionCreate(t, s)
	executionPut(t, execution.Root, "live.txt", "ONE\nTWO\nthree\n")
	plan := executionReview(t, s, execution.ID)
	wake, unsubscribe, err := s.documents.SubscribeDocument(owner, snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	receipt, err := s.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	read := func(revision uint64, before, want string) uint64 {
		t.Helper()
		select {
		case <-wake:
		case <-ctx.Done():
			t.Fatal("execution did not notify topic", ctx.Err())
		}
		update, _, err := s.documents.TopicDocument(owner, snapshot.ID, revision, snapshot.Saved)
		if err != nil || update.Reset || update.Snapshot.Revision != revision+1 || len(update.Changes) != 1 {
			t.Fatalf("execution topic lost revision: %+v %v", update, err)
		}
		text, _, err := document.Apply([]byte(before), update.Changes[0].Edits)
		if err != nil || string(text) != want || !update.Snapshot.Dirty {
			t.Fatalf("execution recipient bytes=%q want=%q dirty=%v err=%v", text, want, update.Snapshot.Dirty, err)
		}
		return update.Snapshot.Revision
	}
	revision := read(dirty.Revision, "ONE\ntwo\nthree\n", "ONE\nTWO\nthree\n")
	if _, err := s.Undo(ctx, receipt.OperationID); err != nil {
		t.Fatal(err)
	}
	read(revision, "ONE\nTWO\nthree\n", "ONE\ntwo\nthree\n")
	diskText(t, root, "live.txt", "one\ntwo\nthree\n")
}
