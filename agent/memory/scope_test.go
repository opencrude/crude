package memory

import (
	"slices"
	"testing"
)

// The remember tool tells agents that paths boost recall. Recall used them as
// a filter instead: once a session had touched any file, a memory scoped to
// another file, to a file that moved, or to "." (the whole workspace) was
// never offered. 157 of 207 active memories in one workspace had a scope.
func TestScopedMemoriesRankHigherInScopeAndStayVisibleElsewhere(t *testing.T) {
	store := openTestStore(t)
	remember := func(title, content string, paths ...string) Record {
		t.Helper()
		record, err := store.Remember(t.Context(), Record{Kind: KindFact, Title: title, Content: content, ScopePaths: paths, Importance: 50, Source: SourceTool})
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	keyboard := remember("Caps Lock and Esc are swapped", "The user's Caps Lock key sends Esc; a capture that needs Esc uses Caps Lock.", "scripts/tuishot.sh")
	workspace := remember("Esc handling is batched", "Esc work across the workspace lands in one batch.", ".")
	moved := remember("Esc forwarding to MCP clients", "MCP clients receive Esc unchanged.", "mcpclient/client.go")
	editor := remember("Esc closes editor overlays", "In the editor, Esc closes overlays first.", "tui/editor")

	ids := func(q RecallQuery) []string {
		t.Helper()
		records, err := store.Recall(t.Context(), q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, record := range records {
			out = append(out, record.ID)
		}
		return out
	}
	elsewhere := ids(RecallQuery{Text: "press Esc", TouchedPaths: []string{"agent/session/run.go"}})
	for _, record := range []Record{keyboard, workspace, moved, editor} {
		if !slices.Contains(elsewhere, record.ID) {
			t.Errorf("%q is hidden while the agent works on another file: %v", record.Title, elsewhere)
		}
	}
	inEditor := ids(RecallQuery{Text: "press Esc", ActivePaths: []string{"tui/editor/keys.go"}})
	if len(inEditor) == 0 || inEditor[0] != editor.ID {
		t.Fatalf("the memory scoped to the active file does not rank first: %v", inEditor)
	}
	shown := ids(RecallQuery{Text: "press Esc", Exclude: map[RevisionRef]bool{{ID: keyboard.ID, Revision: keyboard.Revision}: true}})
	if slices.Contains(shown, keyboard.ID) || !slices.Contains(shown, editor.ID) {
		t.Fatalf("a revision already in context was sent again: %v", shown)
	}
}
