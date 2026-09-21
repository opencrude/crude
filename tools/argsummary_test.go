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

// A label never grows past the budget the row reserved for it.
func TestSummarizeArgsTruncatesWithinBudget(t *testing.T) {
	long := ""
	for i := 0; i < 90; i++ {
		long += "x"
	}
	if got := SummarizeArgs("execute_command", `command: "`+long+`"`); len([]rune(got)) != 50 {
		t.Fatalf("command label = %d runes, want 50", len([]rune(got)))
	}
	if got := SummarizeArgs("mystery_tool", long); len([]rune(got)) != 60 {
		t.Fatalf("fallback label = %d runes, want 60", len([]rune(got)))
	}
}
