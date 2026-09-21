package viewmodel

import (
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

			// Expanding must stay consistent too: the expanded body reads
			// operands (command/pattern/scope) each path derives its own way.
			assertSameRows(t, "expanded", renderLive(t, toolID, c, true)[0], renderHydrated(t, toolID, c, true)[0])
		})
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
