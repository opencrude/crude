package editor

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/snippet"
	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"github.com/stretchr/testify/require"
)

func numericCompletion(t testing.TB, source string) types.CompletionItem {
	t.Helper()
	text, plan, err := snippet.Parse(source)
	require.NoError(t, err)
	return types.CompletionItem{Label: "fixture", InsertText: text, Snippet: plan}
}

// These are actual source mutation and editor-render stages, not just passive
// planner numbers. Dense unrelated source and many mirrors must remain visible
// in the evidence instead of being hidden by a parser-only microbenchmark.
func BenchmarkSnippetTypingDense(b *testing.B) {
	for _, lines := range []int{200, 20_000} {
		for _, mirrors := range []int{1, 8, 64} {
			for _, paint := range []bool{false, true} {
				b.Run(fmt.Sprintf("lines%d/mirrors%d/paint%t", lines, mirrors, paint), func(b *testing.B) {
					e := NewPreview(theme.DefaultDark(), "snippet.txt", "\n"+strings.Repeat("unchanged source\n", lines), 1)
					b.Cleanup(e.Close)
					e.readOnly = false
					item := numericCompletion(b, "${1:a}"+strings.Repeat(" $1", mirrors-1)+"$0")
					if err := e.ApplyCompletion(item); err != nil {
						b.Fatal(err)
					}
					e.ClearSelection()
					e.SetCursorPosition(0, 1)
					surf := surface.NewSurface("snippet", 120, 40, cell.TransparentBlank())
					e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true})
					b.ReportAllocs()
					b.ResetTimer()
					insert := true
					for b.Loop() {
						key := tea.KeyPressMsg{Code: tea.KeyBackspace}
						if insert {
							key = tea.KeyPressMsg{Code: 'x', Text: "x"}
						}
						e.HandleEvent(key)
						if paint {
							e.Render(surf, ui.PaintContext{Theme: e.th, Focused: true})
						}
						if !e.SnippetActive() {
							b.Fatal("benchmark lost its real snippet workload")
						}
						insert = !insert
					}
				})
			}
		}
	}
}

func TestSnippetCanonicalSparseGroupsOwnAckAndAtomicPeerPublication(t *testing.T) {
	e, initial := projectionEditor(t, "prefix pr suffix")
	e.SetCursorPosition(0, 9)
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${1:word}:$1$0")))
	require.Equal(t, "prefix word:word suffix", e.Value())
	require.True(t, e.SnippetActive())
	completion, err := e.CaptureDocumentEdits(projectionOwner, "completion", "")
	require.NoError(t, err)
	require.NotEmpty(t, completion.GroupID)
	require.Equal(t, []document.Edit{{Start: 7, End: 9, Text: "word:word"}}, completion.Edits,
		"completion cannot replace the whole shared document")
	require.Empty(t, e.changes, "connected views cannot retain a second undo authority")
	require.NoError(t, e.AcceptDocumentReceipt(document.Receipt{
		DocumentID: initial.ID, RequestID: "completion", Before: 1, Revision: 2, Accepted: true,
	}))
	require.True(t, e.SnippetActive(), "an exact own receipt is not a peer/reset edit")

	state := initial
	state.Text, state.Saved, state.Revision = nil, nil, 2
	plan, err := e.PrepareDocumentUpdate(projectionOwner, 1, []document.Change{completion}, state, nil, false)
	require.NoError(t, err)
	require.NoError(t, e.CommitDocumentUpdate(plan))
	require.True(t, e.SnippetActive(), "ordered own ACK must not replay or retire the field")

	peer := document.Change{
		DocumentID: initial.ID, BaseRevision: 2,
		Owner: document.Owner{ClientID: "peer", Epoch: 1}, RequestID: "outside",
		Edits: []document.Edit{{Start: 0, End: 0, Text: "X"}, {Start: 18, End: 18, Text: "Y"}},
	}
	state.Revision = 3
	before := e.captureViewState()
	plan, err = e.PrepareDocumentUpdate(projectionOwner, 2, []document.Change{peer}, state, nil, false)
	require.NoError(t, err)
	require.Equal(t, before, e.captureViewState(), "preparation must not move view or snippet anchors")
	require.Equal(t, "prefix word:word suffix", e.Value())
	require.NoError(t, e.CommitDocumentUpdate(plan))
	require.True(t, e.SnippetActive(), "strictly disjoint peer edits retain private navigation")
	require.Equal(t, "Xprefix word:word sYuffix", e.Value())
	require.Equal(t, "word", e.SelectedText())
	snippetKey(e, 'z', "z", 0)
	require.Equal(t, "Xprefix z:z sYuffix", e.Value())
	typing, err := e.CaptureDocumentEdits(projectionOwner, "typing", "")
	require.NoError(t, err)
	require.NotEqual(t, completion.GroupID, typing.GroupID)
	require.Equal(t, []document.Edit{{Start: 8, End: 12, Text: "z"}, {Start: 13, End: 17, Text: "z"}}, typing.Edits)
}

