package editor

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"crude/document"
)

func TestPieceTableBatchCommitsExactBytesOneGeometryAndSequentialObservations(t *testing.T) {
	pt := NewPieceTable()
	pt.ReplaceAll("α\r\nfirst middle last\n😀tail")
	// Deliberately split the source into multiple original/add pieces first.
	if _, err := pt.insertAtOffset(4, []byte("!")); err != nil {
		t.Fatal(err)
	}
	before := string(pt.Bytes())
	if before != "α\r\n!first middle last\n😀tail" {
		t.Fatal("bad independent fixture")
	}
	at := strings.Index(before, "middle")
	edits := []document.Edit{
		{Start: 0, End: len("α"), Text: "界"},
		{Start: at, End: at + len("middle"), Text: "two\r\nrows"},
		{Start: len(before), End: len(before), Text: "\nEND"},
	}
	oldRevision := pt.Revision()
	replayed := before
	var observed []document.Edit
	pt.mutationObserver = func(edit document.Edit) {
		observed = append(observed, edit)
		replayed = replayed[:edit.Start] + edit.Text + replayed[edit.End:]
		// An observer can inspect the final source without materializing an
		// intermediate state or observing a half-published batch.
		if got := string(pt.Bytes()); got != "界\r\n!first two\r\nrows last\n😀tail\nEND" {
			t.Fatalf("observer saw incomplete source: %q", got)
		}
	}
	if err := pt.applyOffsets(edits); err != nil {
		t.Fatal(err)
	}
	want := "界\r\n!first two\r\nrows last\n😀tail\nEND"
	if got := string(pt.Bytes()); got != want || replayed != want ||
		pt.Revision() != oldRevision+1 || pt.LineCount() != 5 ||
		!reflect.DeepEqual(observed, []document.Edit{edits[2], edits[1], edits[0]}) {
		t.Fatalf("batch outcome: source=%q replay=%q revision=%d observations=%+v", got, replayed, pt.Revision(), observed)
	}
	change := pt.lineChanges[pt.Revision()%uint64(len(pt.lineChanges))]
	if change.start != 0 || change.end != 2 || change.delta != 2 || change.inline {
		t.Fatalf("batch geometry claimed a false inline edit or lost newlines: %+v", change)
	}
}

func TestPieceTableBatchRefusesLaterInvalidEditBeforeSourceOrHistoryEffects(t *testing.T) {
	for _, edits := range [][]document.Edit{
		{{Start: 0, End: 1, Text: "first"}, {Start: 99, End: 99, Text: "bad"}},
		{{Start: 1, End: 4}, {Start: 3, End: 5}},
		{{Start: 1, End: 1}, {Start: 1, End: 1, Text: "ambiguous"}},
		{{Start: -1, End: 0}},
		make([]document.Edit, document.MaxEdits+1),
	} {
		pt := NewPieceTable()
		pt.ReplaceAll("abcdef")
		revision := pt.Revision()
		add := bytes.Clone(pt.add)
		pieces := append([]piece(nil), pt.pieces...)
		journal := pt.lineChanges
		called := false
		pt.mutationObserver = func(document.Edit) { called = true }
		if err := pt.applyOffsets(edits); err == nil {
			t.Fatal("invalid batch accepted")
		}
		if string(pt.Bytes()) != "abcdef" || pt.Revision() != revision ||
			!bytes.Equal(add, pt.add) || !reflect.DeepEqual(pieces, pt.pieces) ||
			journal != pt.lineChanges || called {
			t.Fatal("invalid later edit partially mutated the source/history")
		}
	}
	pt := NewPieceTable()
	pt.ReplaceAll("same")
	revision := pt.Revision()
	if err := pt.applyOffsets([]document.Edit{{Start: 0, End: 4, Text: "same"}}); err != nil || pt.Revision() != revision {
		t.Fatal("byte-identical batch created a source generation")
	}
}

func FuzzPieceTableByteBatches(f *testing.F) {
	for _, seed := range []string{"", "\x00\x00abc", "dense mirrors", "\xff\x01\x10", "\r\n😀"} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 256 {
			t.Skip()
		}
		pt := NewPieceTable()
		pt.ReplaceAll("PREFIX a\r\nβ😀 middle\nSUFFIX")
		for step := 0; step+3 < len(data); step += 4 {
			// Seed fragmentation through the same single-edit owner, including
			// exact inverses inside joined graphemes, before each vector.
			current := string(pt.Bytes())
			pos := int(data[step]) % (len(current) + 1)
			if _, err := pt.insertAtOffset(pos, []byte{data[step+1]}); err != nil {
				t.Fatal(err)
			}
			before := string(pt.Bytes())
			mid := int(data[step+2]) % (len(before) + 1)
			edits := []document.Edit{{Start: 0, End: mid, Text: string(data[step : step+1])}}
			if mid < len(before) {
				start := mid + 1
				edits = append(edits, document.Edit{Start: start, End: len(before), Text: string(data[step+3 : step+4])})
			}
			want := before
			for i := len(edits) - 1; i >= 0; i-- {
				e := edits[i]
				want = want[:e.Start] + e.Text + want[e.End:]
			}
			if err := pt.applyOffsets(edits); err != nil {
				t.Fatal(err)
			}
			if got := string(pt.Bytes()); got != want || pt.LineCount() != strings.Count(want, "\n")+1 {
				t.Fatalf("piece vector differs from independent slicing: got=%q want=%q", got, want)
			}
		}
	})
}
