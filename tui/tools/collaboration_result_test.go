package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
)

func TestCollaborationResultsRemainReadableAndExact(t *testing.T) {
	event := collaboration.Event{
		ID: "event-7", Sequence: 7, WorkspaceID: "/project",
		SenderID: "agent-p", SenderName: "Patrick",
		Recipients: []collaboration.Recipient{{ID: "root", Name: "Olivia", State: collaboration.AgentAvailable, Presence: collaboration.RuntimeAttached}},
		Origin:     collaboration.OriginPeer, Kind: collaboration.EventWorkRequest,
		Body: "Run the focused suite and report exact failures.", ThreadID: "event-7",
		RequestID: "request-1", RequestRevision: 2,
		References: []collaboration.Reference{{Kind: "file", ID: "board_test.go"}},
		CreatedAt:  time.Unix(123, 0),
	}
	encoded, _ := json.Marshal(collaboration.PublishResult{
		Event: event,
		Deliveries: []collaboration.Delivery{{
			ID: "delivery-1", EventID: event.ID, RecipientID: "root",
			State: collaboration.DeliveryPending, Version: 1,
		}},
	})
	result := BuildStructuredResult("agent_send", string(encoded), `{}`)
	plain := structuredPlain(result)
	for _, expected := range []string{"Work request", "Patrick → Olivia", "focused suite", "request-1", "board_test.go", "pending"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("missing %q: %+v", expected, result)
		}
	}
	if strings.Contains(plain, `{"`) {
		t.Fatalf("raw JSON leaked: %s", plain)
	}
	if operand := CollaborationOperand("agent_send", string(encoded), `{}`); operand != "Work request → Olivia" {
		t.Fatalf("operand = %q", operand)
	}

	page, _ := json.Marshal(collaboration.Page{
		Events: []collaboration.Event{event}, Cursor: 7, Snapshot: 8,
		LatestSequence: 9, HumanReadThrough: 3, HasMore: true,
	})
	result = BuildStructuredResult("board_read", string(page), `{}`)
	plain = structuredPlain(result)
	for _, expected := range []string{"1 event", "more in snapshot", "Snapshot", "8", "Human read through"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("missing page fact %q: %+v", expected, result)
		}
	}
}

func TestReportResultPreservesBodyRevisionAndEvidence(t *testing.T) {
	artifact := collaboration.Artifact{
		ID: "report-1", Revision: 2, WorkspaceID: "/project", AuthorID: "researcher",
		Kind: collaboration.ReportFinal, Summary: "Parser verified",
		Body:   "Every parser case passed.\n\nNo unresolved blockers.",
		Digest: "sha256-evidence", SupersedesRevision: 1,
		References: []collaboration.Reference{{Kind: "test", ID: "parser-suite"}},
	}
	encoded, _ := json.Marshal(collaboration.PublishReportResult{
		Artifact: artifact,
		Publish: collaboration.PublishResult{
			Event:      collaboration.Event{ID: "event-report", Kind: collaboration.EventReportPublished},
			Deliveries: []collaboration.Delivery{{ID: "delivery", State: collaboration.DeliveryPending}},
		},
	})
	result := BuildStructuredResult("publish_report", string(encoded), `{}`)
	plain := structuredPlain(result)
	for _, expected := range []string{"revision 2", "Parser verified", "Every parser case passed", "sha256-evidence", "parser-suite"} {
		if !strings.Contains(plain, expected) {
			t.Fatalf("missing %q: %+v", expected, result)
		}
	}
}

func structuredPlain(result StructuredResult) string {
	var text strings.Builder
	text.WriteString(result.Summary)
	for _, section := range result.Sections {
		text.WriteString("\n" + section.Title + "\n" + section.Metadata + "\n" + section.Body)
		for _, field := range section.Details {
			text.WriteString("\n" + field.Label + ": " + field.Value)
		}
	}
	return text.String()
}