func TestSnippetFailedPeerPreparationRetainsExactInteractionUntilCommit(t *testing.T) {
	e, initial := projectionEditor(t, "")
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${1:word}:$1$0")))
	c, err := e.CaptureDocumentEdits(projectionOwner, "create", "")
	require.NoError(t, err)
	require.NoError(t, e.AcceptDocumentReceipt(document.Receipt{
		DocumentID: initial.ID, RequestID: "create", Before: 1, Revision: 2, Accepted: true,
	}))
	peer := document.Change{DocumentID: initial.ID, BaseRevision: 2, Edits: []document.Edit{{Start: 0, End: 0, Text: "peer"}}}
	state := initial
	state.Text, state.Saved, state.Revision = nil, nil, 4
	old := e.snippet
	broken := document.Change{DocumentID: initial.ID, BaseRevision: 99}
	_, err = e.PrepareDocumentUpdate(projectionOwner, 2, []document.Change{peer, broken}, state, nil, false)
	require.Error(t, err)
	require.Same(t, old, e.snippet)
	require.Equal(t, "word:word", e.Value())
	require.Equal(t, "word", e.SelectedText())
	state.Revision = 3
	plan, err := e.PrepareDocumentUpdate(projectionOwner, 2, []document.Change{peer}, state, nil, false)
	require.NoError(t, err)
	require.Same(t, old, e.snippet, "preparing retirement cannot mutate interaction")
	require.NoError(t, e.CommitDocumentUpdate(plan))
	require.False(t, e.SnippetActive())
	require.Equal(t, "peerword:word", e.Value(), "peer input must never initiate mirrors")
	require.Empty(t, e.documentProjection.pending)
	require.NotEmpty(t, c.GroupID)
}

func snippetKey(e *Editor, code rune, text string, mod tea.KeyMod) {
	e.HandleEvent(tea.KeyPressMsg{Code: code, Text: text, Mod: mod})
}

func TestSnippetCompletionTypingMirrorsTabAndHistory(t *testing.T) {
	e := opsEditor(t, "source.go", "pr")
	e.SetCursorPosition(0, 2)
	callbacks := 0
	e.SetOnContentChanged(func(_, text string) {
		callbacks++
		require.Equal(t, e.Value(), text)
	})
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "print(${1:value}, ${2|red,blue|}, $1)$0")))
	require.Equal(t, "print(value, red, value)", e.Value())
	require.Equal(t, "value", e.SelectedText())
	require.True(t, e.SnippetActive())
	require.Equal(t, 1, callbacks)

	snippetKey(e, 'x', "x", 0)
	require.Equal(t, "print(x, red, x)", e.Value())
	require.Equal(t, Position{Col: 7}, e.cursor)
	require.Equal(t, 2, callbacks, "mirrors must not publish an intermediate source")
	e.Undo()
	require.Equal(t, "print(value, red, value)", e.Value())
	require.False(t, e.SnippetActive(), "history restores bytes, not a stale interaction")
	e.Redo()
	require.Equal(t, "print(x, red, x)", e.Value())
	e.Undo()
	e.Undo()
	require.Equal(t, "pr", e.Value(), "completion itself remains a single transaction")
}

func TestSnippetNumericOrderBacktabFinalAndOrdinaryTab(t *testing.T) {
	e := opsEditor(t, "source.go", "")
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${3:last} ${1:first} ${2:middle}$0")))
	require.Equal(t, "first", e.SelectedText())
	before := e.Value()
	snippetKey(e, tea.KeyTab, "", tea.ModShift)
	require.Equal(t, "first", e.SelectedText())
	require.Equal(t, before, e.Value())
	snippetKey(e, tea.KeyTab, "", 0)
	require.Equal(t, "middle", e.SelectedText())
	snippetKey(e, tea.KeyTab, "", 0)
	require.Equal(t, "last", e.SelectedText())
	snippetKey(e, tea.KeyTab, "", tea.ModShift)
	require.Equal(t, "middle", e.SelectedText())
	snippetKey(e, tea.KeyTab, "", 0)
	snippetKey(e, tea.KeyTab, "", 0)
	require.False(t, e.SnippetActive())
	require.False(t, e.HasSelection())
	require.Equal(t, Position{Col: 17}, e.cursor)
	require.Equal(t, before, e.Value())
	snippetKey(e, tea.KeyTab, "", 0)
	require.Equal(t, "last first middle    ", e.Value())
}

func TestSnippetNestedTypingOuterReplacementAndPairDefaults(t *testing.T) {
	e := opsEditor(t, "source.go", "")
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${1:outer ${2:inner}} / $1 / $2$0")))
	snippetKey(e, tea.KeyTab, "", 0)
	require.Equal(t, "inner", e.SelectedText())
	e.HandleEvent(tea.PasteMsg{Content: "value"})
	require.Equal(t, "outer value / outer value / value", e.Value())
	snippetKey(e, tea.KeyTab, "", tea.ModShift)
	require.Equal(t, "outer value", e.SelectedText())
	snippetKey(e, '(', "(", 0)
	require.Equal(t, "() / () / value", e.Value(), "default is replaced, not wrapped")
	snippetKey(e, tea.KeyTab, "", 0)
	require.Equal(t, "value", e.SelectedText(), "independent occurrence remains after overwritten descendants")
}

