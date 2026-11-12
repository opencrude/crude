package workspace

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"crude/types"
)

// A large source with both an explicit UTF-16 edit and an implicit prefix must
// retain the provider's exact row, text and range, not a nearby display column.
func TestCompletionNormalizationKeepsSourceRangesInLargeUnicodeDocument(t *testing.T) {
	source := strings.Repeat("fill\n", 20000) + strings.Repeat("x ", 150) + "界😀名"
	response := types.LSPCompletionResponse{JSON: []byte(`[{"label":"explicit","textEdit":{"range":{"start":{"line":20000,"character":300},"end":{"line":20000,"character":304}},"newText":"value"}},{"label":"implicit","insertText":"newName"}]`)}
	originals, set, _, err := normalizeCompletionSet(source, 20000, 304, response)
	if err != nil || len(set.Entries) != 2 {
		t.Fatal("completion result was not normalized", err)
	}
	for _, tc := range []struct {
		index      int
		prefix     string
		start, end int
		newText    string
	}{
		{0, "界😀名", 300, 304, "value"}, {1, "名", 303, 304, "newName"},
	} {
		row := set.Entries[tc.index]
		if row.Prefix != tc.prefix || row.Insertion.TextEdit == nil {
			t.Fatalf("row %d lost its source prefix: %+v", tc.index, row)
		}
		edit := row.Insertion.TextEdit
		if edit.Line != 20000 || edit.EndLine != 20000 || edit.Col != tc.start || edit.EndCol != tc.end || edit.NewText != tc.newText {
			t.Fatalf("row %d changed source edit: %+v", tc.index, edit)
		}
		if !json.Valid(originals[row.ID]) {
			t.Fatal("completion lost its original item identity", row.ID)
		}
	}
	if _, _, _, err = normalizeCompletionSet(source, 20000, 302, response); err == nil {
		t.Fatal("completion accepted the middle of a surrogate pair")
	}
}

// An empty answer still checks the exact source cursor; it is not permission
// to accept an invalid position or an unsupported response with invented data.
func TestCompletionEmptyAnswerStillValidatesOriginalCursor(t *testing.T) {
	for _, raw := range []string{"[]", "null"} {
		response := types.LSPCompletionResponse{JSON: []byte(raw)}
		originals, set, retained, err := normalizeCompletionSet("a😀\r\n", 0, 3, response)
		if err != nil || len(originals) != 0 || len(set.Entries) != 0 || retained != 0 {
			t.Fatal("valid empty answer changed", raw, err)
		}
		if _, _, _, err = normalizeCompletionSet("a😀\r\n", 0, 2, response); err == nil {
			t.Fatal("empty answer accepted a split UTF-16 position", raw)
		}
	}
}

// Normalization includes parsing the original response, source lookup and
// detached rows. It does not measure editor input or terminal presentation.
func BenchmarkCompletionNormalization(b *testing.B) {
	for _, tc := range []struct {
		name, source     string
		line, col, count int
	}{
		{"empty", strings.Repeat("identifier\n", 20000), 19999, 5, 0},
		{"short", strings.Repeat("line\n", 40) + "pr", 40, 2, 1},
		{"manylines", strings.Repeat("identifier\n", 20000), 19999, 5, 128},
		{"longline", strings.Repeat("x ", 131072) + "identifier", 0, 262154, 128},
	} {
		b.Run(tc.name, func(b *testing.B) {
			items := make([]map[string]any, tc.count)
			for i := range items {
				items[i] = map[string]any{"label": fmt.Sprintf("identifier%d", i)}
			}
			raw, err := json.Marshal(items)
			if err != nil {
				b.Fatal(err)
			}
			response := types.LSPCompletionResponse{JSON: raw}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, set, _, err := normalizeCompletionSet(tc.source, tc.line, tc.col, response)
				if err != nil || len(set.Entries) != tc.count {
					b.Fatal("completion normalization failed", err)
				}
			}
		})
	}
}

// Item commit characters replace the provider's default, even when empty.
func TestCompletionCommitCharactersUseItemThenProviderDefault(t *testing.T) {
	response := types.LSPCompletionResponse{CommitCharacters: []string{".", "("},
		JSON: []byte(`[{"label":"own","commitCharacters":[";"]},{"label":"none","commitCharacters":[]},{"label":"inherits"}]`)}
	_, set, _, err := normalizeCompletionSet("x", 0, 1, response)
	if err != nil || len(set.Entries) != 3 {
		t.Fatal(err)
	}
	got := fmt.Sprint(set.Entries[0].CommitCharacters, set.Entries[1].CommitCharacters, set.Entries[2].CommitCharacters)
	if got != "[;] [] [. (]" {
		t.Fatal("commit characters lost item precedence:", got)
	}
	response.CommitCharacters = []string{"ab"}
	if _, _, _, err := normalizeCompletionSet("x", 0, 1, response); err == nil {
		t.Fatal("a malformed provider default was accepted")
	}
}
