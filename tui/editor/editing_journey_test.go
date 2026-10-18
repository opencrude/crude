package editor

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestEditingJourneySplitTabIndentAndRejoin(t *testing.T) {
	e := opsEditor(t, "sample.go", "\tfoo bar baz\n")
	e.SetCursorPosition(0, 8)
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "\tfoo \n\tbar baz\n", e.Value())
	require.Equal(t, Position{Line: 1, Col: 4}, e.cursor)
	press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "\tfoo \nbar baz\n", e.Value())
	require.Equal(t, Position{Line: 1}, e.cursor, "removing a source tab must not strand the caret inside the next word")
	press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
	require.Equal(t, "\tfoo bar baz\n", e.Value())
	e.Undo()
	require.Equal(t, "\tfoo \nbar baz\n", e.Value())
	e.Undo()
	require.Equal(t, "\tfoo \n\tbar baz\n", e.Value())
	e.Undo()
	require.Equal(t, "\tfoo bar baz\n", e.Value())
}

func TestEditingJourneySourceUnitsRoundTrip(t *testing.T) {
	for _, unit := range []string{"\t", "e\u0301", "👨‍👩‍👧‍👦", "🇨🇦", "👍🏽", "界"} {
		t.Run(unit, func(t *testing.T) {
			for _, backward := range []bool{false, true} {
				e := opsEditor(t, "sample.txt", unit+"word")
				if backward {
					e.SetCursorPosition(0, LineVisualWidth(unit, 4))
					press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
				} else {
					press(e, tea.KeyPressMsg{Code: tea.KeyDelete})
				}
				require.Equal(t, "word", e.Value())
				require.Equal(t, Position{}, e.cursor)
				e.Undo()
				require.Equal(t, unit+"word", e.Value(), "undo must restore original source bytes, not display characters")
				e.Redo()
				require.Equal(t, "word", e.Value())
			}
		})
	}
}

func TestEditingJourneyEmptyLineDeleteAndCRLF(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		e := opsEditor(t, "sample.txt", "a"+newline+newline+"b")
		e.SetCursorPosition(1, 0)
		press(e, tea.KeyPressMsg{Code: tea.KeyDelete})
		require.Equal(t, "a"+newline+"b", e.Value())
		e.Undo()
		require.Equal(t, "a"+newline+newline+"b", e.Value())
		e.Redo()
		require.Equal(t, "a"+newline+"b", e.Value())
	}
}

func TestEditingJourneyCombiningInputUndo(t *testing.T) {
	e := opsEditor(t, "sample.txt", "ex")
	e.SetCursorPosition(0, 1)
	typeChar(e, '\u0301')
	require.Equal(t, "e\u0301x", e.Value())
	require.Equal(t, Position{Col: 1}, e.cursor)
	e.Undo()
	require.Equal(t, "ex", e.Value())
	e.Redo()
	require.Equal(t, "e\u0301x", e.Value())
}

func TestEditingJourneyReplacementAndViewUndo(t *testing.T) {
	e := opsEditor(t, "sample.txt", "hello world")
	selectRange(e, 0, 0, 0, 5)
	_ = e.HandleEvent(tea.PasteMsg{Content: "goodbye"})
	require.Equal(t, "goodbye world", e.Value())
	e.Undo()
	require.Equal(t, "hello world", e.Value())
	require.Equal(t, "hello", e.SelectedText())
	e.Redo()
	require.Equal(t, "goodbye world", e.Value())
	require.False(t, e.HasSelection())
	require.Equal(t, Position{Col: 7}, e.cursor)
}

func TestEditingJourneyReplaceAllIsUndoable(t *testing.T) {
	e := opsEditor(t, "sample.txt", "foo x foo\nfoo")
	e.SetQuery("foo")
	e.SetReplace("bar baz")
	require.Equal(t, 3, e.ReplaceAll())
	require.Equal(t, "bar baz x bar baz\nbar baz", e.Value())
	e.Undo()
	require.Equal(t, "foo x foo\nfoo", e.Value())
	e.Redo()
	require.Equal(t, "bar baz x bar baz\nbar baz", e.Value())
}

