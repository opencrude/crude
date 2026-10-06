package input

import (
	tea "charm.land/bubbletea/v2"
	"strings"
	"testing"

	"crude/sessiondb"
	"crude/tui/theme"
)

type pasteReferenceStore struct {
	references []sessiondb.Reference
}

func TestPastePromotionPreservesSmallProseWithoutFloodingUnboundInput(t *testing.T) {
	for _, test := range []struct {
		name, content string
		promote       bool
	}{
		{"short multiline", "first line\nsecond line", false},
		{"byte boundary", strings.Repeat("x", 4096), true},
		{"multibyte byte boundary", strings.Repeat("界", 1400), true},
		{"tiny tall trace", strings.Repeat("frame\n", 19) + "tail", false},
		{"small trace", strings.Repeat("frame\n", 18) + "tail", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := NewModel(theme.DefaultDark())
			updated, _, event := model.Update(tea.PasteMsg{Content: test.content}, false, false)
			request, promoted := event.(PasteAttachmentEvent)
			if promoted != test.promote {
				t.Fatalf("promoted=%v want=%v", promoted, test.promote)
			}
			if promoted {
				if request.Content != test.content || updated.Value() != "" {
					t.Fatal("pending promoted paste lost content or flooded input")
				}
			} else if updated.Value() != test.content {
				t.Fatal("short paste did not retain exact literal text")
			}
		})
	}
}

func (s *pasteReferenceStore) DeleteReference(string) error { return nil }
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

func TestLargePasteRequestsOffUIStorageBeforeChipInsertion(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	store := &pasteReferenceStore{}
	model.SetReferenceStore(store)

	content := strings.Repeat("large paste ", pasteInlineThreshold)
	updated, _, event := model.handlePaste(content)
	request, ok := event.(PasteAttachmentEvent)
	if !ok || request.Content != content || updated.Value() != "" || len(store.references) != 0 || PastedTextInline(content) {
		t.Fatal("large paste was not retained as an off-UI request")
	}
	ref := sessiondb.Reference{ID: "ref-large", Label: "[Pasted Text1]", Kind: sessiondb.KindPastedTextFile}
	store.references = append(store.references, ref)
	if !updated.CompletePaste(request) || updated.CompletePaste(request) {
		t.Fatal("paste acknowledgement was not exact-once")
	}
	updated.InsertReference(ref)
	ids := updated.ReferencedIDs()
	if len(ids) != 1 || ids[0] != "ref-large" {
		t.Fatalf("referenced IDs = %v", ids)
	}
	if !strings.Contains(updated.Value(), "[Pasted Text1]") {
		t.Fatalf("composer value = %q", updated.Value())
	}
}

func TestShortMultilinePasteStaysLiteral(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	store := &pasteReferenceStore{}
	model.SetReferenceStore(store)

	content := "first line\nsecond line"
	updated, _, _ := model.handlePaste(content)
	if len(store.references) != 0 {
		t.Fatalf("short multiline paste became a reference: %+v", store.references)
	}
	if got := updated.Value(); got != content {
		t.Fatalf("pasted content = %q, want %q", got, content)
	}
}

func TestLargeInlinePasteCreatesContentReference(t *testing.T) {
	model := NewModel(theme.DefaultDark())
	store := &pasteReferenceStore{}
	model.SetReferenceStore(store)

	content := strings.Repeat("x", pasteReferenceThreshold)
	updated, _, event := model.handlePaste(content)
	request, ok := event.(PasteAttachmentEvent)
	if !ok || request.Content != content || len(store.references) != 0 || updated.Value() != "" || !PastedTextInline(content) {
		t.Fatal("inline-size attachment was not retained as an off-UI request")
	}
}
