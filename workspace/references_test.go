package workspace

import (
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"crude/sessiondb"
)

func setupRefStoreDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "refstore.db")
	db, err := sessiondb.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	_ = sessiondb.Create(db, sessiondb.SessionMeta{
		ID:          "sess1",
		ProjectPath: "/tmp",
		ProjectName: "test",
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1,
	})
	return db, dir
}

func TestReferenceStore_CreateReference(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	ref, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "hello world",
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}
	if ref.ID == "" {
		t.Error("ref should have ID")
	}
	if ref.Label != "[Pasted Text1]" {
		t.Errorf("Label: got %q, want [Pasted Text1]", ref.Label)
	}
	// KindPastedText keeps Content in memory (no file write)
	if ref.Content != "hello world" {
		t.Errorf("Content: got %q, want 'hello world'", ref.Content)
	}
}

func TestReferenceStore_CreateReferenceConcurrent(t *testing.T) {
	store := NewReferenceStore("sess1", t.TempDir(), nil)
	const count = 32
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.CreateReference(sessiondb.CreateRequest{
				Kind:    sessiondb.KindPastedImage,
				Mime:    "image/png",
				Content: base64.StdEncoding.EncodeToString([]byte("image")),
			})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	refs := store.ListReferences()
	if len(refs) != count {
		t.Fatalf("references=%d, want %d", len(refs), count)
	}
	labels := make(map[string]struct{}, count)
	for _, ref := range refs {
		if _, exists := labels[ref.Label]; exists {
			t.Fatalf("duplicate concurrent label %q", ref.Label)
		}
		labels[ref.Label] = struct{}{}
	}
}

func TestReferenceStore_CreateReference_TextFile(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	ref, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedTextFile,
		Content: "file content here",
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}
	if ref.ID == "" {
		t.Error("ref should have ID")
	}
	if ref.Label != "[Pasted Text1]" {
		t.Errorf("Label: got %q, want [Pasted Text1]", ref.Label)
	}
	// KindPastedTextFile writes content to disk and clears Content
	if ref.Content != "" {
		t.Errorf("Content should be cleared after writing to file, got %q", ref.Content)
	}
	if ref.FilePath == "" {
		t.Error("FilePath should be set after writing to disk")
	}
	// Verify file exists
	if _, err := os.Stat(ref.FilePath); os.IsNotExist(err) {
		t.Errorf("file should exist at %q", ref.FilePath)
	}
}

func TestReferenceStore_CreateReference_Image(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	imgData := []byte("fake image")
	b64 := base64.StdEncoding.EncodeToString(imgData)

	ref, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedImage,
		Content: b64,
		Mime:    "image/png",
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}
	if ref.ID == "" {
		t.Error("ref should have ID")
	}
	if ref.Label != "[Pasted IMG1]" {
		t.Errorf("Label: got %q, want [Pasted IMG1]", ref.Label)
	}
	// KindPastedImage decodes base64, writes to disk, clears Content
	if ref.Content != "" {
		t.Errorf("Content should be cleared after writing to file, got %d chars", len(ref.Content))
	}
	if ref.FilePath == "" {
		t.Error("FilePath should be set after writing to disk")
	}
	if _, err := os.Stat(ref.FilePath); os.IsNotExist(err) {
		t.Errorf("file should exist at %q", ref.FilePath)
	}
}

func TestReferenceStore_CreateReference_LabelCollision(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	// Create a ref, then add it as existing to simulate session resume
	ref1, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedTextFile,
		Content: "first content",
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}

	// Simulate resume: re-add the existing ref
	store2 := NewReferenceStore("sess1", dir, db)
	store2.AddExisting(ref1)

	// Create new ref — should NOT collide
	ref2, err := store2.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedTextFile,
		Content: "second content",
	})
	if err != nil {
		t.Fatalf("CreateReference after AddExisting: %v", err)
	}
	if ref2.Label == ref1.Label {
		t.Errorf("labels should not collide: both %q", ref1.Label)
	}
	if ref2.Label != "[Pasted Text2]" {
		t.Errorf("expected [Pasted Text2], got %q", ref2.Label)
	}
}

func TestReferenceStore_CreateReference_CustomLabelCollision(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	// Create a ref with auto label
	_, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "auto content",
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}

	// Create a ref with custom label that collides
	ref2, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "custom content",
		Label:   "[Pasted Text1]", // same as first
	})
	if err != nil {
		t.Fatalf("CreateReference with colliding label: %v", err)
	}
	if ref2.Label == "[Pasted Text1]" {
		t.Errorf("custom colliding label should be uniquified, got %q", ref2.Label)
	}
	// Should have a numeric suffix
	if ref2.Label != "[Pasted Text11]" {
		t.Errorf("expected [Pasted Text11], got %q", ref2.Label)
	}
}

func TestReferenceStore_ListReferences_FiltersDeleted(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	ref1, _ := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "keep this",
	})
	ref2, _ := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "delete this",
	})

	// Before delete, 2 refs
	list := store.ListReferences()
	if len(list) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(list))
	}

	// Delete ref2
	_ = store.DeleteReference(ref2.ID)

	// After delete, only 1 ref
	list = store.ListReferences()
	if len(list) != 1 {
		t.Fatalf("expected 1 ref after delete, got %d", len(list))
	}
	if list[0].ID != ref1.ID {
		t.Errorf("expected ref1, got %s", list[0].ID)
	}

	// Verify deleted ref still in map but not active
	got, ok := store.GetReference(ref2.ID)
	if !ok {
		t.Error("deleted ref should still be in map")
	}
	if got.Active() {
		t.Error("deleted ref should not be active")
	}
	_ = got
}

