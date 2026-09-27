package editor

import (
	"crypto/sha256"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"crude/textdiff"
	"crude/tui/theme"
)

func contentIdentityEditor(text string) *Editor {
	pt := NewPieceTable()
	pt.ReplaceAll(text)
	ed := New(theme.DefaultDark())
	ed.OpenBuffer(newBuffer(pt, "identity.txt", true))
	return ed
}

func TestManualInverseEditClearsDirtyAndImmediateGutter(t *testing.T) {
	ed := contentIdentityEditor("first\nsecond\nthird\n")
	_ = gutterRailCharAt(t, ed, 0)
	ed.SetGitAnnotation(ed.Revision(), ed.Value(), nil, nil, false)
	ed.SetCursorPosition(1, 0)
	ed.InsertRune('x')
	if !ed.Modified() || ed.gitKindAt(1) != GitLineModified {
		t.Fatal("edit was not represented immediately")
	}
	ed.deleteBackward()
	if ed.Modified() {
		t.Fatal("byte-identical manual edit remains dirty")
	}
	if got := gutterRailCharAt(t, ed, 1); got != ' ' {
		t.Fatalf("inverse edit left a painted false change: %q", got)
	}
	ed.Undo()
	if !ed.Modified() {
		t.Fatal("undoing the inverse must restore a real change")
	}
	ed.Redo()
	if ed.Modified() {
		t.Fatal("redo to saved bytes must be clean")
	}
}

func TestSaveAcknowledgementUsesBytesRatherThanRevisionIdentity(t *testing.T) {
	ed := contentIdentityEditor("saved")
	ed.InsertRune('x')
	snapshot := ed.BufferSnapshot()
	ed.InsertRune('y')
	ed.Undo()
	ed.AcceptSave(snapshot.Revision, sha256.Sum256(snapshot.Data), snapshot.Data)
	if ed.Modified() {
		t.Fatal("late save acknowledgement re-dirtied identical bytes at a newer revision")
	}
}

func TestUndoRemovesImmediateDeletionMarker(t *testing.T) {
	ed := contentIdentityEditor("one\ntwo\nthree\n")
	ed.SetGitAnnotation(ed.Revision(), ed.Value(), nil, nil, false)
	ed.SetCursorPosition(1, 0)
	_, _ = ed.buf.Delete(Position{Line: 1}, Position{Line: 2})
	ed.notifyContentChanged()
	if ed.gitDeletionMarkerAt(1, ed.LineCount()) == ' ' {
		t.Fatal("deletion has no immediate boundary marker")
	}
	_, _ = ed.buf.Insert(Position{Line: 1}, []byte("two\n"))
	ed.notifyContentChanged()
	if len(ed.GitHunks()) != 0 {
		t.Fatal("restoring deleted bytes left an immediate change")
	}
}

func TestGitProjectionGroupedUndoAndSharedViews(t *testing.T) {
	first := contentIdentityEditor("one\ntwo\nthree\n")
	second := New(theme.DefaultDark())
	second.OpenBuffer(first.buffer)
	head := first.Value()
	first.SetGitAnnotation(first.Revision(), head, nil, nil, false)
	second.SetGitAnnotation(second.Revision(), head, nil, nil, false)
	first.beginUndoGroup()
	for i := 0; i < 80; i++ {
		first.InsertRune('x')
	}
	first.endUndoGroup()
	if second.gitKindAt(0) != GitLineModified || !second.Modified() {
		t.Fatal("another view did not observe shared edits")
	}
	first.Undo()
	if len(first.GitHunks()) != 0 || len(second.GitHunks()) != 0 || second.Modified() {
		t.Fatal("grouped undo beyond the old edit-log bound retained false changes")
	}
	first.Redo()
	if len(second.GitHunks()) != 1 || !second.Modified() {
		t.Fatal("shared redo was not represented")
	}
}

