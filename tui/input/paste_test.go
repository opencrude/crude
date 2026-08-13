package input

import (
	"strings"
	"testing"

	"crude/sessiondb"
	"crude/tui/theme"
)

type pasteReferenceStore struct {
	references []sessiondb.Reference
}

func (s *pasteReferenceStore) CreateReference(request sessiondb.CreateRequest) (sessiondb.Reference, error) {
	ref := sessiondb.Reference{
		ID: "ref-large", Kind: request.Kind, Label: "[Pasted TXT1]",
		Title: request.Title, Content: request.Content, Inline: request.Inline,
	}
	s.references = append(s.references, ref)
	return ref, nil
}
func (s *pasteReferenceStore) UpdateReferenceContent(string, string) error { return nil }
func (s *pasteReferenceStore) DeleteReference(string) error                { return nil }
func (s *pasteReferenceStore) GetReference(id string) (sessiondb.Reference, bool) {
	for _, ref := range s.references {
		if ref.ID == id {
			return ref, true
		}
	}
	return sessiondb.Reference{}, false
}
func (s *pasteReferenceStore) ListReferences() []sessiondb.Reference {
	return append([]sessiondb.Reference(nil), s.references...)
}

func TestLargePasteInsertsFileBackedReferenceChip(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	store := &pasteReferenceStore{}
	model.SetReferenceStore(store)

	content := strings.Repeat("large paste ", pasteInlineThreshold)
	updated, _, _ := model.handlePaste(content)
	if len(store.references) != 1 || store.references[0].Kind != sessiondb.KindPastedTextFile {
		t.Fatalf("references = %+v", store.references)
	}
	ids := updated.ReferencedIDs()
	if len(ids) != 1 || ids[0] != "ref-large" {
		t.Fatalf("referenced IDs = %v", ids)
	}
	if !strings.Contains(updated.Value(), "[Pasted TXT1]") {
		t.Fatalf("composer value = %q", updated.Value())
	}
}