func TestReferenceStore_AddExisting_UpdatesCounter(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	// Add an existing ref with a high index label
	ref := sessiondb.Reference{
		ID:        "existing-ref",
		SessionID: "sess1",
		Kind:      sessiondb.KindPastedText,
		Label:     "[Pasted Text5]",
		Content:   "existing content",
		CreatedAt: time.Now().UnixNano(),
	}
	store.AddExisting(ref)

	// Next create should use index 6, not 1
	newRef, err := store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "new content",
	})
	if err != nil {
		t.Fatalf("CreateReference: %v", err)
	}
	if newRef.Label != "[Pasted Text6]" {
		t.Errorf("expected [Pasted Text6], got %q", newRef.Label)
	}
}

func TestReferenceStore_AddExisting_ImageMigration(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	// Create base64 image content
	imgData := []byte("fake image data")
	b64 := base64.StdEncoding.EncodeToString(imgData)

	// Add existing ref with Content set but no FilePath
	ref := sessiondb.Reference{
		ID:        "img-ref",
		SessionID: "sess1",
		Kind:      sessiondb.KindPastedImage,
		Label:     "[Pasted IMG3]",
		Mime:      "image/png",
		Content:   b64,
		FilePath:  "",
		CreatedAt: time.Now().UnixNano(),
	}
	store.AddExisting(ref)

	// Verify migration happened
	got, ok := store.GetReference("img-ref")
	if !ok {
		t.Fatal("ref should be in store")
	}
	if got.Content != "" {
		t.Errorf("Content should be cleared after migration, got %d chars", len(got.Content))
	}
	if got.FilePath == "" {
		t.Error("FilePath should be set after migration")
	}
	// File should exist on disk
	if _, err := os.Stat(got.FilePath); os.IsNotExist(err) {
		t.Errorf("migrated file should exist at %q", got.FilePath)
	}
}

func TestReferenceStore_AddExisting_ImageMigration_DBPersist(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	// Insert the legacy reference into DB first
	imgData := []byte("fake image data for DB test")
	b64 := base64.StdEncoding.EncodeToString(imgData)
	ref := sessiondb.Reference{
		ID:        "img-db-ref",
		SessionID: "sess1",
		Kind:      sessiondb.KindPastedImage,
		Label:     "[Pasted IMG1]",
		Mime:      "image/png",
		Content:   b64,
		FilePath:  "",
		CreatedAt: time.Now().UnixNano(),
	}
	_ = sessiondb.InsertPromptReference(db, ref)

	// Now load and add via AddExisting
	store.AddExisting(ref)

	// Verify the DB was updated
	refs, err := sessiondb.LoadPromptReferencesByIDs(db, []string{"img-db-ref"})
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Content != "" {
		t.Errorf("DB Content should be cleared after migration, got %q", refs[0].Content)
	}
	if refs[0].FilePath == "" {
		t.Error("DB FilePath should be set after migration")
	}
}

// --- extractLabelIndex tests ---

func TestExtractLabelIndex(t *testing.T) {
	tests := []struct {
		label string
		want  int
	}{
		{"[Pasted Text1]", 1},
		{"[Pasted Text5]", 5},
		{"[Pasted Text10]", 10},
		{"[File2]", 2},
		{"[Pasted IMG1]", 1},
		{"[Pasted Text File3]", 3},
		{"[Reference4]", 4},
		{"[Custom Label]", 0},     // no trailing digits
		{"", 0},                   // empty
		{"[no-number]", 0},        // no number
		{"[Pasted Text0]", 0},     // zero
		{"[Pasted Text99]", 99},   // two-digit
		{"[Pasted Text100]", 100}, // three-digit
	}
	for _, tt := range tests {
		got := extractLabelIndex(tt.label)
		if got != tt.want {
			t.Errorf("extractLabelIndex(%q): got %d, want %d", tt.label, got, tt.want)
		}
	}
}

// --- defaultReferenceLabel tests ---

func TestDefaultReferenceLabel(t *testing.T) {
	tests := []struct {
		kind sessiondb.Kind
		idx  int
		want string
	}{
		{sessiondb.KindPastedText, 1, "[Pasted Text1]"},
		{sessiondb.KindPastedTextFile, 2, "[Pasted Text2]"},
		{sessiondb.KindPastedImage, 3, "[Pasted IMG3]"},
		{sessiondb.KindSelectedFile, 4, "[File4]"},
		{sessiondb.Kind("unknown"), 5, "[Reference5]"},
	}
	for _, tt := range tests {
		got := defaultReferenceLabel(tt.kind, tt.idx)
		if got != tt.want {
			t.Errorf("defaultReferenceLabel(%s, %d): got %q, want %q", tt.kind, tt.idx, got, tt.want)
		}
	}
}

// --- labelExists tests ---

func TestLabelExists(t *testing.T) {
	db, dir := setupRefStoreDB(t)
	defer db.Close()

	store := NewReferenceStore("sess1", dir, db)

	// No refs yet
	if store.labelExists("[Pasted Text1]") {
		t.Error("labelExists should be false with no refs")
	}

	// Add a ref
	_, _ = store.CreateReference(sessiondb.CreateRequest{
		Kind:    sessiondb.KindPastedText,
		Content: "content",
	})

	if !store.labelExists("[Pasted Text1]") {
		t.Error("labelExists should find existing label")
	}
	if store.labelExists("[Pasted Text99]") {
		t.Error("labelExists should not find non-existent label")
	}
}