func TestSnippetChoiceTokensBindEditorInteractionSourceAndSelection(t *testing.T) {
	e := opsEditor(t, "source.go", "")
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${1|red,blue,green|}:$1$0")))
	token, ok := e.SnippetChoiceOptions()
	require.True(t, ok)
	token.Choices[0] = "evil"
	require.Error(t, e.ApplySnippetChoice(token, "evil"))
	require.Equal(t, "red:red", e.Value())
	require.NoError(t, e.ApplySnippetChoice(token, "blue"))
	require.Equal(t, "blue:blue", e.Value())
	require.Error(t, e.ApplySnippetChoice(token, "green"))
	current, ok := e.SnippetChoiceOptions()
	require.True(t, ok)
	other := opsEditor(t, "other.go", "")
	require.NoError(t, other.ApplyCompletion(numericCompletion(t, "${1|red,blue,green|}:$1$0")))
	require.Error(t, other.ApplySnippetChoice(current, "green"))
	e.CancelSnippet()
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${1|red,blue,green|}:$1$0")))
	require.Error(t, e.ApplySnippetChoice(current, "green"), "same-text replacement cannot revive old choices")
}

func TestSnippetEmptyNoopEscAndRawObserverRetirement(t *testing.T) {
	for _, source := range []string{"$1$0", "${1:x}$0"} {
		t.Run(source, func(t *testing.T) {
			initial := ""
			if source == "${1:x}$0" {
				initial = "x"
			}
			e := opsEditor(t, "source.go", initial)
			item := numericCompletion(t, source)
			item.TextEdit = &types.LSPTextEdit{EndCol: len(initial), NewText: item.InsertText}
			require.NoError(t, e.ApplyCompletion(item))
			require.True(t, e.SnippetActive(), "byte-identical insertions still navigate")
			require.Empty(t, e.changes)
			snippetKey(e, tea.KeyEsc, "", 0)
			require.False(t, e.SnippetActive())
			require.False(t, e.HasSelection())
			require.Equal(t, initial, e.Value())
		})
	}
	e := opsEditor(t, "source.go", "")
	require.NoError(t, e.ApplyCompletion(numericCompletion(t, "${1:x}:$1$0")))
	_, err := e.buf.insertAtOffset(0, []byte("foreign"))
	require.NoError(t, err)
	require.False(t, e.SnippetActive(), "unowned/raw mutations cannot create mirrors")
	require.Equal(t, "foreignx:x", e.Value())
	require.Nil(t, e.buf.mutationObserver, "a standalone snippet must not replace the canonical observer slot")
}

func TestSnippetAdditionalEditsPreserveSparseCanonicalOffsetsAndUndo(t *testing.T) {
	e := opsEditor(t, "source.go", "package main\n\npr\nsuffix")
	item := numericCompletion(t, "print(${1:α😀}, $1)$0")
	item.TextEdit = &types.LSPTextEdit{Line: 2, EndLine: 2, EndCol: 2, NewText: item.InsertText}
	item.AdditionalTextEdits = []types.LSPTextEdit{{Line: 1, EndLine: 1, NewText: "import \"fmt\"\n"}}
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, "package main\nimport \"fmt\"\n\nprint(α😀, α😀)\nsuffix", e.Value())
	require.Equal(t, "α😀", e.SelectedText())
	e.HandleEvent(tea.PasteMsg{Content: "界\r\nZ"})
	require.Equal(t, "package main\nimport \"fmt\"\n\nprint(界\r\nZ, 界\r\nZ)\nsuffix", e.Value())
	e.Undo()
	e.Undo()
	require.Equal(t, "package main\n\npr\nsuffix", e.Value())
}

func TestSnippetRejectsMisboundPlansAndJoinedGraphemeEndpointsAtomically(t *testing.T) {
	for _, item := range []types.CompletionItem{
		func() types.CompletionItem {
			v := numericCompletion(t, "${1:x}$0")
			v.InsertText = "${1:x}$0"
			return v
		}(),
		numericCompletion(t, "${1:e}\u0301$0"),
	} {
		e := opsEditor(t, "source.go", "before ")
		e.SetCursorPosition(0, 7)
		before, revision := e.Value(), e.Revision()
		cursor := e.cursor
		require.Error(t, e.ApplyCompletion(item))
		require.Equal(t, before, e.Value())
		require.Equal(t, revision, e.Revision())
		require.Equal(t, cursor, e.cursor)
		require.Empty(t, e.changes)
		require.False(t, e.SnippetActive())
	}
}
