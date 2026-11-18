package editor

import (
	"slices"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

type testGhostUse struct {
	owner   string
	outcome GhostOutcome
	text    string
	caret   Position
}

func TestGhostOutcomeTabCommitsInsertionReplacementOrJump(t *testing.T) {
	const initial = "call(old)\nnext()"
	for _, tc := range []struct {
		name       string
		offer      Suggestion
		wantText   string
		wantCaret  Position
		want       GhostOutcome
		wantEdited bool
	}{
		{"insert", Suggestion{Start: 5, End: 5, Text: "prefix + "}, "call(prefix + old)\nnext()", Position{Line: 0, Col: 14}, GhostAccepted, true},
		{"replace", Suggestion{Start: 5, End: 8, Text: "fresh"}, "call(fresh)\nnext()", Position{Line: 0, Col: 10}, GhostAccepted, true},
		{"jump", Suggestion{Start: 10, End: 10, Marker: "Next edit", JumpTo: 14}, initial, Position{Line: 1, Col: 4}, GhostJumped, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInlayTestEditor(t, initial)
			e.SetCursorPosition(0, 5)
			before := e.Revision()
			var got []testGhostUse
			e.SetOnGhostOutcome(func(owner string, outcome GhostOutcome) {
				got = append(got, testGhostUse{owner, outcome, e.Value(), e.cursor})
			})
			if err := e.SetSuggestion("producer", before, tc.offer); err != nil {
				t.Fatal(err)
			}
			if len(got) != 0 || e.Value() != initial || e.Modified() {
				t.Fatalf("showing the suggestion edited the file or reported a use: %q, %+v", e.Value(), got)
			}
			e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
			want := []testGhostUse{{"producer", tc.want, tc.wantText, tc.wantCaret}}
			if e.Value() != tc.wantText || e.cursor != tc.wantCaret || e.Modified() != tc.wantEdited ||
				(e.Revision() != before) != tc.wantEdited {
				t.Fatalf("Tab left text %q, caret %+v, modified=%t, revision=%d (was %d)", e.Value(), e.cursor, e.Modified(), e.Revision(), before)
			}
			if !slices.Equal(got, want) {
				t.Fatalf("uses = %+v, want %+v", got, want)
			}
			if remaining, shown := e.GhostSuggestion(); shown {
				t.Fatalf("Tab left the accepted suggestion visible: %+v", remaining)
			}
		})
	}
}

func TestGhostOutcomePartialAcceptKeepsTheSameProducersRemainder(t *testing.T) {
	const start, owner = 5, "aeron"
	for _, tc := range []struct {
		name       string
		key        tea.KeyPressMsg
		taken      string
		rest       string
		afterCaret Position
	}{
		{"Ctrl+Right", tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, "alpha", " + beta\nsecond()", Position{Line: 0, Col: 10}},
		{"Alt+End", tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModAlt}, "alpha + beta\n", "second()", Position{Line: 1, Col: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newInlayTestEditor(t, "call()")
			e.SetCursorPosition(0, start)
			var outcomes []testGhostUse
			e.SetOnGhostOutcome(func(owner string, outcome GhostOutcome) {
				outcomes = append(outcomes, testGhostUse{owner, outcome, e.Value(), e.cursor})
			})
			if err := e.SetSuggestion(owner, e.Revision(), Suggestion{Start: start, End: start, Text: tc.taken + tc.rest}); err != nil {
				t.Fatal(err)
			}
			e.HandleEvent(tc.key)
			partText := "call(" + tc.taken + ")"
			if e.Value() != partText || e.cursor != tc.afterCaret {
				t.Fatalf("partial accept left text %q and caret %+v; want %q and %+v", e.Value(), e.cursor, partText, tc.afterCaret)
			}
			at := start + len(tc.taken)
			wantRest := Suggestion{Start: at, End: at, Text: tc.rest}
			if rest, shown := e.GhostSuggestion(); !shown || rest != wantRest || e.GhostOwner() != owner {
				t.Fatalf("remaining suggestion = %+v, shown=%t, owner=%q; want %+v from %q", rest, shown, e.GhostOwner(), wantRest, owner)
			}
			want := []testGhostUse{{owner, GhostAcceptedPart, partText, tc.afterCaret}}
			if !slices.Equal(outcomes, want) {
				t.Fatalf("partial uses = %+v, want %+v", outcomes, want)
			}
			e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
			fullText := "call(" + tc.taken + tc.rest + ")"
			if at, end, ok := e.SourceByteRange(); !ok || at != len(fullText)-1 || at != end || e.Value() != fullText {
				t.Fatalf("Tab did not insert the exact remainder: text=%q, caret bytes=[%d,%d]", e.Value(), at, end)
			}
			want = append(want, testGhostUse{owner, GhostAccepted, fullText, e.cursor})
			if !slices.Equal(outcomes, want) {
				t.Fatalf("uses after Tab = %+v, want %+v", outcomes, want)
			}
		})
	}
}

func TestGhostOutcomeTypedThroughAfterTheLastMatchingKey(t *testing.T) {
	const initial, text, owner = "value = ", "café", "producer"
	e := newInlayTestEditor(t, initial)
	e.SetCursorPosition(0, len(initial))
	var outcomes []testGhostUse
	e.SetOnGhostOutcome(func(owner string, outcome GhostOutcome) {
		outcomes = append(outcomes, testGhostUse{owner, outcome, e.Value(), e.cursor})
	})
	if err := e.SetGhostTextFor(owner, e.Revision(), len(initial), text); err != nil {
		t.Fatal(err)
	}
	for offset, r := range text {
		e.HandleEvent(tea.KeyPressMsg{Code: r, Text: string(r)})
		prefixEnd := offset + utf8.RuneLen(r)
		if e.Value() != initial+text[:prefixEnd] {
			t.Fatalf("typing %q left %q", text[:prefixEnd], e.Value())
		}
		if prefixEnd < len(text) {
			at := len(initial) + prefixEnd
			if rest, shown := e.GhostSuggestion(); !shown || rest != (Suggestion{Start: at, End: at, Text: text[prefixEnd:]}) ||
				e.GhostOwner() != owner || len(outcomes) != 0 {
				t.Fatalf("before the last key: rest=%+v shown=%t owner=%q uses=%+v", rest, shown, e.GhostOwner(), outcomes)
			}
		}
	}
	want := []testGhostUse{{owner, GhostTypedThrough, initial + text, Position{Line: 0, Col: len(initial) + 4}}}
	if !slices.Equal(outcomes, want) {
		t.Fatalf("uses = %+v, want %+v", outcomes, want)
	}
	if rest, shown := e.GhostSuggestion(); shown {
		t.Fatalf("typed-through suggestion is still shown: %+v", rest)
	}
}

func TestGhostOutcomeOwnerlessSuggestionEditsWithoutNotifying(t *testing.T) {
	e := newInlayTestEditor(t, "call()")
	e.SetCursorPosition(0, 5)
	e.SetOnGhostOutcome(func(owner string, outcome GhostOutcome) {
		t.Errorf("ownerless suggestion notified with owner %q and outcome %v", owner, outcome)
	})
	if err := e.SetGhostText(e.Revision(), 5, "value"); err != nil {
		t.Fatal(err)
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "call(value)" || !e.Modified() {
		t.Fatalf("ownerless suggestion was not accepted: %q", e.Value())
	}
}
