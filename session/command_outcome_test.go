package session

import (
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/fileio"
)

// A background command that settles on its own reaches its owning agent as a
// durable runtime delivery (admitted at a safe boundary or waking it), so the
// agent never has to poll. An explicit kill is already known to its author.
func TestSettledBackgroundCommandIsDeliveredToItsOwner(t *testing.T) {
	s, store, _, child := collaborationContractFixture(t)
	started := time.Now()
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{
		Handle: "mcp_prepare", Command: "npm exec --yes --package=pkg@1.0.0 --call 'exit 0'", PID: 41,
		Status: fileio.AsyncFailed, ExitCode: 254, OutputBytes: 120, StartedAt: started,
		EndedAt: started.Add(47 * time.Second), Duration: 47 * time.Second,
	})
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{
		Handle: "dev_server", Command: "npm run dev", PID: 42, Status: fileio.AsyncKilled, Error: "killed", StartedAt: started,
	})
	pending, err := store.ReservePending(t.Context(), collaboration.ReserveRequest{RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch})
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	event := pending[0].Event
	if event.Origin != collaboration.OriginRuntime || event.Kind != collaboration.EventCommandOutcome ||
		!strings.Contains(event.Body, `"mcp_prepare"`) || !strings.Contains(event.Body, "exit_code=254") ||
		!strings.Contains(event.Body, "read_async_output") || !strings.Contains(event.Body, "47s") {
		t.Fatalf("command outcome event = %+v", event)
	}
	// A duplicate settle observation is idempotent, never a second wake.
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{
		Handle: "mcp_prepare", Command: "npm exec --yes --package=pkg@1.0.0 --call 'exit 0'", PID: 41,
		Status: fileio.AsyncFailed, ExitCode: 254, OutputBytes: 120, StartedAt: started,
		EndedAt: started.Add(47 * time.Second), Duration: 47 * time.Second,
	})
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("expected exactly one journaled outcome, got %d", len(page.Events))
	}
}
