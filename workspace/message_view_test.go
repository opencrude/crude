package workspace

import (
	"testing"

	"crude/provider"
	"crude/sessiondb"
)

func TestTakeHydratedDataIsOneShot(t *testing.T) {
	sess := &Session{
		hydratedMessages: []ChatMessageView{{Role: "user", Content: "hello"}},
		hydratedRefs:     []sessiondb.Reference{{ID: "ref-1"}},
	}

	if got := sess.TakeHydratedMessages(); len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("unexpected hydrated messages: %#v", got)
	}
	if got := sess.TakeHydratedMessages(); len(got) != 0 {
		t.Fatalf("hydrated messages were returned twice: %#v", got)
	}
	if got := sess.TakeHydratedRefs(); len(got) != 1 || got[0].ID != "ref-1" {
		t.Fatalf("unexpected hydrated references: %#v", got)
	}
	if got := sess.TakeHydratedRefs(); len(got) != 0 {
		t.Fatalf("hydrated references were returned twice: %#v", got)
	}
}

func TestConvertMessagesForDisplayUsesOriginalReferenceCaption(t *testing.T) {
	message := provider.NewMultiPartMessage("user", []provider.ContentPart{
		{Type: "text", Text: "what is this?"},
		{Type: "image_url", ImageURL: &provider.ImageURL{URL: "data:image/png;base64,eA=="}},
	})
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: message,
		Meta: sessiondb.DisplayMeta{
			DisplayRole:     "user",
			DisplayContent:  "[Pasted IMG1] what is this?",
			ReferenceLabels: []string{"[Pasted IMG1]"},
		},
	}})
	if len(views) != 1 || views[0].Content != "[Pasted IMG1] what is this?" {
		t.Fatalf("hydrated views = %+v", views)
	}
	if len(views[0].ReferenceLabels) != 1 || views[0].ReferenceLabels[0] != "[Pasted IMG1]" {
		t.Fatalf("hydrated reference labels = %v", views[0].ReferenceLabels)
	}
}

func TestConvertMessagesForDisplayLegacyMultipartUsesPromptPartOnly(t *testing.T) {
	message := provider.NewMultiPartMessage("user", []provider.ContentPart{
		{Type: "text", Text: "[Pasted Text1] summarize"},
		{Type: "text", Text: "private pasted payload"},
	})
	views := ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg:  message,
		Meta: sessiondb.DisplayMeta{DisplayRole: "user"},
	}})
	if len(views) != 1 || views[0].Content != "[Pasted Text1] summarize" {
		t.Fatalf("legacy hydrated views = %+v", views)
	}
}