func TestEditingJourneyEveryCommandRestoresBytesAndView(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		setup               func(*Editor)
		edit                func(*Editor)
	}{
		{"split", "\talpha beta\n", "\talpha \n\tbeta\n", func(e *Editor) { e.SetCursorPosition(0, 10) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyEnter}) }},
		{"indent", "first\n\tsecond\nthird", "    first\n    \tsecond\nthird", func(e *Editor) { selectRange(e, 0, 1, 2, 0) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyTab}) }},
		{"single selected indent", "abcdef", "    abcdef", func(e *Editor) { selectRange(e, 0, 2, 0, 4) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyTab}) }},
		{"outdent", "    alpha\n\tbeta", "alpha\nbeta", func(e *Editor) { selectRange(e, 0, 1, 1, 5) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) }},
		{"line move", "one\n\ttwo\nthree", "\ttwo\none\nthree", func(e *Editor) { e.SetCursorPosition(1, 5) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}) }},
		{"duplicate", "one\ntwo", "one\none\ntwo", func(e *Editor) { e.SetCursorPosition(0, 2) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl | tea.ModShift}) }},
		{"delete line", "one\ntwo\nthree", "one\nthree", func(e *Editor) { e.SetCursorPosition(1, 2) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl | tea.ModShift}) }},
		{"comment", "\talpha\n beta", "\t// alpha\n // beta", func(e *Editor) { selectRange(e, 0, 0, 1, 3) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}) }},
		{"wrap", "alpha beta", "(alpha) beta", func(e *Editor) { selectRange(e, 0, 0, 0, 5) }, func(e *Editor) { typeChar(e, '(') }},
		{"pair", "", "()", func(*Editor) {}, func(e *Editor) { typeChar(e, '(') }},
		{"pair delete", "()", "", func(e *Editor) { e.SetCursorPosition(0, 1) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyBackspace}) }},
		{"replace selection", "alpha beta", "界\n\t👩‍💻 beta", func(e *Editor) { selectRange(e, 0, 0, 0, 5) }, func(e *Editor) { _ = e.HandleEvent(tea.PasteMsg{Content: "界\n\t👩‍💻"}) }},
		{"format", "a { b }", "a {\n    b\n}", func(e *Editor) { e.SetCursorPosition(0, 3) }, func(e *Editor) { e.ApplyFormatted("a {\n    b\n}") }},
		{"replace current", "foo foo", "bar foo", func(e *Editor) { e.SetQuery("foo"); e.SetReplace("bar") }, func(e *Editor) { e.ReplaceCurrent() }},
		{"indent backspace", "        word", "    word", func(e *Editor) { e.SetCursorPosition(0, 8) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyBackspace}) }},
		{"crlf enter", "\talpha beta\r\n", "\talpha \r\n\tbeta\r\n", func(e *Editor) { e.SetCursorPosition(0, 10) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyEnter}) }},
		{"bracket enter", "\t{}", "\t{\n\t\t\n\t}", func(e *Editor) { e.SetCursorPosition(0, 5) }, func(e *Editor) { press(e, tea.KeyPressMsg{Code: tea.KeyEnter}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, "sample.go", tc.before)
			tc.setup(e)
			before := e.captureViewState()
			tc.edit(e)
			require.Equal(t, tc.after, e.Value())
			after := e.captureViewState()
			for range 3 {
				e.Undo()
				require.Equal(t, tc.before, e.Value())
				require.Equal(t, before, e.captureViewState(), "undo restores selection and caret")
				e.Redo()
				require.Equal(t, tc.after, e.Value())
				require.Equal(t, after, e.captureViewState(), "redo restores final selection and caret")
			}
		})
	}
}

