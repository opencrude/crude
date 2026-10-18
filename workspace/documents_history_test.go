package workspace

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"crude/document"
)

// Each expectation is an explicit user-visible state, not computed by the
// implementation's edit/rebase helpers.
type documentHistoryJourney struct {
	t        *testing.T
	s        *DocumentService
	id       string
	revision uint64
	request  int
}

func newDocumentHistoryJourney(t *testing.T, text string) (*documentHistoryJourney, document.Owner, document.Owner) {
	t.Helper()
	s, snap, a, b := documentFixture(t, text)
	return &documentHistoryJourney{t: t, s: s, id: snap.ID, revision: snap.Revision}, a, b
}

func (j *documentHistoryJourney) next() string {
	j.request++
	return fmt.Sprintf("history-%d", j.request)
}

func (j *documentHistoryJourney) check(r document.Receipt, err error, want string) {
	j.t.Helper()
	if err != nil || !r.Accepted || r.Before != j.revision || r.Revision <= j.revision {
		j.t.Fatalf("command %d: receipt=%+v err=%v", j.request, r, err)
	}
	j.revision = r.Revision
	snap := documentText(j.t, j.s, j.id, want)
	if snap.Revision != j.revision || snap.Dirty != (want != string(snap.Saved)) {
		j.t.Fatalf("command %d: inconsistent snapshot %+v", j.request, snap)
	}
}

func (j *documentHistoryJourney) edit(owner document.Owner, group string, start, end int, text, want string) {
	j.t.Helper()
	r, err := j.s.Apply(document.Change{Owner: owner, DocumentID: j.id, RequestID: j.next(), BaseRevision: j.revision, GroupID: group, Edits: []document.Edit{{Start: start, End: end, Text: text}}})
	j.check(r, err, want)
}

func (j *documentHistoryJourney) history(owner document.Owner, redo bool, want string) {
	j.t.Helper()
	var r document.Receipt
	var err error
	if redo {
		r, err = j.s.Redo(owner, j.id, j.next(), j.revision)
	} else {
		r, err = j.s.Undo(owner, j.id, j.next(), j.revision)
	}
	j.check(r, err, want)
}

func (j *documentHistoryJourney) rejected(owner document.Owner, redo bool, expected error, want string) {
	j.t.Helper()
	before := stageDocument(j.s.docs[j.id])
	var r document.Receipt
	var err error
	if redo {
		r, err = j.s.Redo(owner, j.id, j.next(), j.revision)
	} else {
		r, err = j.s.Undo(owner, j.id, j.next(), j.revision)
	}
	if err == nil || (expected != nil && !errors.Is(err, expected)) || r.Accepted || r.Revision != j.revision {
		j.t.Fatalf("rejection receipt=%+v err=%v want=%v", r, err, expected)
	}
	after := j.s.docs[j.id]
	if !reflect.DeepEqual(before.snapshot, after.snapshot) || !reflect.DeepEqual(before.journal, after.journal) || !reflect.DeepEqual(before.undo, cloneHistory(after.undo)) || !reflect.DeepEqual(before.redo, cloneHistory(after.redo)) || before.historyBytes != after.historyBytes {
		j.t.Fatal("rejected command changed canonical text or history")
	}
	documentText(j.t, j.s, j.id, want)
}

func TestDocumentHistoryUndoAllRedoAllMixedCommands(t *testing.T) {
	j, a, _ := newDocumentHistoryJourney(t, "alpha beta gamma")
	j.edit(a, "", 5, 5, "!", "alpha! beta gamma")
	j.edit(a, "", 7, 12, "", "alpha! gamma")
	j.edit(a, "replace", 7, 12, "G", "alpha! G")
	j.edit(a, "replace", 7, 8, "OMEGA", "alpha! OMEGA")
	for cycle := 0; cycle < 3; cycle++ {
		j.history(a, false, "alpha! gamma")
		j.history(a, false, "alpha! beta gamma")
		j.history(a, false, "alpha beta gamma")
		j.rejected(a, false, nil, "alpha beta gamma")
		j.history(a, true, "alpha! beta gamma")
		j.history(a, true, "alpha! gamma")
		j.history(a, true, "alpha! OMEGA")
		j.rejected(a, true, nil, "alpha! OMEGA")
	}
}

