package session

import (
	"encoding/base64"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/collaboration"
	"crude/fileio"
	"crude/types/command"
)

// A background command that settles on its own reaches its owning agent as a
// durable runtime delivery (admitted at a safe boundary or waking it), so the
// agent never has to poll. An explicit kill is already known to its author.
func TestSettledBackgroundCommandIsDeliveredToItsOwner(t *testing.T) {
	s, store, _, child := collaborationContractFixture(t)
	started := time.Now()
	output := &command.Result{Output: "\x1b[31mFAILED\x1b[0m\r\n    retained stderr\n", OutputDescriptor: command.OutputDescriptor{Handle: "mcp_prepare", Status: command.Failed, TotalBytes: 120}}
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
		!strings.Contains(event.Body, "read_async_output") || !strings.Contains(event.Body, "47s") {
		t.Fatalf("command outcome event = %+v", event)
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
	output := &command.Result{Command: "binary producer", Output: base64.StdEncoding.EncodeToString([]byte(raw)), OutputEncoding: "base64",
		OutputDescriptor: command.OutputDescriptor{Handle: "binary", Status: command.Completed, TotalBytes: len(raw)}}
	s.publishCommandOutcome(child.ID, fileio.AsyncCmdInfo{Handle: "binary", Command: "binary producer", PID: 43, Status: fileio.AsyncCompleted, OutputBytes: len(raw), SettledOutput: output})
	page, err := store.Page(t.Context(), collaboration.PageQuery{RecipientID: child.ID})
	if err != nil || len(page.Events) != 1 || page.Events[0].Command == nil || page.Events[0].Command.Output == nil {
		t.Fatalf("control-heavy completion output was not journalled: events=%d error=%v", len(page.Events), err)
	}
	decoded, err := base64.StdEncoding.DecodeString(page.Events[0].Command.Output.Output)
	if err != nil || string(decoded) != raw {
		t.Fatal("journal changed binary stdout evidence")
	}
}
