package tools

import "testing"

// The label authority is shared by the live transcript and a resumed session, so
// it must read both encodings of a tool call's arguments — and it must not lose
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
		{"poll JSON", "poll_sub_agent", `{"handle_id":"agent-8f21","extra":{"state":"running"}}`, "agent-8f21"},
		{"poll nested JSON string is not a handle", "poll_sub_agent", `{"handle_id":"{\"private\":\"not a handle\"}"}`, ""},
		{"poll summary", "poll_sub_agent", `handle_id: agent-8f21`, "agent-8f21"},
		{"poll normalized snapshot", "poll_sub_agent", "agent-8f21", "agent-8f21"},
		{"cancel JSON", "cancel_sub_agent", `{"handle_id":"agent-8f21"}`, "agent-8f21"},
		{"cancel summary", "cancel_sub_agent", `handle_id: agent-8f21`, "agent-8f21"},
		{"cancel normalized snapshot", "cancel_sub_agent", "agent-8f21", "agent-8f21"},
		{"spawn normalized snapshot", "run_sub_agent", "Inspect renderer", "Inspect renderer"},
		{"spawn category", "run_sub_agent", `{"category":"researcher","description":"Inspect renderer"}`, "Researcher · Inspect renderer"},
		{"list subagents", "list_sub_agents", `{"include_completed":true}`, ""},
		{"empty object", "list_sub_agents", `{}`, ""},
		{"empty plan", "get_plan", `{}`, ""},
		{"plan missing id", "update_plan", `{"objective":"Do work","steps":[{"description":"Inspect"}]}`, ""},
		{"question", "question", `{"questions":[{"prompt":"Choose"}]}`, ""},
		{"memory title", "remember", `{"title":"Preferred layout","content":"Keep narrow screens readable"}`, "Preferred layout"},
		{"memory update", "update_memory", `{"id":"memory-1","content":"updated"}`, "memory-1"},
		{"memory forget", "forget_memory", `{"id":"memory-2"}`, "memory-2"},
		{"report", "write_report", `{"report":"Large JSON report body"}`, ""},
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
		{"poll_sub_agent", "poll_sub_agent"},
		{"functions.read_file", "functions.read_file"},
		{`{"handle_id":"agent-8f21"}`, "tool"},
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
		{"board_post", `{"to":"Patrick","type":"ASK","body":"Run the tests"}`, "ASK → Patrick"},
		{"board_post", `{}`, ""},
		{"board_read", `{"thread":"board-thread-42"}`, "thread board-thread-42"},
		{"board_read", `{"since":19}`, "after #19"},
		{"mcp_read_resource", `{}`, ""},
		{"mcp_get_prompt", `{"connection_id":"docs","name":"review"}`, "docs · review"},
		{"capability_search", `{"query":"complete capability search query"}`, "complete capability search query"},
	} {
		if got := SummarizeArgs(tc.tool, tc.input); got != tc.want {
			t.Fatalf("%s title = %q, want %q", tc.tool, got, tc.want)
		}
	}
}