func TestDocumentHistoryGroupedDisjointPeerAndCancellationChains(t *testing.T) {
	j, a, b := newDocumentHistoryJourney(t, "aaa bbb ccc")
	j.edit(a, "command", 0, 3, "AAAA", "AAAA bbb ccc")
	j.edit(a, "command", 0, 4, "AAAAA", "AAAAA bbb ccc")
	j.edit(b, "", 10, 13, "CCCC", "AAAAA bbb CCCC")
	for cycle := 0; cycle < 3; cycle++ {
		j.history(a, false, "aaa bbb CCCC")
		j.history(a, true, "AAAAA bbb CCCC")
	}
	j.history(b, false, "AAAAA bbb ccc")
	j.history(a, false, "aaa bbb ccc")
	j.history(b, true, "aaa bbb CCCC")
	j.history(a, true, "AAAAA bbb CCCC")
}

func TestDocumentHistoryBranchAfterUndo(t *testing.T) {
	j, a, _ := newDocumentHistoryJourney(t, "zero")
	j.edit(a, "", 0, 4, "one", "one")
	j.edit(a, "", 0, 3, "two", "two")
	j.history(a, false, "one")
	j.edit(a, "", 0, 3, "branch", "branch")
	j.rejected(a, true, nil, "branch")
	j.history(a, false, "one")
	j.history(a, false, "zero")
	j.history(a, true, "one")
	j.history(a, true, "branch")
}

func TestDocumentHistoryGroupedOverlapIsAtomic(t *testing.T) {
	j, a, b := newDocumentHistoryJourney(t, "aaa bbb ccc")
	j.edit(a, "command", 0, 3, "AAAA", "AAAA bbb ccc")
	j.edit(a, "command", 9, 12, "CCCC", "AAAA bbb CCCC")
	// The last group member can be reversed, but its first member conflicts.
	j.edit(b, "", 1, 2, "x", "AxAA bbb CCCC")
	j.rejected(a, false, document.ErrOverlap, "AxAA bbb CCCC")
	j.history(b, false, "AAAA bbb CCCC")
	j.history(a, false, "aaa bbb ccc")
}

func TestDocumentHistoryGroupSurvivesInterleavedPeerCommands(t *testing.T) {
	j, a, b := newDocumentHistoryJourney(t, "aaa bbb ccc")
	j.edit(a, "command", 0, 3, "AAAA", "AAAA bbb ccc")
	j.edit(b, "", 9, 12, "CCCC", "AAAA bbb CCCC")
	j.edit(a, "command", 0, 4, "AAAAA", "AAAAA bbb CCCC")
	j.history(a, false, "aaa bbb CCCC")
	j.history(a, true, "AAAAA bbb CCCC")
	j.history(b, false, "AAAAA bbb ccc")
	j.history(a, false, "aaa bbb ccc")
}

func TestDocumentHistoryMultiEditUnicodeAcrossPeerShift(t *testing.T) {
	j, a, b := newDocumentHistoryJourney(t, "é aa 🙂 bb end")
	r, err := j.s.Apply(document.Change{Owner: a, DocumentID: j.id, RequestID: j.next(), BaseRevision: j.revision, Edits: []document.Edit{{Start: 3, End: 5, Text: "AAAA"}, {Start: 11, End: 13, Text: "B"}}})
	j.check(r, err, "é AAAA 🙂 B end")
	j.edit(b, "", 0, 2, "éé", "éé AAAA 🙂 B end")
	j.edit(a, "", 5, 9, "a", "éé a 🙂 B end")
	for cycle := 0; cycle < 2; cycle++ {
		j.history(a, false, "éé AAAA 🙂 B end")
		j.history(a, false, "éé aa 🙂 bb end")
		j.history(a, true, "éé AAAA 🙂 B end")
		j.history(a, true, "éé a 🙂 B end")
	}
}

func TestDocumentHistoryDurableCancellationProvenance(t *testing.T) {
	s, db, snap, a := durableDeltaFixture(t)
	b := document.Owner{ClientID: "peer", Epoch: 1}
	if _, err := s.Open(b, snap.Path); err != nil {
		t.Fatal(err)
	}
	j := &documentHistoryJourney{t: t, s: s, id: snap.ID, revision: snap.Revision}
	restart := func() {
		t.Helper()
		j.s.Close()
		j.s = NewDocumentService(s.root, nil, nil, db.DB)
		t.Cleanup(j.s.Close)
		for _, owner := range []document.Owner{a, b} {
			if _, err := j.s.Open(owner, snap.Path); err != nil {
				t.Fatal(err)
			}
		}
	}
	j.edit(a, "first", 0, 1, "O", "Original")
	j.edit(a, "second", 0, 1, "OO", "OOriginal")
	j.edit(a, "second", 0, 2, "OOO", "OOOriginal")
	j.edit(b, "", 8, 10, "AL", "OOOriginAL")
	j.history(a, false, "OriginAL")
	restart() // Restore cancellation provenance from ordinary command deltas.
	j.history(a, false, "originAL")
	j.history(a, true, "OriginAL")
	j.history(a, true, "OOOriginAL")
	if err := j.s.persistDocument(j.s.docs[j.id]); err != nil {
		t.Fatal(err)
	}
	restart() // Restore identical provenance from a structural checkpoint.
	j.history(a, false, "OriginAL")
	j.history(a, false, "originAL")
	j.history(a, true, "OriginAL")
	j.history(a, true, "OOOriginAL")
}

