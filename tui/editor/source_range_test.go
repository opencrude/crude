package editor

import (
	"bytes"
	"reflect"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSourceByteRangeLiteralSelections(t *testing.T) {
	// Source bytes: a[0,1) 😀[1,5) é[5,8) tab[8,9) Z[9,10)
	// CRLF[10,12) 中[12,15) tab[15,16) q[16,17) LF[17,18).
	const text = "a😀e\u0301\tZ\r\n中\tq\n"
	for _, tc := range []struct {
		name          string
		anchor, end   Position
		active        bool
		start, finish int
		selected      string
	}{
		{"forward", Position{Col: 1}, Position{Col: 9}, true, 1, 10, "😀e\u0301\tZ"},
		{"backward", Position{Col: 9}, Position{Col: 1}, true, 1, 10, "😀e\u0301\tZ"},
		{"released selection", Position{Col: 1}, Position{Col: 9}, false, 1, 10, "😀e\u0301\tZ"},
		{"combining grapheme", Position{Col: 3}, Position{Col: 4}, true, 5, 8, "e\u0301"},
		{"off stop tab", Position{Line: 1, Col: 2}, Position{Line: 1, Col: 4}, true, 15, 16, "\t"},
		{"CRLF only", Position{Col: 9}, Position{Line: 1}, true, 10, 12, "\r\n"},
		{"backward multiline", Position{Line: 1, Col: 4}, Position{Col: 3}, true, 5, 16, "e\u0301\tZ\r\n中\t"},
		{"through trailing LF", Position{Line: 1, Col: 4}, Position{Line: 2}, true, 16, 18, "q\n"},
		{"whole source", Position{}, Position{Line: 2}, true, 0, 18, text},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Editor{buf: NewPieceTable(), cursor: Position{Line: 99},
				selAnchor: tc.anchor, selEnd: tc.end, selecting: tc.active}
			e.buf.ReplaceAll(text)
			start, end, ok := e.SourceByteRange()
			if !ok || start != tc.start || end != tc.finish {
				t.Fatalf("range=(%d,%d,%v), want (%d,%d,true)", start, end, ok, tc.start, tc.finish)
			}
			if text[start:end] != tc.selected {
				t.Fatalf("selected source=%q, want %q", text[start:end], tc.selected)
			}
		})
	}
}

func TestSourceByteRangeLiteralCarets(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		caret      Position
		offset     int
	}{
		{"empty", "", Position{}, 0},
		{"ASCII", "abc", Position{Col: 2}, 2},
		{"EOF", "abc", Position{Col: 3}, 3},
		{"CRLF EOL", "a\r\nb", Position{Col: 1}, 1},
		{"after CRLF", "a\r\nb", Position{Line: 1}, 3},
		{"LF EOL", "ab\n", Position{Col: 2}, 2},
		{"trailing LF EOF", "ab\n", Position{Line: 1}, 3},
		{"trailing CRLF EOF", "a\r\n", Position{Line: 1}, 3},
		{"emoji EOF", "😀", Position{Col: 2}, 4},
		{"combining EOF", "e\u0301", Position{Col: 1}, 3},
		{"tab EOF", "ab\t", Position{Col: 4}, 3},
		{"joined clusters", "🇺🇳👩‍💻👍🏽中", Position{Col: 6}, 27},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Editor{buf: NewPieceTable(), cursor: tc.caret,
				// A collapsed anchor is no selection, even if selecting is set.
				selecting: true, selAnchor: Position{Line: 99}, selEnd: Position{Line: 99}}
			e.buf.ReplaceAll(tc.text)
			start, end, ok := e.SourceByteRange()
			if !ok || start != tc.offset || end != tc.offset {
				t.Fatalf("caret range=(%d,%d,%v), want (%d,%d,true)", start, end, ok, tc.offset, tc.offset)
			}
		})
	}
}

func TestSourceByteRangeRejectsUnrepresentableEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		pos        Position
	}{
		{"negative line", "abc", Position{Line: -1}},
		{"negative column", "abc", Position{Col: -1}},
		{"missing line", "abc\n", Position{Line: 2}},
		{"past EOL", "abc\r\n", Position{Col: 4}},
		{"past empty EOL", "", Position{Col: 1}},
		{"very large line", "abc", Position{Line: int(^uint(0) >> 1)}},
		{"very large column", "abc", Position{Col: int(^uint(0) >> 1)}},
		{"inside tab", "ab\tc", Position{Col: 3}},
		{"inside wide rune", "中", Position{Col: 1}},
		{"inside emoji", "😀", Position{Col: 1}},
		{"inside flag", "🇺🇳", Position{Col: 1}},
		{"inside ZWJ", "👩‍💻", Position{Col: 1}},
		{"inside modifier", "👍🏽", Position{Col: 1}},
		{"zero width ambiguity", "\u200bX", Position{}},
		{"invalid source at caret", "\xff", Position{}},
		{"invalid source elsewhere", "abc\n\xff", Position{Col: 1}},
		{"truncated encoding", "a\xf0\x9f", Position{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"caret", "anchor", "end"} {
				e := &Editor{buf: NewPieceTable(), cursor: tc.pos}
				e.buf.ReplaceAll(tc.text)
				switch mode {
				case "anchor":
					e.selAnchor, e.selEnd = tc.pos, Position{Col: 2}
				case "end":
					e.selAnchor, e.selEnd = Position{Col: 2}, tc.pos
				}
				start, end, ok := e.SourceByteRange()
				if ok || start != 0 || end != 0 {
					t.Errorf("%s: invalid endpoint returned (%d,%d,%v)", mode, start, end, ok)
				}
			}
		})
	}
	for _, e := range []*Editor{nil, {}} {
		if start, end, ok := e.SourceByteRange(); ok || start != 0 || end != 0 {
			t.Fatalf("missing source returned (%d,%d,%v)", start, end, ok)
		}
	}
}

