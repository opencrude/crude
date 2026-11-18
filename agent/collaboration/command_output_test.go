package collaboration

import (
	"errors"
	"strings"
	"testing"

	"crude/types/command"
)

func TestCommandEvidenceBudgetsJSONEscapesWithoutChangingText(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	raw := strings.Repeat("\x00", 32<<10)
	request := PublishRequest{SenderID: root.ID, RecipientIDs: []string{child.ID}, Origin: OriginRuntime,
		Kind: EventCommandOutcome, IdempotencyKey: "control-output", Body: "Command completed",
		Command: &command.Outcome{Handle: "job", Status: "completed", OutputBytes: len(raw),
			Output: &command.Result{Output: raw, OutputDescriptor: command.OutputDescriptor{Status: command.Completed, TotalBytes: len(raw)}}},
	}
	first, err := store.Publish(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Publish(t.Context(), request)
	if err != nil || !again.Duplicate || again.Event.ID != first.Event.ID {
		t.Fatal("command evidence lost idempotency", err)
	}
	page, err := store.Page(t.Context(), PageQuery{RecipientID: child.ID})
	if err != nil || len(page.Events) != 1 || page.Cursor != first.Event.Sequence || page.HasMore || page.Bytes > MaxPageBytes {
		t.Fatal("escaped evidence cannot advance its bounded Board page", err)
	}
	if output := page.Events[0].Command.Output; output.Output != raw || output.OutputEncoding != "" {
		t.Fatal("storage changed the captured text")
	}
	reserved, err := store.ReservePending(t.Context(), ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(reserved) != 1 || reserved[0].Event.Command.Output.Output != raw {
		t.Fatal("delivery changed or lost the evidence", err)
	}

	// The allowance applies to serialized command evidence, not arbitrary notes,
	// and cannot publish an event that permanently blocks the Board cursor.
	request.IdempotencyKey, request.Kind, request.Command = "large-note", EventPeerNote, nil
	request.Body = strings.Repeat("\x00", 32<<10)
	if _, err := store.Publish(t.Context(), request); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("ordinary event size limit changed: %v", err)
	}
	request.IdempotencyKey, request.Kind, request.Body = "oversized-evidence", EventCommandOutcome, "Command completed"
	request.Command = &command.Outcome{Handle: "job", Output: &command.Result{Output: strings.Repeat("\x00", 48<<10)}}
	if _, err := store.Publish(t.Context(), request); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("unpageable command evidence was accepted: %v", err)
	}
}