func TestDocumentHistoryUndoRedoRollsWithoutExhaustion(t *testing.T) {
	j, a, _ := newDocumentHistoryJourney(t, "x")
	for i := 0; i < documentHistoryLimit+20; i++ {
		j.edit(a, "", 0, 1, "y", "y")
		j.history(a, false, "x")
		j.history(a, true, "y")
		j.history(a, false, "x")
	}
	sync, err := j.s.Sync(a, j.id, 1)
	if err != nil || !sync.Reset {
		t.Fatalf("rolled sync=%+v err=%v", sync, err)
	}
}

func TestDocumentHistoryEqualPeerBytesDoNotAuthorizeCancellation(t *testing.T) {
	j, a, b := newDocumentHistoryJourney(t, "before")
	j.edit(a, "", 0, 6, "after", "after")
	// A peer returning equal bytes is still peer work, not a recorded inverse.
	j.edit(b, "", 0, 5, "before", "before")
	j.rejected(a, false, document.ErrOverlap, "before")
	j.history(b, false, "after")
	j.history(a, false, "before")
}

func TestDocumentHistoryOpaquePriorInverseFailsClosed(t *testing.T) {
	s, db, snap, a := durableDeltaFixture(t)
	j := &documentHistoryJourney{t: t, s: s, id: snap.ID, revision: snap.Revision}
	j.edit(a, "", 0, 8, "first", "first")
	j.edit(a, "", 0, 5, "second", "second")
	j.history(a, false, "first")
	// Provenance is optional metadata, not a license to infer historical pairs.
	// A prior checkpoint lacking it must remain readable and conservative.
	s.docs[snap.ID].journal[2].inverseOf = 0
	if err := s.persistDocument(s.docs[snap.ID]); err != nil {
		t.Fatal(err)
	}
	s.Close()
	j.s = NewDocumentService(s.root, nil, nil, db.DB)
	t.Cleanup(j.s.Close)
	if _, err := j.s.Open(a, snap.Path); err != nil {
		t.Fatal(err)
	}
	j.rejected(a, false, document.ErrOverlap, "first")
	j.history(a, true, "second")
	j.history(a, false, "first")
}

func TestDocumentHistoryDenseGroupedUndoAllRedoAll(t *testing.T) {
	j, a, _ := newDocumentHistoryJourney(t, "state-0")
	for i := 1; i <= 80; i++ {
		before := fmt.Sprintf("state-%d", i-1)
		middle := fmt.Sprintf("intermediate-%d", i)
		after := fmt.Sprintf("state-%d", i)
		group := fmt.Sprintf("command-%d", i)
		j.edit(a, group, 0, len(before), middle, middle)
		j.edit(a, group, 0, len(middle), after, after)
	}
	for cycle := 0; cycle < 3; cycle++ {
		for i := 79; i >= 0; i-- {
			j.history(a, false, fmt.Sprintf("state-%d", i))
		}
		j.rejected(a, false, nil, "state-0")
		for i := 1; i <= 80; i++ {
			j.history(a, true, fmt.Sprintf("state-%d", i))
		}
		j.rejected(a, true, nil, "state-80")
	}
}

func TestDocumentHistorySyncPreservesLogicalGroupAcrossUndoRedo(t *testing.T) {
	j, a, b := newDocumentHistoryJourney(t, "start")
	j.edit(a, "logical-command", 0, 5, "middle", "middle")
	j.edit(a, "logical-command", 0, 6, "finish", "finish")
	for cycle := 0; cycle < 2; cycle++ {
		j.history(a, false, "start")
		j.history(a, true, "finish")
	}
	sync, err := j.s.Sync(b, j.id, 1)
	if err != nil || sync.Reset || len(sync.Changes) != 10 {
		t.Fatalf("sync=%+v err=%v", sync, err)
	}
	for _, change := range sync.Changes {
		if change.GroupID != "logical-command" {
			t.Fatalf("history lost logical group: %+v", change)
		}
	}
}
