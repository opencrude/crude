package editor

import (
	"testing"

	model "crude/types"
	"github.com/stretchr/testify/require"
)

func TestLSPCompletionEditUnicodeImportAtomic(t *testing.T) {
	before := "package main\n\t😀日本.pr\n"
	e := opsEditor(t, "main.go", before)
	e.SetCursorPosition(1, 13)
	line, col := e.LSPCursorPosition()
	require.Equal(t, 1, line)
	require.Equal(t, 8, col)
	callbacks := 0
	e.SetOnContentChanged(func(_, text string) {
		callbacks++
		require.Equal(t, e.Value(), text)
	})
	item := model.CompletionItem{
		TextEdit:            &model.LSPTextEdit{Line: 1, Col: 6, EndLine: 1, EndCol: 8, NewText: "print(\"界😀\")"},
		AdditionalTextEdits: []model.LSPTextEdit{{Line: 1, EndLine: 1, NewText: "import \"fmt\"\n"}},
	}
	require.NoError(t, e.ApplyCompletion(item))
	after := "package main\nimport \"fmt\"\n\t😀日本.print(\"界😀\")\n"
	require.Equal(t, after, e.Value())
	require.Equal(t, Position{Line: 2, Col: 24}, e.cursor)
	require.Equal(t, 1, callbacks)
	e.Undo()
	require.Equal(t, before, e.Value())
	require.Equal(t, Position{Line: 1, Col: 13}, e.cursor)
	require.Equal(t, 2, callbacks)
	e.Redo()
	require.Equal(t, after, e.Value())
	require.Equal(t, 3, callbacks)
}

func TestLSPCompletionEditMultilineAndFollowingEdit(t *testing.T) {
	e := opsEditor(t, "main.go", "abc\ndef\nxyz")
	require.NoError(t, e.ApplyCompletion(model.CompletionItem{
		TextEdit:            &model.LSPTextEdit{Line: 0, Col: 1, EndLine: 1, EndCol: 2, NewText: "界\n\t😀"},
		AdditionalTextEdits: []model.LSPTextEdit{{Line: 2, Col: 1, EndLine: 2, EndCol: 3, NewText: "!"}},
	}))
	require.Equal(t, "a界\n\t😀f\nx!", e.Value())
	require.Equal(t, Position{Line: 1, Col: 6}, e.cursor)
	e.Undo()
	require.Equal(t, "abc\ndef\nxyz", e.Value())
	e.Redo()
	require.Equal(t, "a界\n\t😀f\nx!", e.Value())
}

func TestLSPCompletionPrefixUsesEditorIdentifierSemantics(t *testing.T) {
	for _, tt := range []struct {
		before string
		col    int
		item   model.CompletionItem
		want   string
	}{
		{"obj.pr", 6, model.CompletionItem{InsertText: "print"}, "obj.print"},
		{"\t😀.日_2suffix", 11, model.CompletionItem{Label: "日本"}, "\t😀.日本suffix"},
		{"obj.", 4, model.CompletionItem{InsertText: "print"}, "obj.print"},
	} {
		t.Run(tt.before, func(t *testing.T) {
			e := opsEditor(t, "main.go", tt.before)
			e.SetCursorPosition(0, tt.col)
			require.NoError(t, e.ApplyCompletion(tt.item))
			require.Equal(t, tt.want, e.Value())
		})
	}
}

func TestLSPCompletionEditRejectsUnsafeWithoutEffects(t *testing.T) {
	valid := model.LSPTextEdit{Line: 1, Col: 0, EndLine: 1, EndCol: 2, NewText: "print"}
	for _, tt := range []struct {
		name string
		edit model.LSPTextEdit
	}{
		{"negative line", model.LSPTextEdit{Line: -1}},
		{"negative column", model.LSPTextEdit{Col: -1}},
		{"missing line", model.LSPTextEdit{Line: 3, EndLine: 3}},
		{"past line end", model.LSPTextEdit{Col: 5, EndCol: 5}},
		{"surrogate midpoint", model.LSPTextEdit{Col: 1, EndCol: 2}},
		{"reversed range", model.LSPTextEdit{Line: 1, Col: 2, EndLine: 1, EndCol: 1}},
		{"overlapping replacement", model.LSPTextEdit{Line: 1, Col: 1, EndLine: 1, EndCol: 2}},
		{"insert in replacement", model.LSPTextEdit{Line: 1, Col: 1, EndLine: 1, EndCol: 1}},
		{"same start insert", model.LSPTextEdit{Line: 1, EndLine: 1, NewText: "import"}},
		{"invalid UTF8", model.LSPTextEdit{NewText: string([]byte{0xff})}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := opsEditor(t, "main.go", "😀ab\npr")
			e.SetCursorPosition(1, 2)
			selectRange(e, 1, 0, 1, 2)
			callbacks := 0
			e.SetOnContentChanged(func(_, _ string) { callbacks++ })
			revision := e.buf.Revision()
			err := e.ApplyCompletion(model.CompletionItem{TextEdit: &valid, AdditionalTextEdits: []model.LSPTextEdit{tt.edit}})
			require.Error(t, err)
			require.Equal(t, "😀ab\npr", e.Value())
			require.Equal(t, revision, e.buf.Revision())
			require.Equal(t, Position{Line: 1, Col: 2}, e.cursor)
			require.True(t, e.selecting)
			require.Empty(t, e.changes)
			require.Zero(t, callbacks)
		})
	}
	t.Run("same position inserts", func(t *testing.T) {
		e := opsEditor(t, "main.go", "pr")
		insert := model.LSPTextEdit{NewText: "x"}
		require.Error(t, e.ApplyCompletion(model.CompletionItem{TextEdit: &insert, AdditionalTextEdits: []model.LSPTextEdit{insert}}))
		require.Equal(t, "pr", e.Value())
	})
}

