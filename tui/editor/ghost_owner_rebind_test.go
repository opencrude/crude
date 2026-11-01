package editor

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/document"
)

func TestEditorGhostAttachmentReplacementRevokesUnchangedSourceCandidate(t *testing.T) {
	e := newInlayTestEditor(t, "base")
	if err := e.BindDocumentOwner(document.Owner{ClientID: "same-shell", Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	e.SetCursorPosition(0, 4)
	ghostEntry(t, e, 4, "candidate")
	if err := e.BindDocumentOwner(document.Owner{ClientID: "same-shell", Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	if _, _, offered := e.GhostText(); offered {
		t.Fatal("replacement attachment retained a prior owner's prediction acceptance authority")
	}
	e.HandleEvent(tea.KeyPressMsg{Code: tea.KeyTab})
	if e.Value() != "base    " {
		t.Fatal("replacement attachment revived candidate acceptance instead of ordinary indentation")
	}
}
