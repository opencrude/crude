package lspedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"crude/types"
	"github.com/stretchr/testify/require"
)

const literalEdit = `{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"界😀\r\n"}`

func TestParseOriginalLiteralAndOpaqueURIs(t *testing.T) {
	raw := []byte(`{"changes":{"opaque:../%2F${HOME}":[` + literalEdit + `]},"metadata":{"large":9007199254740993123456789}}`)
	original := bytes.Clone(raw)
	documents, err := Parse(raw)
	require.NoError(t, err)
	require.Equal(t, []DocumentEdit{{
		URI:   "opaque:../%2F${HOME}",
		Edits: []types.LSPTextEdit{{Col: 1, EndCol: 3, NewText: "界😀\r\n"}},
	}}, documents)
	require.Equal(t, original, raw)
	result, err := Apply("a😀z", documents[0].Edits)
	require.NoError(t, err)
	require.Equal(t, "a界😀\r\nz", result)
}

func TestParseVersionPresenceAndExactInt32(t *testing.T) {
	for _, version := range []string{"null", "0", "17", "-2147483648", "2147483647"} {
		t.Run(version, func(t *testing.T) {
			result, err := Parse([]byte(`{"documentChanges":[{"textDocument":{"uri":"file:///x","version":` + version + `},"edits":[` + literalEdit + `]}]}`))
			require.NoError(t, err)
			require.Len(t, result, 1)
			require.True(t, result[0].Versioned)
			require.Equal(t, "file:///x", result[0].URI)
			if version == "null" {
				require.Nil(t, result[0].Version)
			} else {
				require.NotNil(t, result[0].Version)
				require.Equal(t, version, fmt.Sprint(*result[0].Version))
			}
		})
	}
	for _, version := range []string{`"0"`, "true", "{}", "[]", "1.0", "1e0", "2147483648", "-2147483649", "9007199254740993"} {
		_, err := Parse([]byte(`{"documentChanges":[{"textDocument":{"uri":"x","version":` + version + `},"edits":[]}]}`))
		require.Error(t, err, version)
	}
	result, err := Parse([]byte(`{"documentChanges":[{"textDocument":{"uri":"x"},"edits":[]}]}`))
	require.Error(t, err)
	require.Nil(t, result)
}

func TestParseDocumentChangesPreferredNotConcatenated(t *testing.T) {
	result, err := Parse([]byte(`{"changes":{"same":[` + literalEdit + `]},"documentChanges":[{"textDocument":{"uri":"same","version":null},"edits":[]}]}`))
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.True(t, result[0].Versioned)
	require.Empty(t, result[0].Edits)
	result, err = Parse([]byte(`{"changes":{"discarded":[` + literalEdit + `]},"documentChanges":[]}`))
	require.NoError(t, err)
	require.Empty(t, result)
	// An unsupported alternate is not smuggled through the preferred representation.
	_, err = Parse([]byte(`{"changes":{"discarded":[{"annotationId":"a",` + literalEdit[1:] + `]},"documentChanges":[]}`))
	require.Error(t, err)
}

func TestParseEmptyIsNotMalformedOrUnsupported(t *testing.T) {
	for _, raw := range []string{"null", "{}", `{"changes":{}}`, `{"documentChanges":[]}`, `{"metadata":{"unrecognized":[1,null,true]}}`} {
		result, err := Parse([]byte(raw))
		require.NoError(t, err, raw)
		require.Empty(t, result, raw)
	}
	for _, raw := range []string{
		"", " ", "[]", "false", "1", `""`, "{}{}", "{", `{"changes":null}`, `{"documentChanges":null}`,
		`{"changes":[]}`, `{"changes":{"x":null}}`, `{"documentChanges":[null]}`,
		`{"documentChanges":[{"edits":[]}]}`,
		`{"documentChanges":[{"textDocument":{"uri":"x","version":null}}]}`,
		`{"documentChanges":[{"textDocument":{"uri":"","version":null},"edits":[]}]}`,
	} {
		result, err := Parse([]byte(raw))
		require.Error(t, err, raw)
		require.Nil(t, result, raw)
	}
}

