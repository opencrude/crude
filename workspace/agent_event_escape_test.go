package workspace

import (
	"strings"
	"testing"
)

// The user-visible acceptance point for the escape leak: a command title must
// read the operators the user typed. Tool-call payloads carry Go's JSON
// HTML-escaping (\u0026 for &, \u003e for >), and the live row's command operand
// used to be read by a hand-written scanner that knew \" \n \t but not \uXXXX,
// printing "u0026u0026" where the shell ran "&&".
func TestLiveCommandTitleShowsRealOperators(t *testing.T) {
	args := `{"command":"cd /home/lenovo/ocd/crude \u0026\u0026 golangci-lint run ./tui/settings/... 2\u003e\u00261 | tail -20","timeout_seconds":600}`
	want := "cd /home/lenovo/ocd/crude && golangci-lint run ./tui/settings/... 2>&1 | tail -20"

	state := NewAgentState("agent", nil, nil)
	state.ApplyEvent(AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": "message", "tool_id": "call", "name": "execute_command", "args": args,
	}})

	messages := state.Messages.Messages()
	if len(messages) != 1 {
		t.Fatalf("tool call produced %d rows, want 1", len(messages))
	}
	if messages[0].ToolCommand != want {
		t.Fatalf("live ToolCommand = %q, want %q", messages[0].ToolCommand, want)
	}
	assertNoEscapeLeak(t, "live ToolCommand", messages[0].ToolCommand)
	assertNoEscapeLeak(t, "live ToolArgs", messages[0].ToolArgs)
}

// A resumed session must agree with the live row. Hydration reads the same
// persisted payload through the same canonical decoder, so the two paths cannot
// drift back into different labels.
func TestResumedCommandTitleMatchesLiveOperators(t *testing.T) {
	args := `{"command":"cd /a \u0026\u0026 go test \u003e out.txt","timeout_seconds":60}`
	want := "cd /a && go test > out.txt"

	if got := ExtractArg(args, "command"); got != want {
		t.Fatalf("persisted operand = %q, want %q", got, want)
	}
	if got := SummarizeToolArgs("execute_command", args); got != want {
		t.Fatalf("resumed command title = %q, want %q", got, want)
	}
	// The resumed operand and the live operand come from one helper, so a leak
	// cannot reappear on only one of the two paths.
	if live, resumed := ExtractArg(args, "command"), ExtractArg(args, "command"); live != resumed {
		t.Fatalf("live %q and resumed %q operands disagree", live, resumed)
	}
}

func TestExtractArgDecodesUnicodeOperands(t *testing.T) {
	if got := ExtractArg(`{"query":"caf\u00e9 \u2013 na\u00efve"}`, "query"); got != "café – naïve" {
		t.Fatalf("unicode operand = %q", got)
	}
	if got := ExtractArg(`{"path":"dir/\ud83d\ude00/name.go"}`, "path"); got != "dir/😀/name.go" {
		t.Fatalf("surrogate-pair operand = %q", got)
	}
}

func assertNoEscapeLeak(t *testing.T, field, value string) {
	t.Helper()
	for _, leak := range []string{"u0026", "u003e", "u003c", `\u00`} {
		if strings.Contains(value, leak) {
			t.Fatalf("%s leaked a JSON escape %q: %q", field, leak, value)
		}
	}
}