func TestSourceByteRangeIsPureWithColdAndWarmCaches(t *testing.T) {
	for _, warm := range []bool{false, true} {
		for _, invalid := range []bool{false, true} {
			e := &Editor{buf: NewPieceTable(), cursor: Position{Line: 1, Col: 4},
				selecting: true, selAnchor: Position{Col: 1}, selEnd: Position{Line: 1, Col: 4},
				scrollX: 3, scrollY: 1, goalCol: 8, goalSet: true,
				openUndo: openUndoStep{group: 7}, historyID: 123}
			e.buf.ReplaceAll("a😀\r\n\tQ")
			e.buf.changes = []Change{{Type: ActInsert, Text: []byte("history"), Offset: 1, Group: 7}}
			e.buf.changeIndex, e.buf.undoGroupSeq = 1, 7
			if invalid {
				e.selEnd.Col = 2 // Inside the second line's tab.
			}
			if warm {
				e.buf.ensureCache()
			}
			// Capture query-relevant editor values, not the Editor itself:
			// Editor owns live watcher/diagnostic mutexes that must not be copied.
			editorState := func() any {
				changes := slices.Clone(e.buf.changes)
				for i := range changes {
					changes[i].Text = bytes.Clone(changes[i].Text)
				}
				return struct {
					View           editorViewState
					GoalCol        int
					GoalSet        bool
					History        []Change
					ChangeIndex    int
					Groups         [5]int
					HistoryID      uint64
					ContentPending bool
					Buffer         *PieceTable
					Shared         *Buffer
					Projection     *DocumentProjection
				}{
					e.captureViewState(), e.goalCol, e.goalSet, changes, e.buf.changeIndex,
					[5]int{e.buf.undoGroupSeq, e.undoGroupDepth, e.undoGroupID, e.undoGroupStart, e.openUndo.group},
					e.historyID, e.contentChangePending, e.buf, e.buffer, e.documentProjection,
				}
			}
			beforeEditor, beforeSource := editorState(), *e.buf
			beforeSource.original = bytes.Clone(e.buf.original)
			beforeSource.add = bytes.Clone(e.buf.add)
			beforeSource.pieces = slices.Clone(e.buf.pieces)
			beforeSource.cachedBytes = bytes.Clone(e.buf.cachedBytes)
			beforeSource.lineOffsets = slices.Clone(e.buf.lineOffsets)
			for i := 0; i < 2; i++ {
				start, end, ok := e.SourceByteRange()
				if invalid {
					if ok || start != 0 || end != 0 {
						t.Fatal("invalid source endpoint accepted")
					}
				} else if !ok || start != 1 || end != 8 {
					t.Fatalf("source range=(%d,%d,%v), want (1,8,true)", start, end, ok)
				}
			}
			if !reflect.DeepEqual(beforeEditor, editorState()) || !reflect.DeepEqual(beforeSource, *e.buf) {
				t.Fatalf("query mutated editor/source state (warm=%v, invalid=%v)", warm, invalid)
			}
		}
	}
}

func TestSourceByteRangeFollowsSelectionEditUndoRedo(t *testing.T) {
	e := opsEditor(t, "range.txt", "a😀\tZ\r\n中")
	t.Cleanup(e.Close)
	check := func(start, end int, selected string) {
		t.Helper()
		gotStart, gotEnd, ok := e.SourceByteRange()
		if !ok || gotStart != start || gotEnd != end {
			t.Fatalf("range=(%d,%d,%v), want (%d,%d,true)", gotStart, gotEnd, ok, start, end)
		}
		if got := e.Value()[gotStart:gotEnd]; got != selected {
			t.Fatalf("source slice=%q, want %q", got, selected)
		}
	}
	e.SetCursorPosition(0, 1)
	check(1, 1, "")
	press(e, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	check(1, 5, "😀")
	typeChar(e, 'X')
	check(2, 2, "")
	if got := e.Value(); got != "aX\tZ\r\n中" {
		t.Fatalf("replacement=%q", got)
	}
	e.Undo()
	check(1, 5, "😀")
	e.Redo()
	check(2, 2, "")
}
