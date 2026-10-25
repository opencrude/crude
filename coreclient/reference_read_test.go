package coreclient

import (
	"testing"

	"crude/coreprotocol"
	"crude/sessiondb"
)

func TestReferenceReadValidatesStrippedMetadataAgainstCompleteRevision(t *testing.T) {
	expected := coreprotocol.ReferenceView{Reference: sessiondb.Reference{
		ID: "exact", SessionID: "session", Kind: sessiondb.KindPastedText,
		Label: "[Pasted Text1]", Content: "canonical content must survive reading",
	}, Revision: "complete-entity-revision"}
	metadata := expected
	metadata.Reference.Content = ""
	for _, mode := range []string{"valid", "content-only-change", "rename", "foreign", "missing-revision", "unstripped"} {
		t.Run(mode, func(t *testing.T) {
			input := metadata
			switch mode {
			case "content-only-change":
				input.Revision = "new-content-revision"
			case "rename":
				input.Reference.Label = "another label"
			case "foreign":
				input.Reference.SessionID = "foreign-session"
			case "missing-revision":
				input.Revision = ""
			case "unstripped":
				input.Reference.Content = "unexpected duplicate bytes"
			}
			if err := validateReferenceRead(expected, input); (err == nil) != (mode == "valid") {
				t.Fatalf("read metadata %s validation: %v", mode, err)
			}
			if expected.Reference.Content != "canonical content must survive reading" {
				t.Fatal("validation changed the canonical reference")
			}
		})
	}
}
