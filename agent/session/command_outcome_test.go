package session

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/types/command"
	"crude/workspace/fileio"
)

// A background command that settles on its own reaches its owning agent as a
// durable runtime delivery (admitted at a safe boundary or waking it), so the
// agent never has to poll. An explicit kill is already known to its author.
func TestSettledBackgroundCommandIsDeliveredToItsOwner(t *testing.T) {
	s, store, _, child := collaborationContractFixture(t)
	started := time.Now()
	code := 254
	output := &command.Result{Command: "npm exec --yes --package=pkg@1.0.0 --call 'exit 0'", PID: 41, WallTimeSeconds: 47, ExitCode: &code,
		Output: "\x1b[31mFAILED\x1b[0m\r\n    retained stderr\n", OutputDescriptor: command.OutputDescriptor{Handle: "mcp_prepare", Status: command.Failed, TotalBytes: 120}}
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{
		Handle: "mcp_prepare", Command: "npm exec --yes --package=pkg@1.0.0 --call 'exit 0'", PID: 41,
		Status: fileio.AsyncFailed, ExitCode: 254, OutputBytes: 120, StartedAt: started,
		EndedAt: started.Add(47 * time.Second), Duration: 47 * time.Second,
		SettledOutput: output,
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
		!strings.Contains(event.Body, `"mcp_prepare"`) || !strings.Contains(event.Body, `"exit_code":254`) ||
		!strings.Contains(event.Body, `"wall_time_seconds":47`) {
		t.Fatalf("command outcome event = %+v", event)
	}
	// Short complete output is in the body the agent reads, so it does not
	// spend a turn reading it back.
	if !strings.Contains(event.Body, "retained stderr") || strings.Contains(event.Body, "read_async_output") {
		t.Fatalf("short output was not delivered with the outcome: %s", event.Body)
	}
	if event.Command == nil || event.Command.Handle != "mcp_prepare" || event.Command.ExitCode != 254 || !event.Command.ExitKnown || event.Command.OutputBytes != 120 {
		t.Fatal("journal dropped typed command evidence")
	}
	if event.Command.Output == nil || event.Command.Output.Output != output.Output {
		t.Fatal("journal lost captured output when the agent never read it")
	}
	attribution := collaborationAttribution(event)
	clone := cloneCollaborationAttribution(attribution)
	if !reflect.DeepEqual(clone.Command, event.Command) {
		t.Fatal("persisted attribution lost command evidence")
	}
	attribution.Command.Handle = "changed"
	if clone.Command.Handle != "mcp_prepare" || event.Command.Handle != "mcp_prepare" {
		t.Fatal("command evidence aliases its producer or another observer")
	}
	// A duplicate settle observation is idempotent, never a second wake.
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{
		Handle: "mcp_prepare", Command: "npm exec --yes --package=pkg@1.0.0 --call 'exit 0'", PID: 41,
		Status: fileio.AsyncFailed, ExitCode: 254, OutputBytes: 120, StartedAt: started,
		EndedAt: started.Add(47 * time.Second), Duration: 47 * time.Second,
		SettledOutput: output,
	})
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("expected exactly one journaled outcome, got %d", len(page.Events))
	}
}

func TestControlHeavyCommandOutputFitsDurableOutcome(t *testing.T) {
	s, store, _, child := collaborationContractFixture(t)
	// Raw NUL output expanded to 192 KiB of JSON escapes and exceeded the
	// event's 128 KiB limit, silently losing the only completion evidence.
	raw := strings.Repeat("\x00", fileio.DefaultOutputChunkBytes)
	output := &command.Result{Command: "control producer", Output: raw,
		OutputDescriptor: command.OutputDescriptor{Handle: "binary", Status: command.Completed, TotalBytes: len(raw)}}
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{Handle: "binary", Command: "binary producer", PID: 43, Status: fileio.AsyncCompleted, OutputBytes: len(raw), SettledOutput: output})
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil || len(page.Events) != 1 || page.Events[0].Command == nil || page.Events[0].Command.Output == nil {
		t.Fatalf("control-heavy completion output was not journalled: events=%d error=%v", len(page.Events), err)
	}
	if got := page.Events[0].Command.Output; got.Output != raw || got.OutputEncoding != "" {
		t.Fatal("journal changed or unnecessarily encoded UTF-8 stdout evidence")
	}
}

func TestBackgroundDeliveryUsesStopFactsNotDiagnosticWords(t *testing.T) {
	s, store, _, child := collaborationContractFixture(t)
	started := time.Now()
	for _, info := range []fileio.AsyncCmdInfo{
		{Handle: "stopped", PID: 44, Status: fileio.AsyncKilled, Error: "timed out", StartedAt: started},
		{Handle: "deadline", PID: 45, Status: fileio.AsyncKilled, TimedOut: true, Error: "killed", StartedAt: started},
		{Handle: "unverified", PID: 46, Status: fileio.AsyncFailed, Uncertain: true, Error: "cancelled", StartedAt: started},
	} {
		s.publishCommandOutcome(child.ID, info)
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("stop wording changed journal delivery: %+v %v", page, err)
	}
	if page.Events[0].Command == nil || !page.Events[0].Command.TimedOut ||
		page.Events[1].Command == nil || !page.Events[1].Command.Uncertain {
		t.Fatal("journal lost timeout or uncertainty facts")
	}
}

// Output that is long or only partly shown stays out of the body: the agent
// gets the status and the exact cursor to read it.
func TestLongCommandOutcomeKeepsTheReadCursor(t *testing.T) {
	s, store, _, child := collaborationContractFixture(t)
	for i, output := range []*command.Result{
		{Output: strings.Repeat("line of test output\n", 2000), OutputDescriptor: command.OutputDescriptor{Handle: "long", Status: command.Completed, TotalBytes: 40000}},
		{Output: "head ... tail", OutputDescriptor: command.OutputDescriptor{Handle: "partial", Status: command.Completed, TotalBytes: 90000, OmittedBytes: 80000}},
	} {
		s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{Handle: output.OutputDescriptor.Handle, PID: 50 + i,
			Status: fileio.AsyncCompleted, OutputBytes: output.OutputDescriptor.TotalBytes, StartedAt: time.Now(), SettledOutput: output})
	}
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil || len(page.Events) != 2 {
		t.Fatalf("events=%+v err=%v", page.Events, err)
	}
	for _, event := range page.Events {
		if !strings.Contains(event.Body, "read_async_output") || strings.Contains(event.Body, "line of test output") || strings.Contains(event.Body, "head ... tail") {
			t.Fatalf("long or partial output in the body: %.300s", event.Body)
		}
		if event.Command == nil || event.Command.Output == nil {
			t.Fatal("the journal lost the typed excerpt")
		}
	}
}