func TestParseRejectsUnsupportedOperationsAtomically(t *testing.T) {
	for _, raw := range []string{
		`{"changeAnnotations":{}}`,
		`{"changeAnnotations":null,"changes":{"x":[` + literalEdit + `]}}`,
		`{"documentChanges":[{"kind":"create","uri":"x"}]}`,
		`{"documentChanges":[{"kind":"rename","oldUri":"x","newUri":"y"}]}`,
		`{"documentChanges":[{"kind":"delete","uri":"x"}]}`,
		`{"documentChanges":[{"textDocument":{"uri":"x","version":0},"edits":[]},{"kind":"create","uri":"y"}]}`,
		`{"documentChanges":[{"kind":"create","textDocument":{"uri":"x","version":0},"edits":[]}]}`,
		`{"command":"evil"}`,
		`{"commands":[]}`,
		`{"executeCommand":{"command":"evil"}}`,
	} {
		result, err := Parse([]byte(raw))
		require.Error(t, err, raw)
		require.Nil(t, result, raw)
	}
	for _, extra := range []string{
		`"annotationId":"a"`, `"annotationId":null`, `"insertTextFormat":2`,
		`"snippet":{"value":"$1"}`, `"insertText":"$1"`, `"insertTextMode":2`,
		`"insert":{}`, `"replace":{}`, `"command":"evil"`,
	} {
		raw := `{"changes":{"x":[` + literalEdit + `,{` + extra + `,` + literalEdit[1:] + `]}}`
		result, err := Parse([]byte(raw))
		require.Error(t, err, raw)
		require.Nil(t, result, raw)
	}
}

func TestParseRejectsMalformedRangesAndReplacements(t *testing.T) {
	for _, edit := range []string{
		`{}`, `{"range":null,"newText":""}`, `{"newText":"x"}`,
		`{"range":{"start":{"line":0,"character":0},"end":{"line":0}},"newText":"x"}`,
		`{"range":{"start":{"line":-1,"character":0},"end":{"line":0,"character":0}},"newText":""}`,
		`{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":0}},"newText":""}`,
		strings.Replace(literalEdit, `"line":0`, `"line":2147483648`, 1),
		strings.Replace(literalEdit, `"character":1`, `"character":1.0`, 1),
		strings.Replace(literalEdit, `"character":1`, `"character":null`, 1),
		strings.Replace(literalEdit, `"newText":"界😀\r\n"`, `"newText":null`, 1),
		strings.Replace(literalEdit, `"newText":"界😀\r\n"`, `"newText":{"value":"$1"}`, 1),
	} {
		result, err := Parse([]byte(`{"changes":{"x":[` + edit + `]}}`))
		require.Error(t, err, edit)
		require.Nil(t, result, edit)
	}
}

func TestParseDuplicateMembersAndTargets(t *testing.T) {
	for _, raw := range []string{
		`{"changes":{},"changes":{}}`,
		`{"changes":{},"\u0063hanges":{}}`,
		`{"changes":{"x":[],"x":[]}}`,
		`{"changes":{"x":[],"\u0078":[]}}`,
		`{"documentChanges":[{"textDocument":{"uri":"x","version":null,"version":0},"edits":[]}]}`,
		`{"documentChanges":[{"textDocument":{"uri":"x","version":0},"edits":[]},{"textDocument":{"uri":"x","version":1},"edits":[]}]}`,
		`{"changes":{"x":[{"range":{"start":{"line":0,"line":1,"character":0},"end":{"line":1,"character":0}},"newText":""}]}}`,
	} {
		result, err := Parse([]byte(raw))
		require.Error(t, err, raw)
		require.Nil(t, result, raw)
	}
}

func TestParseUnicodeWithoutSilentRepair(t *testing.T) {
	for _, value := range []string{`"\ud800"`, `"\udc00"`, `"\ud800x"`, `"\ud800\u0041"`} {
		_, err := Parse([]byte(`{"changes":{"x":[` + strings.Replace(literalEdit, `"界😀\r\n"`, value, 1) + `]}}`))
		require.Error(t, err, value)
	}
	_, err := Parse([]byte("{\"changes\":{\"x\000\":[]}}"))
	require.Error(t, err)
	_, err = Parse([]byte("{\"changes\":{\"x\xff\":[]}}"))
	require.Error(t, err)
	result, err := Parse([]byte(`{"changes":{"x":[` + strings.Replace(literalEdit, `"界😀\r\n"`, `"\ud83d\ude00\\uD800\""`, 1) + `]}}`))
	require.NoError(t, err)
	require.Equal(t, "😀\\uD800\"", result[0].Edits[0].NewText)
}