func BenchmarkBufferSavepointManualRoundTripLargeFile(b *testing.B) {
	pt := NewPieceTable()
	pt.ReplaceAll(strings.Repeat("stable source line\n", 100_000))
	buffer := newBuffer(pt, "large.txt", true)
	base := append([]piece(nil), pt.pieces...)
	pt.add = []byte("x")
	changed := []piece{{buffer: addBuffer, length: 1}, {buffer: originalBuffer, length: len(pt.original)}}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		pt.pieces = changed
		buffer.NoteEdit()
		if !buffer.IsDirty() {
			b.Fatal("changed text is clean")
		}
		pt.pieces = base
		buffer.NoteEdit()
		if buffer.IsDirty() {
			b.Fatal("identical text is dirty")
		}
	}
}

func TestIncrementalGitHunksAlwaysReconstructLiveBytes(t *testing.T) {
	for _, head := range []string{"", "same\nsame\n\nsame\ntail", "alpha\nbeta\ngamma\n"} {
		ed := contentIdentityEditor(head)
		ed.SetGitAnnotation(ed.Revision(), head, nil, nil, false)
		rng := rand.New(rand.NewPCG(11, 29))
		position := func(text string, offset int) Position {
			prefix := text[:offset]
			return Position{Line: strings.Count(prefix, "\n"), Col: len(prefix) - strings.LastIndex(prefix, "\n") - 1}
		}
		for step := 0; step < 500; step++ {
			current := ed.Value()
			offset := rng.IntN(len(current) + 1)
			if step%41 == 0 {
				ed.ReplaceDocument(head)
			} else if len(current) > 0 && rng.IntN(2) == 0 {
				end := offset + rng.IntN(len(current)-offset+1)
				_, _ = ed.buf.Delete(position(current, offset), position(current, end))
				ed.notifyContentChanged()
			} else {
				values := []string{"x", "\n", "same\n", "a\nb\n", "tail"}
				_, _ = ed.buf.Insert(position(current, offset), []byte(values[rng.IntN(len(values))]))
				ed.notifyContentChanged()
			}
			lines := textdiff.Lines(head)
			var restored []string
			old := 0
			for _, h := range ed.GitHunks() {
				if h.OldStart < old || h.OldStart+len(h.OldLines) > len(lines) {
					t.Fatalf("step %d invalid old range: %+v", step, h)
				}
				restored = append(restored, lines[old:h.OldStart]...)
				if h.NewStart != len(restored) {
					t.Fatalf("step %d new offset=%d want=%d hunks=%+v live=%q", step, h.NewStart, len(restored), ed.GitHunks(), ed.Value())
				}
				if strings.Join(h.OldLines, "") != strings.Join(lines[h.OldStart:h.OldStart+len(h.OldLines)], "") {
					t.Fatal("hunk preimage does not match HEAD")
				}
				restored = append(restored, h.NewLines...)
				old = h.OldStart + len(h.OldLines)
			}
			restored = append(restored, lines[old:]...)
			if got := strings.Join(restored, ""); got != ed.Value() {
				t.Fatalf("step %d projection=%q live=%q hunks=%+v", step, got, ed.Value(), ed.GitHunks())
			}
			if ed.Modified() != (ed.Value() != head) {
				t.Fatalf("step %d dirty flag does not equal byte difference", step)
			}
		}
	}
}

func BenchmarkImmediateGitGutterLargeFile(b *testing.B) {
	for _, mode := range []string{"baseline", "tracked", "untracked"} {
		b.Run(mode, func(b *testing.B) {
			var text strings.Builder
			for i := 0; i < 20_000; i++ {
				fmt.Fprintf(&text, "source line %06d remains unchanged\n", i)
			}
			ed := contentIdentityEditor(text.String())
			if mode == "tracked" {
				ed.SetGitAnnotation(ed.Revision(), ed.Value(), nil, nil, false)
			}
			if mode == "untracked" {
				ed.SetGitAnnotation(ed.Revision(), "", textdiff.Hunks("", ed.Value()), nil, false)
			}
			ed.SetCursorPosition(10_000, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				ed.InsertRune('x')
				ed.Undo()
			}
		})
	}
}
