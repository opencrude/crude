package editor

import (
	"reflect"
	"testing"

	"crude/document"
)

func TestSourcePositionExactByteOffsets(t *testing.T) {
	// Independent source byte boundaries: the flag occupies bytes [6,14),
	// the ZWJ cluster [14,25), the modifier cluster [25,33), and CJK [33,36).
	pt := NewPieceTable()
	pt.ReplaceAll("ab\te\u0301🇺🇳👩‍💻👍🏽中Z\r\n\tQ")
	for _, tc := range []struct{ col, offset, snapped int }{
		{0, 0, 0}, {1, 1, 1}, {2, 2, 2}, {3, 2, 2},
		{4, 3, 4}, {5, 6, 5}, {6, 6, 5}, {7, 14, 7},
		{8, 14, 7}, {9, 25, 9}, {10, 25, 9}, {11, 33, 11},
		{12, 33, 11}, {13, 36, 13}, {14, 37, 14}, {100, 37, 14},
	} {
		pos := Position{Col: tc.col}
		if got := pt.positionToOffset(pos); got != tc.offset {
			t.Errorf("col %d: offset=%d want %d", tc.col, got, tc.offset)
		}
		if got := pt.snapPosition(pos); got != (Position{Col: tc.snapped}) {
			t.Errorf("col %d: snap=%v want col %d", tc.col, got, tc.snapped)
		}
	}
	if got := pt.positionToOffset(Position{Line: 1, Col: 4}); got != 40 {
		t.Fatalf("second line after tab offset=%d want 40", got)
	}
	for _, tc := range []struct {
		offset int
		pos    Position
	}{
		{-1, Position{}}, {4, Position{Col: 4}}, {5, Position{Col: 4}},
		{6, Position{Col: 5}}, {10, Position{Col: 5}},
		{20, Position{Col: 7}}, {28, Position{Col: 9}},
		{35, Position{Col: 11}}, {37, Position{Col: 14}}, {38, Position{Col: 14}},
		{39, Position{Line: 1}}, {40, Position{Line: 1, Col: 4}},
		{100, Position{Line: 1, Col: 5}},
	} {
		if got := pt.positionAtOffset(tc.offset); got != tc.pos {
			t.Errorf("offset %d: position=%v want %v", tc.offset, got, tc.pos)
		}
	}
}

func TestSourceMovementVisitsWholeUnits(t *testing.T) {
	text := "ab\te\u0301🇺🇳👩‍💻👍🏽中Z\r\n\tQ"
	ed := loadEditorText(t, "source.txt", text)
	want := []Position{
		{}, {Col: 1}, {Col: 2}, {Col: 4}, {Col: 5}, {Col: 7},
		{Col: 9}, {Col: 11}, {Col: 13}, {Col: 14},
		{Line: 1}, {Line: 1, Col: 4}, {Line: 1, Col: 5},
	}
	for i := 1; i < len(want); i++ {
		ed.moveRight()
		if ed.cursor != want[i] {
			t.Fatalf("right step %d: %v want %v", i, ed.cursor, want[i])
		}
	}
	ed.moveRight()
	if ed.cursor != want[len(want)-1] {
		t.Fatal("right at EOF must not move")
	}
	for i := len(want) - 2; i >= 0; i-- {
		ed.moveLeft()
		if ed.cursor != want[i] {
			t.Fatalf("left step %d: %v want %v", i, ed.cursor, want[i])
		}
	}
	ed.moveLeft()
	if ed.cursor != (Position{}) {
		t.Fatal("left at BOF must not move")
	}
	if got := string(ed.buf.Bytes()); got != text {
		t.Fatalf("motion changed source bytes: %q", got)
	}
}

func TestSourceMovementSnapsInteriorColumns(t *testing.T) {
	ed := loadEditorText(t, "source.txt", "ab\t🇺🇳x")
	for _, tc := range []struct{ col, left, right int }{
		{3, 2, 4}, // Inside the off-stop tab span.
		{5, 4, 6}, // Inside the flag's second cell.
	} {
		ed.cursor = Position{Col: tc.col}
		ed.moveLeft()
		if ed.cursor.Col != tc.left {
			t.Errorf("left from %d=%d want %d", tc.col, ed.cursor.Col, tc.left)
		}
		ed.cursor = Position{Col: tc.col}
		ed.moveRight()
		if ed.cursor.Col != tc.right {
			t.Errorf("right from %d=%d want %d", tc.col, ed.cursor.Col, tc.right)
		}
	}
}

func TestSourceVerticalMovementPreservesUnsnappedGoal(t *testing.T) {
	ed := loadEditorText(t, "source.txt", "abcdefg\r\nab\tX\r\n🇺🇳中x\r\nz\r\nabcdefg")
	ed.cursor = Position{Col: 3}
	for line, col := range []int{2, 2, 1, 3} {
		ed.moveDown()
		if ed.cursor != (Position{Line: line + 1, Col: col}) || !ed.goalSet || ed.goalCol != 3 {
			t.Fatalf("down line %d: cursor=%v goal=%d/%v", line+1, ed.cursor, ed.goalCol, ed.goalSet)
		}
	}
	for _, pos := range []Position{{Line: 3, Col: 1}, {Line: 2, Col: 2}, {Line: 1, Col: 2}, {Col: 3}} {
		ed.moveUp()
		if ed.cursor != pos {
			t.Fatalf("up: cursor=%v want %v", ed.cursor, pos)
		}
	}
	ed.viewH = 3 // One logical line per page.
	ed.pageDown()
	if ed.cursor != (Position{Line: 1, Col: 2}) || ed.goalCol != 3 {
		t.Fatalf("page down: cursor=%v goal=%d", ed.cursor, ed.goalCol)
	}
	ed.pageUp()
	if ed.cursor != (Position{Col: 3}) {
		t.Fatalf("page up did not restore goal: %v", ed.cursor)
	}
}

