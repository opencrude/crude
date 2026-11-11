package editor

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// A terminal sends the line breaks of a bracketed paste as CR, and the Windows
// clipboard holds CRLF. Every pasted line break must become a line of the
// document, with the document's own terminator.
func TestPasteGivesEveryLineBreakTheDocumentTerminator(t *testing.T) {
	for _, tc := range []struct {
		name, before, pasted, after string
		lines                       int
	}{
		{"terminal CR into LF", "head\ntail", "one\rtwo\rthree", "one\ntwo\nthreehead\ntail", 4},
		{"clipboard CRLF into LF", "head\ntail", "one\r\ntwo", "one\ntwohead\ntail", 3},
		{"LF into CRLF", "head\r\ntail", "one\ntwo", "one\r\ntwohead\r\ntail", 3},
		{"terminal CR into CRLF", "head\r\ntail", "one\rtwo", "one\r\ntwohead\r\ntail", 3},
		{"CRLF into CRLF", "head\r\ntail", "one\r\ntwo", "one\r\ntwohead\r\ntail", 3},
		{"mixed into LF", "", "a\r\nb\rc\nd", "a\nb\nc\nd", 4},
		{"no line break", "head", "界 x", "界 xhead", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := opsEditor(t, "sample.txt", tc.before)
			e.HandleEvent(tea.PasteMsg{Content: tc.pasted})
			require.Equal(t, tc.after, e.Value())
			require.Equal(t, tc.lines, e.LineCount())
			e.Undo()
			require.Equal(t, tc.before, e.Value(), "one undo step restores the document")
		})
	}
}

// The reported case: an object copied from a web page and pasted into an
// LF-terminated JSON file through Windows Terminal arrived on one line with
// bare CR bytes between its lines, and the saved file kept them.
func TestPasteOfCopiedJSONKeepsItsLines(t *testing.T) {
	before := "{\n  \"plugins\": [\n    \"expo-localization\",\n    \n  ]\n}\n"
	pasted := "{\r          \"supportedLocales\": {\r            \"ios\": [\"en\", \"ja\"],\r            \"android\": [\"en\", \"ja\"]\r          }\r        }"
	e := opsEditor(t, "app.json", before)
	e.SetCursorPosition(3, 4)
	e.HandleEvent(tea.PasteMsg{Content: pasted})
	got := e.Value()
	require.NotContains(t, got, "\r", "a bare CR stayed inside a line")
	require.Equal(t, strings.Count(before, "\n")+5, strings.Count(got, "\n"))
	require.Equal(t, "            \"ios\": [\"en\", \"ja\"],", e.LineText(5))
	var decoded map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &decoded))
}
