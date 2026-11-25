package session

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/types/command"
)

type planModeRequest struct {
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
}

func (r planModeRequest) text(index int) string {
	var text string
	if json.Unmarshal(r.Messages[index].Content, &text) != nil {
		text = string(r.Messages[index].Content)
	}
	return text
}

func (r planModeRequest) offers(name string) bool {
	for _, tool := range r.Tools {
		if tool.Function.Name == name {
			return true
		}
	}
	return false
}

func (r planModeRequest) hasUpdate(fragment string) bool {
	for i, m := range r.Messages {
		if m.Role == "user" && strings.HasPrefix(r.text(i), "<settings_update>") && strings.Contains(r.text(i), fragment) {
			return true
		}
	}
	return false
}

func TestPlanModeTurnsOfferScopedInvestigationAndEndExplicitly(t *testing.T) {
	j := newEpochJourney(t)
	runtime := epochRuntime{catalog: make(chan bool, 16)}
	j.SetCapabilities(runtime, nil)
	j.SetCodeIntelligence(&fakeCodeIntelligence{editor: true})
	initial := j.start(t, "Inspect in normal mode.")
	for _, name := range []string{"open_plan_investigation", "delete_file", "lsp", "mcp__editor__change", "agent_start", "capability_activate"} {
		if !slices.ContainsFunc(initial.Tools, func(d provider.ToolDef) bool { return d.Function.Name == name }) {
			t.Fatalf("test did not exercise declaration %q", name)
		}
	}
	initial.tool("open_plan_investigation", "{}")
	after := j.next(t)
	assertToolReply := func(call epochCall, text string) {
		t.Helper()
		if !slices.ContainsFunc(call.Messages, func(m provider.ChatMessage) bool {
			return m.Role == "tool" && strings.Contains(m.ContentString(), text)
		}) {
			t.Fatalf("missing tool refusal %q", text)
		}
	}
	assertToolReply(after, "Turn on Plan mode")
	j.finish(t, after, "Ready.")
	if err := j.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	planned := j.start(t, "Plan the refactor.")
	assertEpochPrefix(t, after.ChatRequest, planned.ChatRequest)
	assertEpochUpdate(t, planned.Messages[len(after.Messages):], "<plan_mode>", "Keep the source unchanged")
	planned.tool("delete_file", `{"path":"unchanged.go"}`)
	refused := j.next(t)
	assertToolReply(refused, "Plan mode does not allow delete_file")
	j.finish(t, refused, "Proposed changes.")
	if err := j.SetPlanMode(false); err != nil {
		t.Fatal(err)
	}
	normal := j.start(t, "Implement it.")
	assertEpochPrefix(t, refused.ChatRequest, normal.ChatRequest)
	tail := normal.Messages[len(refused.Messages):]
	assertEpochUpdate(t, tail, "<planning_contract>")
	if !slices.ContainsFunc(tail, func(m provider.ChatMessage) bool { return strings.Contains(m.ContentString(), "turned Plan mode off") }) {
		t.Fatal("missing ended notice")
	}
	j.finish(t, normal, "Done.")
	if len(runtime.catalog) != 5 {
		t.Fatalf("got %d catalog preparations, want one per request", len(runtime.catalog))
	}
	for _, want := range []bool{false, false, true, true, false} {
		if got := <-runtime.catalog; got != want {
			t.Fatal("Plan mode must only observe connected tools")
		}
	}
}

// Plan mode is a session setting: it survives resume.
func TestPlanModeSurvivesResume(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	cfg := mediaAttributionConfig("http://127.0.0.1:1", "plan-model", "openai")
	s, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	resumed, err := ResumeSessionWithDir(s.dbPath, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	if !resumed.PlanMode() {
		t.Fatal("Plan mode did not survive resume")
	}
}

func TestPlanModeRefusesMutationsAtExecution(t *testing.T) {
	for _, test := range []struct {
		tool    string
		params  map[string]any
		refused bool
	}{
		{"read_file", map[string]any{"path": "a.go"}, false},
		{"exec_command", map[string]any{"cmd": "git status"}, false},
		{"exec_command", map[string]any{"cmd": "rm -rf build"}, true},
		{"execute_command", map[string]any{"command": "sed -i s/a/b/ a.go"}, true},
		{"write_stdin", map[string]any{"chars": "yes\n"}, true},
		{"apply_patch", map[string]any{"input": "*** Begin Patch"}, true},
		{"write_file", map[string]any{"path": "a.go"}, true},
		{"update_plan", map[string]any{}, true},
		{"crude_control", map[string]any{}, true},
		// Declared on every root turn; Plan mode refuses them at execution.
		{"delete_file", map[string]any{"path": "a.go"}, true},
		{"agent_start", map[string]any{}, true},
		{"capability_activate", map[string]any{}, true},
		{"mcp__editor__change", map[string]any{}, true},
	} {
		err := planModeRefusal(test.tool, test.params, nil, nil, command.SyntaxPOSIX)
		if (err != nil) != test.refused {
			t.Errorf("%s %v: refusal = %v, want refused %v", test.tool, test.params, err, test.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "plan mode") {
			t.Errorf("%s refusal does not name Plan mode: %v", test.tool, err)
		}
	}
}

// PowerShell runs code inside @( ), { } and after <# #> that bash parsing
// does not see, so Plan mode must not accept such a command as read-only.
func TestPlanModeReadsCommandsInTheAgentShellSyntax(t *testing.T) {
	for _, cmd := range []string{"git status @(Remove-Item src)", "type README.md; & {Remove-Item src}", "git status <# x #> ; Remove-Item src"} {
		if err := planModeRefusal("execute_command", map[string]any{"command": cmd}, nil, nil, command.SyntaxPowerShell); err == nil {
			t.Errorf("Plan mode accepted %q in PowerShell", cmd)
		}
	}
	if err := planModeRefusal("execute_command", map[string]any{"command": "git status"}, nil, nil, command.SyntaxPowerShell); err != nil {
		t.Errorf("plain read-only PowerShell command refused: %v", err)
	}
	// exec_command may name its own shell; the gate reads that shell's syntax.
	if err := planModeRefusal("exec_command", map[string]any{"cmd": "git status @(Remove-Item src)", "shell": "pwsh"}, nil, nil, command.SyntaxPOSIX); err == nil {
		t.Error("Plan mode accepted hidden PowerShell code for an explicit pwsh shell")
	}
}
