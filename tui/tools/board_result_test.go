package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/board"
)

func TestBoardResultPreservesPeerIdentityBodyAndPageFacts(t *testing.T) {
	message := board.Message{ID: "board-message-7", BoardID: "review", ProjectID: "/project", Sequence: 7,
		SenderID: "agent-p", SenderName: "Patrick", Type: "RESULT", To: "main", RecipientID: "root",
		RecipientAvailability: "active", Recipients: []board.Recipient{{ID: "root", Name: "Olivia", Availability: "active"}},
		Body:    strings.Repeat("Full findings with unicode 👩‍💻 and tests. ", 12) + "retained ending",
		ReplyTo: "board-message-2", ThreadID: "board-message-1", References: []board.Reference{{Kind: "file", ID: "report.md"}},
	}
	post, _ := json.Marshal(board.PostResult{Message: message, Duplicate: true})
	result := BuildStructuredResult("board_post", string(post), `{"to":"main","type":"RESULT"}`)
	if len(result.Sections) != 1 || result.Sections[0].Body != message.Body {
		t.Fatalf("post body shortened: %+v", result)
	}
	text := memoryEvidenceText(result)
	for _, want := range []string{"Patrick → Olivia", "stored", "already recorded", "not delivered or read", "board-message-7", "report.md"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	if got := BoardPostOperand(string(post), `{"to":"wrong-agent"}`); got != "" {
		t.Fatalf("conflicting recipient renamed title: %q", got)
	}
	page, _ := json.Marshal(board.Page{BoardID: "review", ProjectID: "/project", Messages: []board.Message{message},
		Roster: []board.Member{{ID: "agent-p", Name: "Patrick", ParentID: "root", Availability: "finished"}},
		Cursor: 7, LatestSequence: 9, AcceptedThrough: 3, AcceptThrough: 7, HumanReadThrough: 2, HasMore: true, Bytes: 900})
	result = BuildStructuredResult("board_read", string(page), `{}`)
	text = memoryEvidenceText(result)
	for _, want := range []string{"1 message", "1 participant", "more available", "Cursor: 7", "Latest sequence: 9", "Context accepted through: 3", "Human read through: 2", "finished", "retained ending"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing page evidence %q: %s", want, text)
		}
	}
}

func TestBoardEmptyAndMalformedResultsDoNotInventSuccess(t *testing.T) {
	result := BuildStructuredResult("board_read", `{"board_id":"review","roster":[],"messages":[],"cursor":0}`, `{}`)
	if !strings.Contains(memoryEvidenceText(result), "No Board messages") {
		t.Fatal("empty board page has no readable empty state")
	}
	for _, content := range []string{`{"message":`, `{"error":"not a member"}`, "Error: access denied"} {
		result = BuildStructuredResult("board_post", content, `{}`)
		if strings.Contains(result.Summary, "stored") {
			t.Fatalf("invalid result asserted a stored post: %+v", result)
		}
	}
}
