package lspedit

import (
	"strings"
	"testing"

	"crude/document"
	"crude/types"
	"github.com/stretchr/testify/require"
)

func TestOffsetStrictSourceBytes(t *testing.T) {
	source := "\t界é😀abc\r\nab\t界é😀XYZ\r\n"
	for _, tc := range []struct{ line, col, offset int }{
		{0, 0, 0}, {0, 1, 1}, {0, 3, 5}, {0, 4, 7},
		{0, 6, 11}, {0, 9, 14}, {1, 0, 16}, {1, 9, 30}, {2, 0, 34},
	} {
		actual, err := Offset(source, tc.line, tc.col)
		require.NoError(t, err, "%+v", tc)
		require.Equal(t, tc.offset, actual, "%+v", tc)
	}
	for _, tc := range []struct{ line, col int }{
		{-1, 0}, {0, -1}, {0, 5}, {0, 10}, {1, 7}, {1, 12}, {2, 1}, {3, 0},
	} {
		_, err := Offset(source, tc.line, tc.col)
		require.Error(t, err, "%+v", tc)
	}
	_, err := Offset("\xff", 0, 0)
	require.Error(t, err)
	_, err = Offset(strings.Repeat("a", document.MaxBytes+1), 0, 0)
	require.Error(t, err)
	// A lone CR without LF is source content, as in the original primitive.
	offset, err := Offset("x\r", 0, 2)
	require.NoError(t, err)
	require.Equal(t, 2, offset)
}

func TestApplyLiteralUnicodeCRLFAndImmutableCoordinates(t *testing.T) {
	before := "package main\r\n\t😀日本.pr\r\néx"
	edits := []types.LSPTextEdit{
		{Line: 2, Col: 1, EndLine: 2, EndCol: 2, NewText: "!"},
		{Line: 1, Col: 6, EndLine: 1, EndCol: 8, NewText: "print(\"界😀\")"},
		{Line: 1, EndLine: 1, NewText: "import \"fmt\"\r\n"},
	}
	original := append([]types.LSPTextEdit(nil), edits...)
	after, err := Apply(before, edits)
	require.NoError(t, err)
	require.Equal(t, "package main\r\nimport \"fmt\"\r\n\t😀日本.print(\"界😀\")\r\ne!x", after)
	require.Equal(t, original, edits)
	after, err = Apply("ab\r\n界😀xy", []types.LSPTextEdit{
		{Col: 1, EndLine: 1, EndCol: 3, NewText: "é\n"},
	})
	require.NoError(t, err)
	require.Equal(t, "aé\nxy", after)
}

func TestApplyRejectsWholeUnsafeVector(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit types.LSPTextEdit
	}{
		{"negative line", types.LSPTextEdit{Line: -1}},
		{"negative col", types.LSPTextEdit{Col: -1}},
		{"missing line", types.LSPTextEdit{Line: 9, EndLine: 9}},
		{"past CRLF", types.LSPTextEdit{Col: 5, EndCol: 5}},
		{"surrogate", types.LSPTextEdit{Col: 1, EndCol: 2}},
		{"reversed", types.LSPTextEdit{Line: 1, Col: 2, EndLine: 1, EndCol: 1}},
		{"overlap", types.LSPTextEdit{Line: 1, Col: 1, EndLine: 1, EndCol: 2}},
		{"insert inside", types.LSPTextEdit{Line: 1, Col: 1, EndLine: 1, EndCol: 1}},
		{"same start", types.LSPTextEdit{Line: 1, EndLine: 1, NewText: "x"}},
		{"bad replacement", types.LSPTextEdit{NewText: "\xff"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Apply("😀ab\r\npr", []types.LSPTextEdit{
				{Line: 1, EndLine: 1, EndCol: 2, NewText: "print"}, tc.edit,
			})
			require.Error(t, err)
			require.Empty(t, result)
		})
	}
	_, err := Apply("a", []types.LSPTextEdit{{NewText: "x"}, {NewText: "y"}})
	require.Error(t, err)
	_, err = Apply("\xff", nil)
	require.Error(t, err)
	_, err = Apply("", make([]types.LSPTextEdit, document.MaxEdits+1))
	require.Error(t, err)
	_, err = Apply(strings.Repeat("a", document.MaxBytes+1), nil)
	require.Error(t, err)
	_, err = Apply("", []types.LSPTextEdit{{NewText: strings.Repeat("a", document.MaxBytes+1)}})
	require.Error(t, err)
	_, err = Apply("a", []types.LSPTextEdit{{NewText: strings.Repeat("x", document.MaxBytes)}})
	require.Error(t, err)
}

func TestApplyBoundsAndTouchingRanges(t *testing.T) {
	text := strings.Repeat("a", document.MaxBytes)
	result, err := Apply(text, nil)
	require.NoError(t, err)
	require.Equal(t, text, result)
	result, err = Apply("abc", []types.LSPTextEdit{
		{Col: 1, EndCol: 2, NewText: "X"}, {Col: 2, EndCol: 3, NewText: "Y"},
	})
	require.NoError(t, err)
	require.Equal(t, "aXY", result)
	// Result bounds apply to the final source, not a transient edit order.
	result, err = Apply(text, []types.LSPTextEdit{
		{EndCol: document.MaxBytes - 1}, {Col: document.MaxBytes, EndCol: document.MaxBytes, NewText: "Z"},
	})
	require.NoError(t, err)
	require.Equal(t, "aZ", result)
	edits := make([]types.LSPTextEdit, document.MaxEdits)
	for i := range edits {
		edits[i] = types.LSPTextEdit{Col: i, EndCol: i + 1, NewText: "x"}
	}
	result, err = Apply(strings.Repeat("a", document.MaxEdits), edits)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("x", document.MaxEdits), result)
}
