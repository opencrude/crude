package core

import (
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"

	"crude/coreprotocol"
	"crude/history"
	"crude/sessiondb"
	"crude/workspace"
)

func TestCoreHistoryImmutableCrossSession(t *testing.T) {
	h := history.New()
	path := filepath.Join(t.TempDir(), "history.json")
	a := workspace.NewReferenceStore("a", t.TempDir(), nil)
	b := workspace.NewReferenceStore("b", t.TempDir(), nil)
	ref, err := a.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedTextFile, Content: "immutable source"})
	if err != nil {
		t.Fatal(err)
	}
	text := "Ω " + ref.DisplayName()
	in := coreprotocol.HistoryCommand{SessionID: "a", Text: text, Attachments: []coreprotocol.HistoryAttachment{{Start: 2, End: utf8.RuneCountInString(text), Reference: referenceView(ref)}}}
	// A client path is ignored in favor of the server reference identity.
	in.Attachments[0].Reference.Reference.FilePath = "/arbitrary/client/path"
	value, err := runHistoryCommand(h, path, a, coreprotocol.HistoryRecord, in)
	if err != nil {
		t.Fatal(err)
	}
	entry := value.(coreprotocol.HistoryListResult).Entries[0]
	if err := os.WriteFile(ref.FilePath, []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = runHistoryCommand(h, path, b, coreprotocol.HistoryRestore, coreprotocol.HistoryCommand{SessionID: "b", EntryID: entry.ID})
	if err != nil {
		t.Fatal(err)
	}
	restored := value.(coreprotocol.HistoryRestoreResult)
	if restored.Text != text || len(restored.ReferenceIDs) != 1 {
		t.Fatalf("bad restore: %+v", restored)
	}
	created := restored.References[0].Reference
	bytes, err := os.ReadFile(created.FilePath)
	if err != nil || string(bytes) != "immutable source" || created.SessionID != "b" {
		t.Fatalf("nonimmutable restore: %q %v", bytes, err)
	}
	if err := os.Remove(entry.Attachments[0].Reference.Reference.FilePath); err != nil {
		t.Fatal(err)
	}
	before := len(b.ListReferences())
	if _, err := runHistoryCommand(h, path, b, coreprotocol.HistoryRestore, coreprotocol.HistoryCommand{SessionID: "b", EntryID: entry.ID}); err == nil {
		t.Fatal("missing media downgraded")
	}
	if len(b.ListReferences()) != before {
		t.Fatal("failed import mutated destination")
	}
	in.Attachments[0].Start = 1
	if _, err := runHistoryCommand(h, path, a, coreprotocol.HistoryRecord, in); err == nil {
		t.Fatal("accepted invalid span")
	}
	in.Attachments[0].Start = 2
	in.Attachments[0].Reference.Revision = "stale"
	if _, err := runHistoryCommand(h, path, a, coreprotocol.HistoryRecord, in); err == nil {
		t.Fatal("stale reference downgraded")
	}
}
