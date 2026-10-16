package tools

import "testing"

// The label authority is shared by the live transcript and a resumed session, so
// it must read both encodings of a tool call's arguments, and it must not lose
// the value behind the colon (the previous copy consumed the space that followed
// "key:" and returned an empty label for every summary-form payload).
func TestExtractArgReadsBothArgumentEncodings(t *testing.T) {
	cases := []struct {
		name string
		args string
		key  string
		want string
	}{
		{"json object", `{"path":"tools/dispatcher.go","old_string":"a"}`, "path", "tools/dispatcher.go"},
		{"summary form", `path: tools/dispatcher.go old_string: a`, "path", "tools/dispatcher.go"},
		{"quoted multi-word value", `pattern: "func main" path: tui/tools`, "pattern", "func main"},
		{"summary form without value", `pattern: path: tui/tools`, "pattern", ""},
		{"missing key", `path: x.go`, "query", ""},
		{"trailing value", `query: undo projection`, "query", "undo"},
		{"unterminated quote", `pattern: "func main`, "pattern", "func main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractArg(c.args, c.key); got != c.want {
				t.Fatalf("ExtractArg(%q, %q) = %q, want %q", c.args, c.key, got, c.want)
			}
		})
	}
}

// A tool-call payload arrives with Go's JSON HTML-escaping applied, so a shell
// "&&" is written "\u0026\u0026". Every operand reader must decode escapes through
// the shared JSON decoder; a hand-written scanner that only knew \" \n \t printed
// the literal "u0026u0026" in live command titles.
func TestExtractArgDecodesJSONEscapesAndUnicode(t *testing.T) {
	cases := []struct {
		name string
		args string
		key  string
		want string
	}{
		{"shell operators", `{"command":"cd /a \u0026\u0026 go test \u003e out.txt","timeout_seconds":60}`, "command", "cd /a && go test > out.txt"},
		{"redirect and pipe", `{"command":"go build ./... 2\u003e\u00261 | tail -20"}`, "command", "go build ./... 2>&1 | tail -20"},
		{"multibyte unicode", `{"query":"caf\u00e9 \u2013 na\u00efve"}`, "query", "café – naïve"},
		{"surrogate pair", `{"query":"\ud83d\ude00 emoji"}`, "query", "😀 emoji"},
		{"angle brackets", `{"command":"diff \u003c a \u003e b"}`, "command", "diff < a > b"},
		{"quote and backslash inside value", `{"command":"echo \"hi\" \\ there"}`, "command", `echo "hi" \ there`},
		{"plain operators stay intact", `{"command":"a && b"}`, "command", "a && b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ExtractArg(c.args, c.key); got != c.want {
				t.Fatalf("ExtractArg(%q, %q) = %q, want %q", c.args, c.key, got, c.want)
			}
		})
	}
}

// A stream is observed while its envelope is still open. An unfinished envelope
// is not a key/value summary, so the operand stays empty rather than being
// guessed from a partial payload; the completed envelope supplies it. This is the
// pre-existing contract; the escape fix must not change it.
func TestExtractArgLeavesAnUnfinishedEnvelopeUnlabelled(t *testing.T) {
	for _, args := range []string{
		`{"command":"cd /a \u0026\u0026 go test`,
		`{"path":"tui/ui/list`,
		`{"command":"cd /a \u0026\u0026 b 2\u003e\u00261`,
	} {
		if got := ExtractArg(args, "command"); got != "" {
			t.Fatalf("ExtractArg(%q, %q) = %q, want empty for an unfinished envelope", args, "command", got)
		}
	}
}

func TestSummarizeArgsLabelsOperands(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args string
		want string
	}{
		{"plan id", "update_plan", `plan_id: p1 steps: map[audit:done]`, "p1"},
		{"plan id json", "get_plan", `{"plan_id":"plan-123"}`, "plan-123"},
		{"grep pattern and scope", "grep_files", `pattern: "func main" path: tui/tools`, `"func main" in tui/tools`},
		{"grep pattern only", "grep_files", `pattern: "func main"`, `"func main"`},
		{"file path", "edit_file", `new_string: d old_string: b path: x.go`, "x.go"},
		{"query", "web_search", `query: "golang tui"`, "golang tui"},
		{"command", "execute_command", `command: "go test ./tools"`, "go test ./tools"},
		{"handle", "check_async_command", `handle: build-1`, "build-1"},
		{"wait seconds", "wait", `seconds: 5`, "5s"},
		{"glob pattern", "glob_files", `pattern: "**/*.go"`, "**/*.go"},
		{"unknown falls back to the payload", "mystery_tool", `alpha: beta`, "alpha: beta"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := SummarizeArgs(c.tool, c.args); got != c.want {
				t.Fatalf("SummarizeArgs(%q, %q) = %q, want %q", c.tool, c.args, got, c.want)
			}
		})
	}
}