func TestEditingJourneySelectionCollapseAndTypingBoundaries(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		for _, key := range []rune{tea.KeyLeft, tea.KeyRight} {
			e := opsEditor(t, "sample.txt", "abcdef")
			if reverse {
				selectRange(e, 0, 4, 0, 1)
			} else {
				selectRange(e, 0, 1, 0, 4)
			}
			press(e, tea.KeyPressMsg{Code: key})
			want := 1
			if key == tea.KeyRight {
				want = 4
			}
			require.Equal(t, Position{Col: want}, e.cursor)
			require.False(t, e.HasSelection())
		}
	}
	e := opsEditor(t, "sample.txt", "")
	typeChar(e, 'a')
	typeChar(e, 'b')
	press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
	typeChar(e, 'c')
	typeChar(e, 'd')
	for _, want := range []string{"ab\n", "ab", ""} {
		press(e, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
		require.Equal(t, want, e.Value())
	}
	for _, want := range []string{"ab", "ab\n", "ab\ncd"} {
		press(e, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift})
		require.Equal(t, want, e.Value())
	}
	press(e, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	typeChar(e, 'X')
	press(e, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	require.Equal(t, "ab\nX", e.Value(), "new typing invalidates redo")
}

// A saved-state oracle is deliberately independent of the editor's recorded
// inverses. Mix actual key and paste commands, then traverse the entire history
// repeatedly; an incorrect recorded span cannot satisfy the literal snapshots.
func TestEditingJourneyRandomCommandsUndoAllRedoAll(t *testing.T) {
	for seed := int64(0); seed < 12; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			e := opsEditor(t, "sample.go", "\talpha é👩‍💻\n    beta\n\ngamma\r\n")
			rng := rand.New(rand.NewSource(seed))
			states := []string{e.Value()}
			for range 80 {
				line := rng.Intn(e.LineCount())
				press(e, tea.KeyPressMsg{Code: tea.KeyHome}) // explicit navigation ends typing coalescing
				e.SetCursorPosition(line, rng.Intn(e.lineVisualWidth(line)+1))
				before := e.Value()
				beforeHistory := e.changeIndex
				operation := rng.Intn(8)
				switch operation {
				case 0:
					_ = e.HandleEvent(tea.PasteMsg{Content: []string{"X", "\t", "é", "👩‍💻", "界\n"}[rng.Intn(5)]})
				case 1:
					press(e, tea.KeyPressMsg{Code: tea.KeyEnter})
				case 2:
					press(e, tea.KeyPressMsg{Code: tea.KeyBackspace})
				case 3:
					press(e, tea.KeyPressMsg{Code: tea.KeyDelete})
				case 4:
					press(e, tea.KeyPressMsg{Code: tea.KeyTab})
				case 5:
					press(e, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
				case 6:
					press(e, tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
				case 7:
					press(e, tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl})
				}
				if before != e.Value() {
					states = append(states, e.Value())
				} else {
					require.Equal(t, beforeHistory, e.changeIndex, "no-op command %d must not create a history step", operation)
				}
			}
			for range 3 {
				for i := len(states) - 2; i >= 0; i-- {
					e.Undo()
					require.Equal(t, states[i], e.Value(), "undo %d", i)
				}
				for i := 1; i < len(states); i++ {
					e.Redo()
					require.Equal(t, states[i], e.Value(), "redo %d", i)
				}
			}
		})
	}
}

func TestEditingJourneyLargeGroupIsNotPartiallyPruned(t *testing.T) {
	before := strings.Repeat("foo ", 600)
	e := opsEditor(t, "sample.txt", before)
	e.SetQuery("foo")
	e.SetReplace("bar")
	require.Equal(t, 600, e.ReplaceAll())
	e.Undo()
	require.Equal(t, before, e.Value())
	e.Redo()
	require.Equal(t, strings.Repeat("bar ", 600), e.Value())
}
