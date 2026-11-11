package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/tui/theme"
)

func TestTypingNativeIntentFollowsFinalSourceAndCaret(t *testing.T) {
	for _, tc := range []struct {
		before, key, after string
		col                int
	}{
		{"join", "(", "join()", 5},
		{"pr", "i", "pri", 3},
		{"device", ".", "device.", 7},
		{"join(x", ",", "join(x,", 7},
		{"\tjoin", "(", "\tjoin()", 9},
	} {
		t.Run(tc.before+tc.key, func(t *testing.T) {
			e, _ := projectionEditor(t, tc.before)
			e.SetCursorPosition(0, LineVisualWidth(tc.before, DefaultTabWidth))
			count := 0
			e.SetOnTyping(func(tea.KeyPressMsg) {
				count++
				if e.Value() != tc.after || e.CursorLine() != 0 || e.CursorCol() != tc.col || e.undoGroupDepth != 0 {
					t.Fatalf("intent preceded the final native edit/caret/history group: %q %d:%d depth=%d", e.Value(), e.CursorLine(), e.CursorCol(), e.undoGroupDepth)
				}
			})
			e.HandleEvent(tea.KeyPressMsg{Code: rune(tc.key[0]), Text: tc.key})
			if count != 1 {
				t.Fatal("native delimiter did not emit one actual-edit intent", count)
			}
		})
	}
}

func TestNativeLineAndIndentKeysPublishFinalInputIntent(t *testing.T) {
	for _, step := range []struct {
		name      string
		key       tea.KeyPressMsg
		want      string
		line, col int
	}{
		{"new line", tea.KeyPressMsg{Code: tea.KeyEnter}, "join\n", 1, 0},
		{"indent", tea.KeyPressMsg{Code: tea.KeyTab}, "join    ", 0, 8},
	} {
		t.Run(step.name, func(t *testing.T) {
			ed, _ := projectionEditor(t, "join")
			ed.SetCursorPosition(0, 4)
			calls := 0
			ed.SetOnTyping(func(key tea.KeyPressMsg) {
				calls++
				if key.Code != step.key.Code || ed.Value() != step.want ||
					ed.CursorLine() != step.line || ed.CursorCol() != step.col || ed.undoGroupDepth != 0 {
					t.Fatal("native text-key intent preceded its final source, caret or edit group")
				}
			})
			ed.HandleEvent(step.key)
			if calls != 1 {
				t.Fatal("native text-key edit did not publish exactly one input intent", calls)
			}
		})
	}
}

func TestTypingNativeIntentDoesNotTreatOtherMutationAsTyping(t *testing.T) {
	for _, kind := range []string{"paste", "API", "readonly", "modified", "publication"} {
		t.Run(kind, func(t *testing.T) {
			e, snapshot := projectionEditor(t, "join")
			e.SetCursorPosition(0, 4)
			count := 0
			e.SetOnTyping(func(tea.KeyPressMsg) { count++ })
			switch kind {
			case "paste":
				e.HandleEvent(tea.PasteMsg{Content: "("})
			case "API":
				e.InsertRune('(')
			case "readonly":
				e.readOnly = true
				e.HandleEvent(tea.KeyPressMsg{Code: '(', Text: "("})
			case "modified":
				e.HandleEvent(tea.KeyPressMsg{Code: ',', Text: ",", Mod: tea.ModAlt})
			case "publication":
				change := document.Change{DocumentID: snapshot.ID, BaseRevision: 1, Owner: projectionOwner, RequestID: "peer",
					Edits: []document.Edit{{Start: 4, End: 4, Text: "("}}}
				snapshot.Text, snapshot.Revision = []byte("join("), 2
				if err := e.ApplyDocumentDelta(change, snapshot); err != nil {
					t.Fatal(err)
				}
			}
			if count != 0 {
				t.Fatal("non-native mutation became a parameter-help trigger", kind, count)
			}
		})
	}
	e := NewPreview(theme.DefaultDark(), "/virtual/readonly", "join", 1)
	t.Cleanup(e.Close)
	e.SetOnTyping(func(tea.KeyPressMsg) { t.Fatal("preview requested parameter help") })
	e.HandleEvent(tea.KeyPressMsg{Code: ',', Text: ","})
}

func TestTypingDeferredHistoryKeyOnlyTriggersWhenExecuted(t *testing.T) {
	e, _ := projectionEditor(t, "join")
	e.SetCursorPosition(0, 4)
	if err := e.BindDocumentOwner(projectionOwner); err != nil {
		t.Fatal(err)
	}
	var undo DocumentUndoIntent
	e.SetOnDocumentUndo(func(intent DocumentUndoIntent) { undo = intent })
	count := 0
	e.SetOnTyping(func(tea.KeyPressMsg) { count++ })
	e.Undo()
	e.HandleEvent(tea.KeyPressMsg{Code: '(', Text: "("})
	if count != 0 || e.Value() != "join" || !e.DocumentState().HistoryQueued {
		t.Fatal("queued delimiter triggered before original input execution", count, e.Value())
	}
	// A definitive refusal also resolves the ordering barrier. The callback is
	// still about the subsequent actual key, not success of the preceding Undo.
	if err := e.CompleteDocumentUndo(undo, document.ErrOverlap); err != nil {
		t.Fatal(err)
	}
	e.CompleteDocumentHistorySync()
	if count != 1 || e.Value() != "join()" || e.CursorCol() != 5 {
		t.Fatal("deferred native key lost its single final-source trigger", count, e.Value(), e.CursorCol())
	}
}

func TestTypingRecoveryAndDeferredAPIRetainTextWithoutNewUIIntent(t *testing.T) {
	for _, origin := range []string{"recovery", "API"} {
		t.Run(origin, func(t *testing.T) {
			e, _ := projectionEditor(t, "join")
			e.SetCursorPosition(0, 4)
			if err := e.BindDocumentOwner(projectionOwner); err != nil {
				t.Fatal(err)
			}
			var undo DocumentUndoIntent
			e.SetOnDocumentUndo(func(intent DocumentUndoIntent) { undo = intent })
			e.Undo()
			if origin == "API" {
				e.InsertRune('(')
			} else {
				e.HandleEvent(tea.KeyPressMsg{Code: '(', Text: "("})
				record, ok := e.ExportDocumentRecovery()
				if !ok {
					t.Fatal("queued key lost its durable recovery input")
				}
				reopened, _ := projectionEditor(t, "join")
				if err := reopened.RestoreDocumentRecovery(record); err != nil {
					t.Fatal(err)
				}
				e = reopened
				var pending bool
				undo, pending = e.PendingDocumentUndoIntent(false)
				if !pending {
					t.Fatal("recovery lost original command identity")
				}
			}
			count := 0
			e.SetOnTyping(func(tea.KeyPressMsg) { count++ })
			if err := e.CompleteDocumentUndo(undo, document.ErrOverlap); err != nil {
				t.Fatal(err)
			}
			e.CompleteDocumentHistorySync()
			if e.Value() != "join()" || e.CursorCol() != 5 || count != 0 {
				t.Fatal("noninteractive input lost text/history or invented fresh parameter intent", origin, count, e.Value())
			}
			// Suppression is scoped to that replay, never a persistent editor mode.
			e.HandleEvent(tea.KeyPressMsg{Code: ',', Text: ","})
			if count != 1 || e.Value() != "join(,)" {
				t.Fatal("replayed input disabled later genuine native help", count, e.Value())
			}
		})
	}
}