// Semantic operands reach layout intact; only unknown-payload fallbacks use a
// summary budget. The renderer owns wrapping at the actual terminal width.
func TestSummarizeArgsPreservesKnownOperands(t *testing.T) {
	long := ""
	for i := 0; i < 90; i++ {
		long += "x"
	}
	if got := SummarizeArgs("execute_command", `command: "`+long+`"`); got != long {
		t.Fatalf("command operand shortened: %q", got)
	}
	if got := SummarizeArgs("mystery_tool", long); len([]rune(got)) != 60 {
		t.Fatalf("fallback label = %d runes, want 60", len([]rune(got)))
	}
}

func TestStructuredToolArgumentsNeverBecomeTitlePayloads(t *testing.T) {
	for _, tc := range []struct{ name, tool, args, want string }{
		{"wait JSON", "agent_wait", `{"run_id":"run-8f21","extra":{"state":"running"}}`, "run-8f21"},
		{"wait nested JSON string is not an identity", "agent_wait", `{"run_id":"{\"private\":\"not a run\"}"}`, ""},
		{"control JSON", "agent_control", `{"operation":"cancel","agent_id":"agent-8f21"}`, "cancel · agent-8f21"},
		{"spawn normalized snapshot", "agent_start", "Inspect renderer", "Inspect renderer"},
		{"spawn category", "agent_start", `{"category":"researcher","description":"Inspect renderer"}`, "Researcher · Inspect renderer"},
		{"list agents", "agent_list", `{"query":"renderer"}`, "renderer"},
		{"empty object", "agent_list", `{}`, ""},
		{"empty plan", "get_plan", `{}`, ""},
		{"plan missing id", "update_plan", `{"objective":"Do work","steps":[{"description":"Inspect"}]}`, ""},
		{"question", "question", `{"questions":[{"prompt":"Choose"}]}`, ""},
		{"memory title", "remember", `{"title":"Preferred layout","content":"Keep narrow screens readable"}`, "Preferred layout"},
		{"memory update", "update_memory", `{"id":"memory-1","content":"updated"}`, "memory-1"},
		{"memory forget", "forget_memory", `{"id":"memory-2"}`, "memory-2"},
		{"report", "publish_report", `{"summary":"Renderer review","body":"Large JSON report body"}`, "Renderer review"},
		{"patch wrapper", "apply_patch", `{"input":"not a patch"}`, ""},
		{"unknown object", "plugin_tool", `{"mystery":{"secret":"do not paint"}}`, ""},
		{"unknown array", "plugin_tool", `[{"secret":"do not paint"}]`, ""},
		{"unknown partial object", "plugin_tool", `{"secret":"do not paint`, ""},
		{"unknown partial array", "plugin_tool", `[{"secret":"do not paint`, ""},
		{"string containing JSON remains a command", "execute_command", `{"command":"echo '{\"answer\":42}'"}`, `echo '{"answer":42}'`},
		{"unstructured unknown keeps readable label", "plugin_tool", `alpha: beta`, "alpha: beta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := SummarizeArgs(tc.tool, tc.args); got != tc.want {
				t.Fatalf("title operand = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtractArgNeverFindsAFieldInsideUnrelatedJSONText(t *testing.T) {
	for _, input := range []string{
		`{"description":"path: /do-not-use"}`,
		`{"nested":{"path":"/do-not-use"}}`,
		`{"path":{"nested":"/do-not-use"}}`,
	} {
		if got := ExtractArg(input, "path"); got != "" {
			t.Fatalf("found fictitious path %q inside %s", got, input)
		}
	}
}

func TestToolDisplayNameRejectsStructuredOrUnboundedNames(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"agent_wait", "agent_wait"},
		{"functions.read_file", "functions.read_file"},
		{`{"private_id":"agent-8f21"}`, "tool"},
		{"read_file\n{\"secret\":1}", "tool"},
		{"", "tool"},
	} {
		if got := DisplayToolName(tc.name); got != tc.want {
			t.Fatalf("name %q displays as %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestRelatedToolFamilyTitlesUseWholeSemanticOperands(t *testing.T) {
	for _, tc := range []struct{ tool, input, want string }{
		{"agent_send", `{"recipient_ids":["Patrick"],"kind":"request","request_id":"review-42","body":"Run the tests"}`, "request"},
		{"agent_respond", `{"disposition":"deferred","request_id":"review-42"}`, "deferred · review-42"},
		{"agent_control", `{"operation":"cancel","agent_id":"agent-8f21"}`, "cancel · agent-8f21"},
		{"publish_report", `{"summary":"Complete parser review","body":"Evidence"}`, "Complete parser review"},
		{"board_read", `{"thread_id":"board-thread-42"}`, "thread board-thread-42"},
		{"board_read", `{"since":19}`, "after #19"},
		{"mcp_read_resource", `{}`, ""},
		{"mcp_get_prompt", `{"connection_id":"docs","name":"review"}`, "docs · review"},
		{"mcp_request_access", `{"connection_id":"docs","grants":[{"tool":"lookup","fingerprint":"exact","effect":"read","scope":"public"}]}`, "docs"},
		{"capability_search", `{"query":"complete capability search query"}`, "complete capability search query"},
	} {
		if got := SummarizeArgs(tc.tool, tc.input); got != tc.want {
			t.Fatalf("%s title = %q, want %q", tc.tool, got, tc.want)
		}
	}
}