func TestParseBoundsBeforeReturningAnyEdits(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"raw", []byte(strings.Repeat(" ", MaxRawBytes+1))},
		{"replacement", []byte(`{"changes":{"x":[` + strings.Replace(literalEdit, `"界😀\r\n"`, `"`+strings.Repeat("x", MaxStringBytes+1)+`"`, 1) + `]}}`)},
		{"uri", []byte(`{"changes":{"` + strings.Repeat("x", MaxStringBytes+1) + `":[]}}`)},
		{"edits", []byte(`{"changes":{"x":[` + strings.Repeat(literalEdit+",", MaxEdits) + literalEdit + `]}}`)},
		{"nesting", []byte(`{"metadata":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + `}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Parse(tc.raw)
			require.Error(t, err)
			require.Nil(t, result)
		})
	}
	for _, count := range []int{MaxDocuments, MaxDocuments + 1} {
		var raw strings.Builder
		raw.WriteString(`{"changes":{`)
		for i := 0; i < count; i++ {
			if i != 0 {
				raw.WriteByte(',')
			}
			fmt.Fprintf(&raw, `"opaque:%d":[]`, i)
		}
		raw.WriteString("}}")
		result, err := Parse([]byte(raw.String()))
		if count == MaxDocuments {
			require.NoError(t, err)
			require.Len(t, result, count)
		} else {
			require.Error(t, err)
			require.Nil(t, result)
		}
	}
	result, err := Parse([]byte(`{"changes":{"x":[` + strings.Repeat(literalEdit+",", MaxEdits-1) + literalEdit + `]}}`))
	require.NoError(t, err)
	require.Len(t, result[0].Edits, MaxEdits)
	result, err = Parse([]byte(`{"changes":{"x":[` + strings.Repeat(literalEdit+",", MaxEdits-1) + literalEdit + `],"y":[` + literalEdit + `]}}`))
	require.Error(t, err)
	require.Nil(t, result)
	result, err = Parse([]byte(`{"changes":{"` + strings.Repeat("x", MaxStringBytes) + `":[` + strings.Replace(literalEdit, `"界😀\r\n"`, `"`+strings.Repeat("y", MaxStringBytes)+`"`, 1) + `]}}`))
	require.NoError(t, err)
	require.Len(t, result[0].URI, MaxStringBytes)
	require.Len(t, result[0].Edits[0].NewText, MaxStringBytes)
	result, err = Parse(append([]byte("{}"), bytes.Repeat([]byte(" "), MaxRawBytes-2)...))
	require.NoError(t, err)
	require.Empty(t, result)
	for _, count := range []int{MaxDocuments, MaxDocuments + 1} {
		var raw strings.Builder
		raw.WriteString(`{"documentChanges":[`)
		for i := 0; i < count; i++ {
			if i != 0 {
				raw.WriteByte(',')
			}
			fmt.Fprintf(&raw, `{"textDocument":{"uri":"opaque:%d","version":null},"edits":[]}`, i)
		}
		raw.WriteString("]}")
		result, err := Parse([]byte(raw.String()))
		if count == MaxDocuments {
			require.NoError(t, err)
			require.Len(t, result, count)
		} else {
			require.Error(t, err)
			require.Nil(t, result)
		}
	}
	// Both representations are validated/accounted, though only the preferred
	// one is returned: an alternate cannot bypass aggregate work limits.
	result, err = Parse([]byte(`{"changes":{"x":[` + strings.Repeat(literalEdit+",", MaxEdits-1) + literalEdit + `]},"documentChanges":[{"textDocument":{"uri":"x","version":null},"edits":[` + literalEdit + `]}]}`))
	require.Error(t, err)
	require.Nil(t, result)
	// The aggregate replacement guard is defense in depth: original JSON's
	// 1 MiB bound is stricter than 1 MiB of unescaped replacement payload.
	budget := parseBudget{replacements: MaxReplacementBytes}
	_, err = budget.edits(json.RawMessage("[" + literalEdit + "]"))
	require.Error(t, err)
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"null", "{}", `{"changes":{"x":[` + literalEdit + `]}}`,
		`{"documentChanges":[{"textDocument":{"uri":"x","version":null},"edits":[]}]}`,
		`{"changes":{},"\u0063hanges":{}}`, `{"changes":{"\ud800":[]}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		result, err := Parse(raw)
		if err != nil {
			require.Nil(t, result)
			return
		}
		require.LessOrEqual(t, len(raw), MaxRawBytes)
		require.LessOrEqual(t, len(result), MaxDocuments)
		count, replacements := 0, 0
		for _, target := range result {
			require.NotEmpty(t, target.URI)
			require.LessOrEqual(t, len(target.URI), MaxStringBytes)
			for _, edit := range target.Edits {
				count++
				replacements += len(edit.NewText)
				require.LessOrEqual(t, len(edit.NewText), MaxStringBytes)
			}
		}
		require.LessOrEqual(t, count, MaxEdits)
		require.LessOrEqual(t, replacements, MaxReplacementBytes)
	})
}