func TestSourceWordMovementUsesFullClusterWidths(t *testing.T) {
	ed := loadEditorText(t, "source.txt", "ab\te\u0301 🇺🇳👩‍💻👍🏽中\r\n\t \r\n\r\nnext")
	for _, col := range []int{2, 5, 12, 14} {
		ed.moveWordRight()
		if ed.cursor != (Position{Col: col}) {
			t.Fatalf("word right cursor=%v want col %d", ed.cursor, col)
		}
	}
	ed.cursor = Position{Line: 3}
	ed.moveWordLeft()
	if ed.cursor != (Position{Col: 12}) {
		t.Fatalf("word left over blank CRLF lines=%v want last word at col 12", ed.cursor)
	}
	for _, col := range []int{6, 4, 0} {
		ed.moveWordLeft()
		if ed.cursor != (Position{Col: col}) {
			t.Fatalf("word left cursor=%v want col %d", ed.cursor, col)
		}
	}
}

func TestSourceCRLFContentAndExactTerminatorDeletion(t *testing.T) {
	pt := NewPieceTable()
	pt.ReplaceAll("a\r\n\r\n中\r\n")
	for line, want := range []string{"a", "", "中", ""} {
		got, err := pt.Line(line)
		if err != nil || string(got) != want {
			t.Fatalf("line %d=%q/%v want %q", line, got, err, want)
		}
	}
	start := Position{Col: 1}
	end := pt.nextPosition(start)
	if got := pt.GetText(start, end); got != "\r\n" {
		t.Fatalf("line transition source=%q want full CRLF", got)
	}
	if got := pt.previousPosition(end); got != start {
		t.Fatalf("back across CRLF=%v want %v", got, start)
	}
	_, _ = pt.Delete(start, end)
	if got := string(pt.Bytes()); got != "a\r\n中\r\n" {
		t.Fatalf("terminator deletion=%q", got)
	}
}

func TestSourceVisualEditsPreserveExactClusters(t *testing.T) {
	pt := NewPieceTable()
	pt.ReplaceAll("a\te\u0301🇺🇳👩‍💻👍🏽中Z")
	if got := pt.GetText(Position{Col: 4}, Position{Col: 11}); got != "e\u0301🇺🇳👩‍💻👍🏽" {
		t.Fatalf("source selection=%q", got)
	}
	_, _ = pt.Insert(Position{Col: 3}, []byte("X")) // Floor inside tab.
	if got := string(pt.Bytes()); got != "aX\te\u0301🇺🇳👩‍💻👍🏽中Z" {
		t.Fatalf("insertion at tab interior=%q", got)
	}
	_, _ = pt.Delete(Position{Col: 4}, Position{Col: 11})
	if got := string(pt.Bytes()); got != "aX\t中Z" {
		t.Fatalf("source cluster deletion=%q", got)
	}
}

func TestSourceBytePrimitivesInvertJoinedGrapheme(t *testing.T) {
	pt := NewPieceTable()
	pt.ReplaceAll("e\r\n")
	var edits []document.Edit
	pt.mutationObserver = func(edit document.Edit) { edits = append(edits, edit) }
	_, _ = pt.insertAtOffset(1, []byte("\u0301"))
	if got := string(pt.Bytes()); got != "e\u0301\r\n" {
		t.Fatalf("combining insertion=%q", got)
	}
	_, _ = pt.deleteOffsets(1, 3)
	if got := string(pt.Bytes()); got != "e\r\n" {
		t.Fatalf("exact inverse=%q", got)
	}
	want := []document.Edit{{Start: 1, End: 1, Text: "\u0301"}, {Start: 1, End: 3}}
	if !reflect.DeepEqual(edits, want) {
		t.Fatalf("observed edits=%#v want %#v", edits, want)
	}
	if change := pt.lineChanges[pt.revision%uint64(len(pt.lineChanges))]; change.start != 0 || change.end != 0 || change.delta != 0 {
		t.Fatalf("byte inverse line change=%+v", change)
	}
}

func TestSourceBytePrimitivesRestoreEntireDeletedBuffer(t *testing.T) {
	pt := NewPieceTable()
	pt.ReplaceAll("e\u0301\r\n\t🇺🇳")
	_, _ = pt.insertAtOffset(14, []byte("!")) // Leave prior add-buffer content after deletion.
	_, _ = pt.deleteOffsets(0, 15)
	if got := string(pt.Bytes()); got != "" {
		t.Fatalf("delete entire buffer=%q", got)
	}
	_, _ = pt.insertAtOffset(0, []byte("e\u0301\r\n\t🇺🇳"))
	if got := string(pt.Bytes()); got != "e\u0301\r\n\t🇺🇳" {
		t.Fatalf("restore entire buffer=%q", got)
	}
}
