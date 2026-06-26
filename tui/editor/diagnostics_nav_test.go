package editor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestEditorWithDiagnostics(t *testing.T, content string, diags []LSPDiagnostic) *Editor {
	t.Helper()
	ed := editorWithTheme()
	if content != "" {
		if _, err := ed.buf.Insert(Position{}, []byte(content)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	ed.SetDiagnostics(diags)
	return ed
}

func TestDiagnostics_ReturnsCopy(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "x\n", []LSPDiagnostic{
		{Line: 0, Col: 0, Severity: DiagError, Message: "e1"},
	})
	got := ed.Diagnostics()
	assert.Len(t, got, 1)
	got[0].Message = "mutated"
	// Original must be unchanged — we got a copy.
	assert.Equal(t, "e1", ed.Diagnostics()[0].Message)
}

func TestFirstError_SkipsWarnings(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "abc\ndef\n", []LSPDiagnostic{
		{Line: 0, Col: 0, Severity: DiagWarning, Message: "w"},
		{Line: 1, Col: 2, Severity: DiagError, Message: "e"},
	})
	got := ed.FirstError()
	if assert.NotNil(t, got) {
		assert.Equal(t, "e", got.Message)
		assert.Equal(t, 1, got.Line)
	}
}

func TestFirstError_Empty(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "x", []LSPDiagnostic{
		{Line: 0, Severity: DiagWarning},
	})
	assert.Nil(t, ed.FirstError())
}

func TestNextError_FindsFollowing(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "a\nb\nc\n", []LSPDiagnostic{
		{Line: 0, Col: 0, Severity: DiagError, Message: "e0"},
		{Line: 2, Col: 0, Severity: DiagError, Message: "e2"},
	})
	d := ed.NextError(0, 0)
	if assert.NotNil(t, d) {
		assert.Equal(t, "e2", d.Message)
	}
}

func TestNextError_NoneAfterCursor(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "a\nb\n", []LSPDiagnostic{
		{Line: 0, Severity: DiagError, Message: "e0"},
	})
	assert.Nil(t, ed.NextError(99, 0))
}

func TestPrevError_FindsPreceding(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "a\nb\nc\n", []LSPDiagnostic{
		{Line: 0, Col: 0, Severity: DiagError, Message: "e0"},
		{Line: 2, Col: 0, Severity: DiagError, Message: "e2"},
	})
	d := ed.PrevError(2, 0)
	if assert.NotNil(t, d) {
		assert.Equal(t, "e0", d.Message)
	}
}

func TestPrevError_NoneBeforeCursor(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "a\n", []LSPDiagnostic{
		{Line: 0, Severity: DiagError},
	})
	assert.Nil(t, ed.PrevError(0, 0))
}

func TestNextError_SkipsWarnings(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "a\nb\nc\n", []LSPDiagnostic{
		{Line: 1, Col: 0, Severity: DiagWarning, Message: "w1"},
		{Line: 2, Col: 0, Severity: DiagError, Message: "e2"},
	})
	d := ed.NextError(0, 0)
	if assert.NotNil(t, d) {
		assert.Equal(t, "e2", d.Message)
	}
}

func TestSetSelection_BackwardsRangeNormalized(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "hello world\n", nil)
	ed.SetSelection(0, 11, 0, 6) // backwards: 11 → 6 normalizes to 6 → 11
	sLine, sCol, eLine, eCol := ed.SelectionRange()
	assert.Equal(t, 0, sLine)
	assert.Equal(t, 6, sCol)
	assert.Equal(t, 0, eLine)
	assert.Equal(t, 11, eCol)
	assert.True(t, ed.HasSelection())
}

func TestSetSelection_NegativeStartClampedToZero(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "hi\n", nil)
	ed.SetSelection(-5, -5, 0, 1)
	sLine, sCol, _, _ := ed.SelectionRange()
	assert.Equal(t, 0, sLine)
	assert.Equal(t, 0, sCol)
}

func TestSetSelection_EndBeforeStartGetsSwapped(t *testing.T) {
	ed := newTestEditorWithDiagnostics(t, "abc\n", nil)
	ed.SetSelection(0, 2, 0, 0) // end before start on same line
	sLine, sCol, eLine, eCol := ed.SelectionRange()
	// Backwards single-line ranges are normalized by swapping
	// anchor and end, so we get a 0..2 selection.
	assert.Equal(t, 0, sLine)
	assert.Equal(t, 0, sCol)
	assert.Equal(t, 0, eLine)
	assert.Equal(t, 2, eCol)
}
