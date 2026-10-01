package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/tui/types"
	ctypes "crude/types"
	"crude/workspace"
)

// projectionCase is one tool call as both producers see it: the raw argument
// summary the agent emits (agent.FormatToolArgs) plus the operands its event
// carries, the result body, and the outcome counters.
type projectionCase struct {
	name      string
	tool      string
	rawArgs   string
	operands  map[string]any
	content   string
	isError   bool
	added     int
	removed   int
	estTokens int
	pathLabel string
}

// The transcript is filled from two directions — live agent events through the
// workspace conversation, and a resumed session's persisted history — and both
// must land in the same projection authority with identical rows. Each case runs
// through both real entry points: workspace.Conversation for live, and the
// canonical DisplayMeta snapshot the session writes
// (session/canonicalizeDisplayMeta) through workspace.ConvertMessagesForDisplay
// and viewmodel.ChatMessagesFromViews for hydration.
func TestLiveAndHydratedToolRowsAreIdentical(t *testing.T) {
	const toolID = "call-1"
	cases := []projectionCase{
		{
			name: "edit no-change failure", tool: "edit_file",
			rawArgs:   `path: tools/dispatcher.go old_string: beta new_string: beta`,
			content:   "Error: no changes to make: old_string and new_string are exactly the same\nHint: match the file's current bytes exactly.",
			isError:   true,
			pathLabel: "tools/dispatcher.go",
		},
		{
			name: "edit real patch", tool: "edit_file",
			rawArgs: `path: tools/dispatcher.go old_string: beta new_string: delta`,
			content: "Replaced 1 occurrence in tools/dispatcher.go\n\n```diff\n--- a/tools/dispatcher.go\n+++ b/tools/dispatcher.go\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n```\n",
			added:   1, removed: 1, estTokens: 120,
			pathLabel: "tools/dispatcher.go",
		},
		{
			name: "write file", tool: "write_file",
			rawArgs: `content: package main path: x.go`,
			content: "Written 1 lines to x.go\n\n```diff\n--- a/x.go\n+++ b/x.go\n@@ -0,0 +1,1 @@\n+package main\n```\n",
			added:   1, estTokens: 42, pathLabel: "x.go",
		},
		{
			name: "grep with pattern and scope", tool: "grep_files",
			rawArgs:  `pattern: func main path: tui/tools`,
			operands: map[string]any{"pattern": "func main", "path": "tui/tools"},
			content:  "2 matches for \"func main\" in tui/tools:\ntui/tools/diff.go:1: func main()\ntui/tools/render.go:9: func main()\n",
		},
		{
			name: "web search with query", tool: "web_search",
			rawArgs:  `query: golang tui`,
			operands: map[string]any{"query": "golang tui"},
			content:  "Web search results for: golang tui\n\n1. A result\n   https://example.com\n",
		},
		{
			name:    "glob files",
			tool:    "glob_files",
			rawArgs: `pattern: **/*.go`,
			content: "3 files matching **/*.go:\na.go\nb.go\nc.go\n",
		},
		{
			name:      "read file summary",
			tool:      "read_file",
			rawArgs:   `path: tui/tools/diff.go`,
			content:   "package tools\n\nfunc main() {}\n",
			pathLabel: "tui/tools/diff.go",
		},
		{
			name:    "read file partial with diff-like evidence",
			tool:    "read_file",
			rawArgs: `limit: 45 offset: 22 path: tui/viewmodel/projection_parity_test.go`,
			content: "Lines 22–66 of tui/viewmodel/projection_parity_test.go (114 total):\n\n" +
				"  22 func TestLiveAndHydratedToolRowsAreIdentical(t *testing.T) {\n" +
				"  52     content: \"Replaced 1 occurrence in tools/dispatcher.go\\n\\n```diff\\n--- a/tools/dispatcher.go\\n+++ b/tools/dispatcher.go\",\n" +
				"  66 }\n",
			pathLabel: "tui/viewmodel/projection_parity_test.go line 22-66",
		},
		{
			name: "execute command", tool: "execute_command",
			rawArgs:   `command: go test ./tools`,
			operands:  map[string]any{"command": "go test ./tools"},
			content:   "ok  crude/tools  0.5s\n",
			pathLabel: "go run .",
		},
		{
			name:    "plan card",
			tool:    "update_plan",
			rawArgs: `plan_id: p1 steps: map[audit:done]`,
			content: `{"id":"p1","revision":2,"objective":"Ship the plan card","status":"active","steps":[{"id":"audit","description":"Audit repo","status":"completed"},{"id":"fix","description":"Fix rows","status":"in_progress"}]}`,
		},
		{
			name:    "plan failure reason",
			tool:    "update_plan",
			rawArgs: `plan_id: p1 steps: map[audit:done]`,
			content: `{"error":"revision_conflict","expected_revision":1,"received_revision":2,"current_plan":{"id":"p1"}}`,
			isError: true,
		},
		{
			name:    "get plan",
			tool:    "get_plan",
			rawArgs: `plan_id: p1`,
			content: `{"id":"p1","revision":3,"objective":"Ship the plan card","status":"active","steps":[{"id":"audit","description":"Audit repo","status":"completed"}]}`,
		},
		{
			name:    "memory rows",
			tool:    "search_history",
			rawArgs: `query: undo projection`,
			content: `{"results":[{"title":"Undo verified","summary":"byte-exact restore"}]}`,
		},
		{
			name:      "sub agent poll",
			tool:      "poll_sub_agent",
			rawArgs:   `handle_id: h-1`,
			content:   `{"status":"completed","name":"aeron","result_path":"tmp/report.md"}`,
			estTokens: 12,
		},
		{
			name: "poll with original JSON", tool: "poll_sub_agent",
			rawArgs: `{"handle_id":"agent-8f21"}`, content: `{"status":"completed","name":"Lily"}`,
		},
		{
			name: "cancel with original JSON", tool: "cancel_sub_agent",
			rawArgs: `{"handle_id":"agent-8f21"}`, content: `{"status":"cancelled","name":"Lily"}`,
		},
		{
			name: "poll running with nickname", tool: "poll_sub_agent",
			rawArgs: `{"handle_id":"agent-8f21"}`, content: "Sub-agent \"Lily sub Dawn\" [researcher] is still running.\nElapsed: 2s | Steps: 4",
		},
		{
			name: "cancelled child with nickname", tool: "cancel_sub_agent",
			rawArgs: `{"handle_id":"agent-8f21"}`, content: "## Sub-Agent Result: Lily sub Dawn [researcher]\n**Agent:** agent-8f21\n**Status:** CANCELLED",
		},
		{
			name: "list with only boolean", tool: "list_sub_agents",
			rawArgs: `{"include_completed":true}`, content: "No sub-agents found",
		},
		{
			name: "list with identities", tool: "list_sub_agents",
			rawArgs: `{"include_completed":true}`, content: "- Lily sub Dawn (agent-8f21) [researcher] running",
		},
		{
			name: "authored report with identity", tool: "write_report",
			rawArgs: `{"report":"Outcome"}`, content: `{"status":"report_written","report_path":"/tmp/agent-8f21-report.md","handle_id":"agent-8f21","name":"Lily sub Dawn"}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			live := renderLive(t, toolID, c, false)
			hydrated := renderHydrated(t, toolID, c, false)
			if len(live) != 1 || len(hydrated) != 1 {
				t.Fatalf("rows live=%d hydrated=%d, want 1 each", len(live), len(hydrated))
			}
			if live[0].Toggle != hydrated[0].Toggle {
				t.Fatalf("disclosure differs: live=%v hydrated=%v", live[0].Toggle, hydrated[0].Toggle)
			}
			assertSameRows(t, "collapsed", live[0], hydrated[0])
			if strings.Contains(c.name, "with nickname") || strings.Contains(c.name, "with identity") {
				if !strings.Contains(strings.Join(live[0].Lines, "\n"), "Lily sub Dawn (agent-8f21)") && !strings.Contains(c.name, "list") {
					t.Fatalf("known child title omitted nickname or handle: %v", live[0].Lines)
				}
			}
			if strings.Contains(c.rawArgs, `"handle_id"`) || strings.Contains(c.rawArgs, `"include_completed"`) {
				for _, row := range []chatprojection.MessageRow{live[0], hydrated[0]} {
					text := strings.Join(row.Lines, "\n")
					if strings.Contains(text, `"handle_id"`) || strings.Contains(text, `"include_completed"`) ||
						(strings.Contains(c.rawArgs, `"handle_id"`) && !strings.Contains(text, "agent-8f21")) {
						t.Fatalf("structured arguments in title or lost operand: %s", text)
					}
				}
			}

			// Expanding must stay consistent too: the expanded body reads
			// operands (command/pattern/scope) each path derives its own way.
			assertSameRows(t, "expanded", renderLive(t, toolID, c, true)[0], renderHydrated(t, toolID, c, true)[0])
			if c.name == "list with identities" {
				if !live[0].Toggle || !hydrated[0].Toggle {
					t.Fatalf("sub-agent list evidence cannot be disclosed: live=%v resumed=%v", live[0].Toggle, hydrated[0].Toggle)
				}
				expanded := renderLive(t, toolID, c, true)[0]
				if !strings.Contains(strings.Join(expanded.Lines, "\n"), "Lily sub Dawn (agent-8f21)") {
					t.Fatalf("expanded list lost individual identity: %v", expanded.Lines)
				}
			}
		})
	}
}

func TestHydratedCompactToolSnapshotKeepsSubAgentHandle(t *testing.T) {
	for _, name := range []string{"poll_sub_agent", "cancel_sub_agent"} {
		views := workspace.ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
			Msg: provider.ChatMessage{Role: "tool", ToolCallID: "call"},
			Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{
				Role: "tool", ToolName: name, ToolArgs: "agent-8f21", ToolStatus: "success", ToolPreviewPhase: "done",
			}}},
		}})
		if len(views) != 1 || views[0].ToolArgs != "agent-8f21" {
			t.Fatalf("%s: normalized snapshot lost handle: %+v", name, views)
		}
	}
}

func TestLiveAndHydratedParallelSubAgentIdentitiesAreIdentical(t *testing.T) {
	input := `{"tool_uses":[{"recipient_name":"poll_sub_agent","parameters":{"handle_id":"agent-8f21"}},{"recipient_name":"cancel_sub_agent","parameters":{"handle_id":"agent-39bf"}}]}`
	result := `{"results":[{"recipient_name":"poll_sub_agent","result":"## Sub-Agent Result: Lily sub Dawn [researcher]\n**Agent:** agent-8f21\n**Status:** OK"},{"recipient_name":"cancel_sub_agent","result":"## Sub-Agent Result: Rose sub Dawn [crafter]\n**Agent:** agent-39bf\n**Status:** CANCELLED"}]}`
	conv := workspace.NewConversation()
	conv.UpdateToolCall("call", "batch", "multi_tool_use.parallel", "2 tools")
	conv.SetToolInput("call", "batch", input, "")
	conv.UpdateToolResult("call", "batch", "multi_tool_use.parallel", result, false, 0, 0, 0)
	for i := range 8 {
		conv.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
	}
	live, _ := chatMessagesFromConversation(conv)
	live[0].ToolDuration = 0
	views := workspace.ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: provider.ChatMessage{Role: "tool", ToolCallID: "call"},
		Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{{
			InternalID: "batch", Role: "tool", ToolID: "call", ToolName: "multi_tool_use.parallel", ToolInput: input,
			ToolArgs: "2 tools", Content: result, ToolStatus: "success", ToolPreviewPhase: "done",
		}}},
	}})
	for _, width := range []int{80, 160} {
		root, recovered := newParityRenderer(), newParityRenderer()
		root.SetSize(width, 40)
		recovered.SetSize(width, 40)
		root.SyncMessages(live, 1)
		recovered.SyncMessages(ChatMessagesFromViews(views), 1)
		for _, expand := range []bool{false, true} {
			left, _, _ := root.ExportMessageRows(width)
			right, _, _ := recovered.ExportMessageRows(width)
			if len(left) != 2 || len(right) != 2 {
				t.Fatalf("width %d: batch rows=%d/%d", width, len(left), len(right))
			}
			for i, want := range []string{"Lily sub Dawn (agent-8f21)", "Rose sub Dawn (agent-39bf)"} {
				assertSameRows(t, "parallel live/resumed", left[i], right[i])
				if !strings.Contains(strings.Join(left[i].Lines, "\n"), want) || strings.Contains(strings.Join(left[i].Lines, "\n"), `"handle_id"`) {
					t.Fatalf("width %d expanded %v: bad sub-agent label: %v", width, expand, left[i].Lines)
				}
			}
			if !expand {
				root.ToggleDisclosure("batch:0")
				recovered.ToggleDisclosure("batch:0")
			}
		}
	}
}

// A successful run_sub_agent tool transport can return a running handle or a
// terminal child failure. Both projections must report the child's outcome,
// never the launch-call transport status or the raw JSON assignment.
func TestLiveAndHydratedSubAgentOutcomeRows(t *testing.T) {
	args := `{"category":"researcher","description":"Inspect the renderer and report findings","wait":true}`
	for _, tc := range []struct {
		name, content, want string
		failed              bool
	}{
		{"running", `{"name":"Lily sub Dawn","category":"researcher","handle_id":"child-1","status":"running"}`, "Started sub-agent Lily sub Dawn (child-1)", false},
		{"completed", "## Sub-Agent Result: Lily sub Dawn [researcher]\n**Agent:** child-1\n**Status:** OK\n**Report:** report.md", "Sub-agent completed Lily sub Dawn (child-1)", false},
		{"failed", "## Sub-Agent Result: Lily sub Dawn [researcher]\n**Agent:** child-1\n**Status:** ERROR — provider failed\n**Report:** report.md", "Sub-agent failed Lily sub Dawn (child-1)", true},
		{"recorded timeout", "## Sub-Agent Result: Lily sub Dawn [researcher]\n**Agent:** child-1\n**Status:** TIMEOUT\n**Report:** report.md", "Sub-agent timed out Lily sub Dawn (child-1)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := projectionCase{name: tc.name, tool: "run_sub_agent", rawArgs: args, content: tc.content}
			live := renderLive(t, "child-call", c, false)[0]
			hydrated := renderHydrated(t, "child-call", c, false)[0]
			assertSameRows(t, "collapsed", live, hydrated)
			if !live.Toggle || !hydrated.Toggle {
				t.Fatalf("child result lost disclosure: live=%v hydrated=%v", live.Toggle, hydrated.Toggle)
			}
			for _, row := range []chatprojection.MessageRow{live, hydrated} {
				text := strings.Join(row.Lines, "\n")
				if !strings.Contains(text, tc.want) || strings.Contains(text, "Sub-agent finished") || strings.Contains(text, `"category"`) || strings.Contains(text, "**Status:**") {
					t.Fatalf("misleading child outcome: %q", text)
				}
				if tc.failed && !strings.Contains(text, "× ") {
					t.Fatalf("child failure was not marked as failed: %q", text)
				}
				if !tc.failed && strings.Contains(text, "× ") {
					t.Fatalf("successful launch/completion marked as failure: %q", text)
				}
			}
			if tc.name != "running" {
				liveFull := renderLive(t, "child-call", c, true)[0]
				hydratedFull := renderHydrated(t, "child-call", c, true)[0]
				assertSameRows(t, "expanded", liveFull, hydratedFull)
				if !strings.Contains(strings.Join(liveFull.Lines, "\n"), "**Report:** report.md") {
					t.Fatalf("child report unavailable on disclosure: %+v", liveFull.Lines)
				}
			}
		})
	}
}

// The activity regression exercises the real live conversation operations and
// the persisted snapshot converter, not two copies of the renderer's grouping
// helper. The messages intentionally include a progress boundary, a mutation,
// and a failure so grouping cannot cross any of them.
func TestLiveAndHydratedActivityGroupsAreIdentical(t *testing.T) {
	conv := workspace.NewConversation()
	conv.AppendMessage(workspace.Message{InternalID: "progress", Role: "assistant", Content: "Checking the workspace.", Time: time.Unix(1, 0)})
	for i, c := range []projectionCase{
		{tool: "read_file", rawArgs: `path: one.go`, content: "package one"},
		{tool: "execute_command", rawArgs: `command: go test ./...`, operands: map[string]any{"command": "go test ./..."}, content: "PASS"},
	} {
		id := "call-" + string(rune('a'+i))
		conv.UpdateToolStreaming(id, "persisted-"+id, c.tool, map[string]any{"command": operandString(c.operands, "command")})
		conv.UpdateToolCall(id, "persisted-"+id, c.tool, workspace.SummarizeToolArgs(c.tool, c.rawArgs))
		conv.UpdateToolResult(id, "persisted-"+id, c.tool, c.content, false, 0, 0, 0)
	}
	for i := 0; i < 8; i++ {
		if !conv.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second)) {
			break
		}
	}
	conv.AppendMessage(workspace.Message{InternalID: "edit", Role: "tool", ToolName: "edit_file", ToolStatus: "success", ToolPreviewPhase: "done", ToolArgs: "main.go", Content: "Replaced 1 occurrence", Time: time.Unix(4, 0)})
	conv.AppendMessage(workspace.Message{InternalID: "reply:progress-2", Role: "assistant", Content: "Running final checks.", Time: time.Unix(5, 0)})
	conv.AppendMessage(workspace.Message{InternalID: "failure", Role: "tool", ToolName: "execute_command", ToolStatus: "error", ToolPreviewPhase: "error", ToolArgs: "go test ./...", Content: "Command failed: exit 1", Time: time.Unix(6, 0)})
	live, _ := chatMessagesFromConversation(conv)
	// The persisted transcript uses the same canonical facts with durable
	// tool-result metadata. Row identity, text, disclosure, and order must agree.
	var history []sessiondb.MessageWithMeta
	for _, message := range conv.Messages() {
		snapshot := ctypes.ConversationMessage(message)
		if snapshot.Role == "tool" && snapshot.ToolName != "edit_file" && snapshot.InternalID != "failure" {
			snapshot.ToolArgs = ""
			switch snapshot.ToolName {
			case "read_file":
				snapshot.ToolArgs = "path: one.go"
			case "execute_command":
				snapshot.ToolArgs = "command: go test ./..."
			}
		}
		history = append(history, sessiondb.MessageWithMeta{Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{snapshot}}})
	}
	hydrated := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(history))
	lr, hr := newParityRenderer(), newParityRenderer()
	lr.SyncMessages(live, 1)
	hr.SyncMessages(hydrated, 1)
	left, _, _ := lr.ExportMessageRows(200)
	right, _, _ := hr.ExportMessageRows(200)
	if len(left) != len(right) || len(left) != 5 {
		t.Fatalf("activity rows live=%d hydrated=%d", len(left), len(right))
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Toggle != right[i].Toggle {
			t.Fatalf("collapsed row identity/disclosure differs at %d: %+v vs %+v", i, left[i], right[i])
		}
		assertSameRows(t, "collapsed", left[i], right[i])
	}
	if left[1].ID != "activity:persisted-call-a" || !strings.Contains(strings.Join(left[1].Lines, "\n"), "Ran 1 command") || right[2].ID != "edit" || right[3].ID != "reply:progress-2" || right[4].ID != "failure" {
		t.Fatalf("activity boundaries or counts: %+v", left)
	}
	lr.ToggleDisclosure(left[1].ID)
	hr.ToggleDisclosure(right[1].ID)
	left, _, _ = lr.ExportMessageRows(200)
	right, _, _ = hr.ExportMessageRows(200)
	if len(left) != len(right) || len(left) != 5 {
		t.Fatalf("expanded rows live=%d hydrated=%d", len(left), len(right))
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Toggle != right[i].Toggle {
			t.Fatalf("expanded row identity/disclosure differs at %d: %+v vs %+v", i, left[i], right[i])
		}
		assertSameRows(t, "expanded", left[i], right[i])
	}
}

func renderLive(t *testing.T, toolID string, c projectionCase, expanded bool) []chatprojection.MessageRow {
	t.Helper()
	r := newParityRenderer()
	r.SyncMessages(liveToolMessages(t, toolID, c), 1)
	return exportRows(t, r, expanded)
}

func renderHydrated(t *testing.T, toolID string, c projectionCase, expanded bool) []chatprojection.MessageRow {
	t.Helper()
	r := newParityRenderer()
	r.SyncMessages(ChatMessagesFromViews(hydratedViews(t, toolID, c)), 1)
	return exportRows(t, r, expanded)
}

func newParityRenderer() *chatprojection.Renderer {
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetSize(200, 40)
	r.SetChatAreaBounds(0, 40)
	return r
}

func exportRows(t *testing.T, r *chatprojection.Renderer, expanded bool) []chatprojection.MessageRow {
	t.Helper()
	rows, _, _ := r.ExportMessageRows(200)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if expanded {
		r.ToggleDisclosure(rows[0].ID)
		rows, _, _ = r.ExportMessageRows(200)
	}
	return rows
}

// liveToolMessages drives the conversation the way agent events do and returns
// the projections the viewmodel publishes for it.
func liveToolMessages(t *testing.T, toolID string, c projectionCase) []types.ChatMessage {
	t.Helper()
	conv := workspace.NewConversation()
	internalID := "live-1"
	data := map[string]any{"name": c.tool, "args": c.rawArgs}
	for key, value := range c.operands {
		data[key] = value
	}
	conv.UpdateToolStreaming(toolID, internalID, c.tool, data)
	conv.UpdateToolCall(toolID, internalID, c.tool, workspace.SummarizeToolArgs(c.tool, c.rawArgs))
	if c.pathLabel != "" {
		conv.UpdateToolProgress(toolID, internalID, c.tool, "streaming", "", c.pathLabel, c.added, c.removed, c.estTokens)
	}
	conv.UpdateToolResult(toolID, internalID, c.tool, c.content, c.isError, c.estTokens, c.added, c.removed)
	// Phase transitions are ticked rather than applied inline: drain them until
	// the row reaches its terminal state, exactly as the running app does.
	for i := 0; i < 8; i++ {
		if !conv.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second)) {
			break
		}
	}
	messages, _ := chatMessagesFromConversation(conv)
	if len(messages) != 1 {
		t.Fatalf("live messages = %d, want 1", len(messages))
	}
	// Duration is a wall-clock artifact of the moment the tool finished; the
	// hydrated side carries DurationMs instead, so both sides compare with the
	// duration cleared.
	messages[0].ToolDuration = 0
	return messages
}

// hydratedViews reproduces what a resumed session reads back: the canonical tool
// snapshot the session persists, with the raw argument summary it stores and the
// operands applyCanonicalToolOperands derives.
func hydratedViews(t *testing.T, toolID string, c projectionCase) []workspace.ChatMessageView {
	t.Helper()
	status, phase := "success", "done"
	if c.isError {
		status, phase = "error", "error"
	}
	snapshot := ctypes.ConversationMessage{
		InternalID: "persisted-0", Role: "tool", ToolID: toolID,
		ToolName: c.tool, ToolArgs: c.rawArgs,
		ToolStatus: status, ToolPreviewPhase: phase,
		ToolEstTokens: c.estTokens, ToolLinesAdded: c.added, ToolLinesRemoved: c.removed,
		Content:           c.content,
		ToolProgressLabel: c.pathLabel,
	}
	switch c.tool {
	case "execute_command", "async_execute_command":
		snapshot.ToolCommand = operandString(c.operands, "command")
	case "grep_files":
		snapshot.ToolInput = operandString(c.operands, "pattern")
		snapshot.ToolScope = operandString(c.operands, "path")
	case "web_search", "arxiv_search":
		snapshot.ToolInput = operandString(c.operands, "query")
	case "web_fetch":
		snapshot.ToolInput = operandString(c.operands, "url")
	}
	views := workspace.ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg: provider.ChatMessage{Role: "tool", ToolCallID: toolID},
		Meta: sessiondb.DisplayMeta{
			SnapshotVersion: sessiondb.ConversationSnapshotVersion,
			Snapshots:       []ctypes.ConversationMessage{snapshot},
		},
	}})
	if len(views) != 1 {
		t.Fatalf("hydration produced %d views, want 1", len(views))
	}
	return views
}

func operandString(operands map[string]any, key string) string {
	value, _ := operands[key].(string)
	return value
}

func assertSameRows(t *testing.T, label string, live, hydrated chatprojection.MessageRow) {
	t.Helper()
	if len(live.Lines) != len(hydrated.Lines) {
		t.Fatalf("%s height differs:\n live     %#v\n hydrated %#v", label, live.Lines, hydrated.Lines)
	}
	for i := range live.Lines {
		if live.Lines[i] != hydrated.Lines[i] {
			t.Fatalf("%s row %d differs:\n live     %q\n hydrated %q", label, i, live.Lines[i], hydrated.Lines[i])
		}
	}
	if len(live.Native) != len(hydrated.Native) {
		t.Fatalf("%s native line counts differ: %d vs %d", label, len(live.Native), len(hydrated.Native))
	}
	for i := range live.Native {
		want, got := live.Native[i].Spans, hydrated.Native[i].Spans
		if len(want) != len(got) {
			t.Fatalf("%s row %d span counts differ: %d vs %d", label, i, len(want), len(got))
		}
		for s := range want {
			if want[s] != got[s] {
				t.Fatalf("%s row %d span %d differs:\n live     %+v\n hydrated %+v", label, i, s, want[s], got[s])
			}
		}
	}
}
