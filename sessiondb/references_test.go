package sessiondb

import (
	"path/filepath"
	"testing"
	"time"
)

func setupRefDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	// Create a session for foreign key
	_ = Create(db, SessionMeta{
		ID:          "sess1",
		ProjectPath: "/tmp",
		ProjectName: "test",
		Model:       "test-model",
		Provider:    "test",
		CreatedAt:   1,
	})
	return &DB{db: db, path: dbPath}, dir
}

func makeRef(id, kind, label string) Reference {
	return Reference{
		ID:        id,
		SessionID: "sess1",
		Kind:      Kind(kind),
		Label:     label,
		Title:     "test ref",
		Mime:      "",
		Content:   "test content",
		FilePath:  "",
		Inline:    false,
		CreatedAt: time.Now().UnixNano(),
		DeletedAt: 0,
	}
}

func TestLoadPromptReferencesByIDs_SpecificIDs(t *testing.T) {
	db, _ := setupRefDB(t)
	defer db.Close()

	// Insert three references
	ref1 := makeRef("ref1", "pasted_text", "[Pasted Text1]")
	ref2 := makeRef("ref2", "pasted_text", "[Pasted Text2]")
	ref3 := makeRef("ref3", "pasted_image", "[Pasted IMG1]")

	if err := InsertPromptReference(db.db, ref1); err != nil {
		t.Fatalf("insert ref1: %v", err)
	}
	if err := InsertPromptReference(db.db, ref2); err != nil {
		t.Fatalf("insert ref2: %v", err)
	}
	if err := InsertPromptReference(db.db, ref3); err != nil {
		t.Fatalf("insert ref3: %v", err)
	}

	// Query only ref1 and ref3
	refs, err := LoadPromptReferencesByIDs(db.db, []string{"ref1", "ref3"})
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(refs))
	}
	// Should only return the requested IDs
	gotIDs := make(map[string]bool)
	for _, r := range refs {
		gotIDs[r.ID] = true
	}
	if !gotIDs["ref1"] || !gotIDs["ref3"] {
		t.Errorf("expected ref1 and ref3, got IDs: %v", gotIDs)
	}
	if gotIDs["ref2"] {
		t.Error("ref2 should not be returned")
	}
}

func TestLoadPromptReferencesByIDs_DeletedExcluded(t *testing.T) {
	db, _ := setupRefDB(t)
	defer db.Close()

	ref1 := makeRef("ref-a", "pasted_text", "[Pasted Text1]")
	if err := InsertPromptReference(db.db, ref1); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Soft-delete it
	if err := DeletePromptReference(db.db, "ref-a"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	refs, err := LoadPromptReferencesByIDs(db.db, []string{"ref-a"})
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("deleted refs should be excluded, got %d refs", len(refs))
	}
}

func TestLoadPromptReferencesByIDs_EmptyIDs(t *testing.T) {
	db, _ := setupRefDB(t)
	defer db.Close()

	refs, err := LoadPromptReferencesByIDs(db.db, nil)
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs(nil): %v", err)
	}
	if refs != nil {
		t.Errorf("empty ID list should return nil, got %v", refs)
	}

	refs, err = LoadPromptReferencesByIDs(db.db, []string{})
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs(empty): %v", err)
	}
	if refs != nil {
		t.Errorf("empty ID list should return nil, got %v", refs)
	}
}

func TestLoadPromptReferencesByIDs_NonExistentIDs(t *testing.T) {
	db, _ := setupRefDB(t)
	defer db.Close()

	refs, err := LoadPromptReferencesByIDs(db.db, []string{"nonexistent1", "nonexistent2"})
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("non-existent IDs should return empty, got %d refs", len(refs))
	}
}

func TestLoadPromptReferences_DeletedExcluded(t *testing.T) {
	db, _ := setupRefDB(t)
	defer db.Close()

	ref1 := makeRef("ref-x", "pasted_text", "[Pasted Text1]")
	ref2 := makeRef("ref-y", "pasted_text", "[Pasted Text2]")
	if err := InsertPromptReference(db.db, ref1); err != nil {
		t.Fatalf("insert ref1: %v", err)
	}
	if err := InsertPromptReference(db.db, ref2); err != nil {
		t.Fatalf("insert ref2: %v", err)
	}

	// Delete ref1
	if err := DeletePromptReference(db.db, "ref-x"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	refs, err := LoadPromptReferences(db.db)
	if err != nil {
		t.Fatalf("LoadPromptReferences: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref (deleted filtered), got %d", len(refs))
	}
	if refs[0].ID != "ref-y" {
		t.Errorf("expected ref-y, got %s", refs[0].ID)
	}
}

func TestUpdatePromptReferenceFilePath(t *testing.T) {
	db, _ := setupRefDB(t)
	defer db.Close()

	ref := makeRef("ref-fp", "pasted_image", "[Pasted IMG1]")
	ref.Content = "base64data"
	ref.Mime = "image/png"
	if err := InsertPromptReference(db.db, ref); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Update file path and clear content
	if err := UpdatePromptReferenceFilePath(db.db, "ref-fp", "/tmp/img.png"); err != nil {
		t.Fatalf("UpdatePromptReferenceFilePath: %v", err)
	}

	// Load back and verify
	refs, err := LoadPromptReferencesByIDs(db.db, []string{"ref-fp"})
	if err != nil {
		t.Fatalf("LoadPromptReferencesByIDs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].FilePath != "/tmp/img.png" {
		t.Errorf("FilePath: got %q, want /tmp/img.png", refs[0].FilePath)
	}
	if refs[0].Content != "" {
		t.Errorf("Content should be cleared, got %q", refs[0].Content)
	}
}