func TestLSPCompletionEditNoopAndReadonly(t *testing.T) {
	e := opsEditor(t, "main.go", "print")
	e.SetCursorPosition(0, 2)
	callbacks := 0
	e.SetOnContentChanged(func(_, _ string) { callbacks++ })
	item := model.CompletionItem{TextEdit: &model.LSPTextEdit{EndCol: 5, NewText: "print"}}
	require.NoError(t, e.ApplyCompletion(item))
	require.Equal(t, Position{Col: 2}, e.cursor)
	require.Empty(t, e.changes)
	require.Zero(t, callbacks)
	require.Error(t, e.ApplyCompletion(model.CompletionItem{}))
	require.Equal(t, "print", e.Value())
	e.readOnly = true
	require.Error(t, e.ApplyCompletion(model.CompletionItem{InsertText: "other"}))
	require.Equal(t, "print", e.Value())
	require.Zero(t, callbacks)
}

func TestLSPCursorEditCoordinatesUseActualTabWidth(t *testing.T) {
	e := opsEditor(t, "main.go", "\t😀日本\n")
	require.NoError(t, e.SetLSPCursorPosition(0, 5))
	require.Equal(t, Position{Col: 10}, e.cursor)
	line, col := e.LSPCursorPosition()
	require.Equal(t, 0, line)
	require.Equal(t, 5, col)
	require.Error(t, e.SetLSPCursorPosition(0, 2))
	require.Equal(t, Position{Col: 10}, e.cursor)
	require.NoError(t, e.SetLSPCursorPosition(1, 0))
	require.Equal(t, Position{Line: 1}, e.cursor)
}

func TestLSPCompletionEditCRLFAndCombiningBoundary(t *testing.T) {
	e := opsEditor(t, "main.go", "a\r\néx")
	require.Error(t, e.ApplyCompletion(model.CompletionItem{TextEdit: &model.LSPTextEdit{Col: 2, EndCol: 2, NewText: "!"}}))
	require.NoError(t, e.ApplyCompletion(model.CompletionItem{TextEdit: &model.LSPTextEdit{Line: 1, Col: 1, EndLine: 1, EndCol: 2, NewText: "!"}}))
	require.Equal(t, "a\r\ne!x", e.Value())
	e.Undo()
	require.Equal(t, "a\r\néx", e.Value())
}

func TestLSPDiagnosticNavigationValidatesBothSourceLinesAtomically(t *testing.T) {
	e := opsEditor(t, "unicode.txt", "\t界é😀abc\r\nab\t界é😀XYZ\r\n")
	valid := LSPDiagnostic{Line: 0, Col: 6, EndLine: 1, EndCol: 9}
	require.NoError(t, e.SetLSPDiagnosticPosition(valid))
	require.Equal(t, Position{Col: 9}, e.cursor)
	require.Equal(t, "abc\r\nab\t界é😀X", e.SelectedText())
	for _, invalid := range []LSPDiagnostic{
		{Line: -1, EndLine: 1},
		{Line: 0, Col: 5, EndLine: 1, EndCol: 9},  // start splits 😀 surrogate
		{Line: 0, Col: 6, EndLine: 1, EndCol: 7},  // end splits different line's 😀
		{Line: 0, Col: 6, EndLine: 1, EndCol: 12}, // excludes CRLF from content
		{Line: 0, Col: 6, EndLine: 9},
		{Line: 1, Col: 9, EndLine: 0, EndCol: 6},
	} {
		before := e.captureViewState()
		require.Error(t, e.SetLSPDiagnosticPosition(invalid), "%+v", invalid)
		require.Equal(t, before, e.captureViewState(), "invalid end must not move start cursor")
	}
}
