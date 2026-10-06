package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/sessiondb"
)

func TestReferenceContentReadAndSaveInlineComplete(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()
	store := NewReferenceStore("sess1", dir, db)
	original := strings.Repeat("α-long pasted text\n", 2000)
	ref, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Title:   "large paste",
		Mime:    "text/plain",
		Content: original,
		Inline:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, readOnly, err := store.ReadReferenceContent(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readOnly {
		t.Fatal("pasted text unexpectedly read-only")
	}
	if got != original {
		t.Fatalf("read %d bytes, want exact %d-byte content", len(got), len(original))
	}

	edited := original + "edited"
	if err := store.SaveReferenceContent(ref, original, edited); err != nil {
		t.Fatal(err)
	}
	got, readOnly, err = store.ReadReferenceContent(ref.ID)
	if err != nil || readOnly || got != edited {
		t.Fatalf("saved read = (%d bytes, readOnly=%v, err=%v), want exact edit", len(got), readOnly, err)
	}
	loaded, err := sessiondb.LoadPromptReferencesByIDs(db, []string{ref.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Content != edited {
		t.Fatalf("persisted reference = %#v, want edited content", loaded)
	}
}

func TestReferenceContentProjectionRemainsResponsiveDuringStorage(t *testing.T) {
	for _, operation := range []string{"create", "save"} {
		t.Run(operation, func(t *testing.T) {
			db, dir := setupRefStoreDB(t)
			defer db.Close()
			db.SetMaxOpenConns(1)
			store := NewReferenceStore("sess1", dir, db)
			ref, err := store.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "original"})
			if err != nil {
				t.Fatal(err)
			}
			transaction, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transaction.Rollback() }()
			baseline := db.Stats().WaitCount
			done := make(chan error, 1)
			go func() {
				if operation == "create" {
					_, err := store.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: "new"})
					done <- err
				} else {
					done <- store.SaveReferenceContent(ref, "original", "edited")
				}
			}()
			deadline := time.Now().Add(time.Second)
			for db.Stats().WaitCount == baseline && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if db.Stats().WaitCount == baseline {
				t.Fatal("storage fixture did not block on the held DB connection")
			}
			projected := make(chan struct{})
			go func() {
				_, _ = store.GetReference(ref.ID)
				store.ListReferences()
				close(projected)
			}()
			select {
			case <-projected:
			case <-time.After(250 * time.Millisecond):
				t.Fatal("background storage held the UI projection lock")
			}
			if err := transaction.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceContentReadAndSaveFileComplete(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()
	store := NewReferenceStore("sess1", dir, db)
	original := strings.Repeat("file-backed pasted text\n", 2000)
	ref, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedTextFile,
		Title:   "large file paste",
		Mime:    "text/plain",
		Content: original,
		Inline:  false,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, readOnly, err := store.ReadReferenceContent(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readOnly || got != original {
		t.Fatalf("file read = (%d bytes, readOnly=%v), want exact editable content", len(got), readOnly)
	}

	edited := original + "edited"
	if err := store.SaveReferenceContent(ref, original, edited); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ref.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != edited {
		t.Fatal("backing file did not receive exact edit")
	}
	current, ok := store.GetReference(ref.ID)
	if !ok {
		t.Fatal("saved reference missing")
	}
	if current != ref || current.Content != "" {
		t.Fatalf("file-backed metadata changed: got %#v want %#v", current, ref)
	}
	loaded, err := sessiondb.LoadPromptReferencesByIDs(db, []string{ref.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0] != ref {
		t.Fatalf("persisted metadata changed: got %#v want %#v", loaded, ref)
	}
}

func TestReferenceContentRejectsStalePreimagesAndMissingFiles(t *testing.T) {
	store := NewReferenceStore("sess1", t.TempDir(), nil)
	inline, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "original",
	})
	if err != nil {
		t.Fatal(err)
	}
	stale := inline
	stale.Title = "stale metadata"
	if err := store.SaveReferenceContent(stale, "original", "edited"); err == nil {
		t.Fatal("save with stale full reference preimage succeeded")
	}
	if err := store.SaveReferenceContent(inline, "wrong original", "edited"); err == nil {
		t.Fatal("save with stale original content succeeded")
	}
	got, _, err := store.ReadReferenceContent(inline.ID)
	if err != nil || got != "original" {
		t.Fatalf("failed saves changed inline content: got %q, err=%v", got, err)
	}

	fileRef, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedTextFile,
		Content: "on disk",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fileRef.FilePath, []byte("external edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveReferenceContent(fileRef, "on disk", "edited"); err == nil {
		t.Fatal("save over externally changed pasted-text file succeeded")
	}
	data, err := os.ReadFile(fileRef.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external edit" {
		t.Fatalf("stale save overwrote external content: %q", data)
	}
	if err := os.Remove(fileRef.FilePath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadReferenceContent(fileRef.ID); err == nil {
		t.Fatal("missing pasted-text file read succeeded")
	}
	if err := store.SaveReferenceContent(fileRef, "on disk", "edited"); err == nil {
		t.Fatal("missing pasted-text file save succeeded")
	}

	if err := store.DeleteReference(inline.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ReadReferenceContent(inline.ID); err == nil {
		t.Fatal("deleted reference read succeeded")
	}
	if err := store.SaveReferenceContent(inline, "original", "edited"); err == nil {
		t.Fatal("deleted reference save succeeded")
	}
}

func TestReferenceContentPersistenceFailureRollsBackExactState(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()
	store := NewReferenceStore("sess1", dir, db)

	inline, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "inline original",
	})
	if err != nil {
		t.Fatal(err)
	}
	fileRef, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedTextFile,
		Content: "file original",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER fail_reference_content_update
		BEFORE UPDATE OF content ON prompt_references
		BEGIN
			SELECT RAISE(FAIL, 'injected persistence failure');
		END
	`); err != nil {
		t.Fatal(err)
	}

	if err := store.SaveReferenceContent(inline, "inline original", "inline edited"); err == nil {
		t.Fatal("inline save unexpectedly survived injected DB failure")
	}
	current, _ := store.GetReference(inline.ID)
	if current != inline {
		t.Fatalf("inline in-memory state changed on failure: got %#v want %#v", current, inline)
	}

	if err := store.SaveReferenceContent(fileRef, "file original", "file edited"); err == nil {
		t.Fatal("file save unexpectedly survived injected DB failure")
	}
	data, err := os.ReadFile(fileRef.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "file original" {
		t.Fatalf("file rollback = %q, want exact original", data)
	}
	current, _ = store.GetReference(fileRef.ID)
	if current != fileRef {
		t.Fatalf("file metadata changed on failure: got %#v want %#v", current, fileRef)
	}
	loaded, err := sessiondb.LoadPromptReferencesByIDs(db, []string{inline.ID, fileRef.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0] != inline || loaded[1] != fileRef {
		t.Fatalf("database state changed on failure: got %#v", loaded)
	}
}

func TestReferenceContentSelectedFileIsBoundedReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "selected.txt")
	if err := os.WriteFile(path, []byte("selected file body"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewReferenceStore("sess1", dir, nil)
	ref, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:     sessiondb.KindSelectedFile,
		Title:    "selected.txt",
		FilePath: path,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, readOnly, err := store.ReadReferenceContent(ref.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !readOnly || got != "selected file body" {
		t.Fatalf("selected file read = (%q, readOnly=%v), want bounded read-only preview", got, readOnly)
	}
	if err := store.SaveReferenceContent(ref, got, "edited"); err == nil {
		t.Fatal("selected-file save succeeded")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "selected file body" {
		t.Fatalf("selected file mutated: %q", data)
	}
}
