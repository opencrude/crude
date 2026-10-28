package viewmodel

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

// Exercise the actual application registry and agent-event ingress, not just a
// path helper: the workspace root must reach the renderer that paints the row.
func TestToolPathProjectionUsesWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "long_filename.go")
	app := newWorkspaceSwitchLifecycleApp(t, root)
	app.AddOrchestrator("path-owner")
	state := app.ws.AgentMgr().Get("path-owner").State
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": "read", "tool_id": "call", "name": "read_file", "args": string(args),
	}})
	app.syncAgentMessages(state.ID, false)
	r := app.subPaneProjections.GetExisting(state.ID).renderer
	r.SetSize(160, 40)
	r.SetChatAreaBounds(0, 40)
	rows, _, _ := r.ExportMessageRows(160)
	if len(rows) != 1 {
		t.Fatalf("expected one tool row, got %d", len(rows))
	}
	text := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(text, filepath.Join("src", "long_filename.go")) || strings.Contains(text, root) {
		t.Fatalf("tool title should use a project-relative path, got %q", text)
	}
	if got := state.Messages.Messages()[0]; got.ToolArgs != path || got.Role != types.RoleTool {
		t.Fatalf("presentation changed canonical tool data: %+v", got)
	}
}

func TestToolPathProjectionFollowsWorkspaceSwitch(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	app := newWorkspaceSwitchLifecycleApp(t, first)
	if err := app.SwitchProject(second); err != nil {
		t.Fatal(err)
	}
	app.AddOrchestrator("second-owner")
	state := app.ws.AgentMgr().Get("second-owner").State
	path := filepath.Join(second, "src", "new.go")
	args, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": "read", "tool_id": "call", "name": "read_file", "args": string(args),
	}})
	app.syncAgentMessages(state.ID, false)
	r := app.subPaneProjections.GetExisting(state.ID).renderer
	r.SetSize(160, 40)
	r.SetChatAreaBounds(0, 40)
	rows, _, _ := r.ExportMessageRows(160)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), filepath.Join("src", "new.go")) ||
		strings.Contains(strings.Join(rows[0].Lines, "\n"), second) {
		t.Fatalf("switched workspace root was not published: %+v", rows)
	}
}

func TestRelativePathsInStreamingAndFailedRows(t *testing.T) {
	state := workspace.NewAgentState("agent", workspace.NewConversation(), nil)
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
		"internal_id": "read", "tool_id": "call", "name": "read_file",
		"path": "/project/src/a.go", "offset": 2, "limit": 5,
	}})
	live, _ := chatMessagesFromConversation(state.Messages)
	r := newParityRenderer()
	r.SetProjectRoot("/project")
	r.SyncMessages(live, 1, 0)
	rows, _, _ := r.ExportMessageRows(200)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "src/a.go line 2-6") ||
		strings.Contains(strings.Join(rows[0].Lines, "\n"), "/project") {
		t.Fatalf("streaming path or line range lost: %+v", rows)
	}
	for i, name := range []string{"lsp", "grep_files"} {
		stream := workspace.NewAgentState("stream", workspace.NewConversation(), nil)
		stream.ApplyEvent(workspace.AgentEvent{Type: "tool_call_streaming", Data: map[string]any{
			"internal_id": name, "tool_id": name, "name": name, "path": "/project/src/a.go",
		}})
		messages, _ := chatMessagesFromConversation(stream.Messages)
		r.SyncMessages(messages, uint64(i+2), 0)
		rows, _, _ = r.ExportMessageRows(200)
		if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "src/a.go") ||
			strings.Contains(strings.Join(rows[0].Lines, "\n"), "/project") {
			t.Fatalf("%s streaming path was not projected: %+v", name, rows)
		}
	}

	failed := types.ChatMessage{InternalID: "failed", Role: types.RoleTool, ToolName: "read_file",
		ToolArgs: "/project/src/a.go", ToolStatus: types.ToolStatusError, ToolPreviewPhase: types.ToolPreviewError,
		Content: "Error: denied absolute path /project/src/a.go"}
	r.SyncMessages([]types.ChatMessage{failed}, 4, 0)
	rows, _, _ = r.ExportMessageRows(200)
	text := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(text, "Read File failed src/a.go") || !strings.Contains(text, "Error: denied absolute path /project/src/a.go") {
		t.Fatalf("failure lost its relative title or literal diagnostics: %s", text)
	}
}

func TestProjectRelativeWindowsPathsInRenderedRows(t *testing.T) {
	r := newParityRenderer()
	r.SetProjectRoot(`C:\Users\Me\Project`)
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", ToolArgs: `c:/users/me/project/src\main.go`, ToolStatus: types.ToolStatusSuccess},
		{InternalID: "external", Role: types.RoleTool, ToolName: "delete_file", ToolArgs: `D:\outside\file.go`, ToolStatus: types.ToolStatusError, Content: "Error: denied"},
	}, 1, 0)
	rows, _, _ := r.ExportMessageRows(200)
	var text strings.Builder
	for _, row := range rows {
		text.WriteString(strings.Join(row.Lines, "\n"))
	}
	if !strings.Contains(text.String(), `Read src\main.go`) || !strings.Contains(text.String(), `D:\outside\file.go`) ||
		strings.Contains(text.String(), `c:/users/me/project/src\main.go`) {
		t.Fatalf("Windows workspace paths rendered incorrectly: %s", text.String())
	}
}

func TestParallelPatchUsesRemotePathSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, root, path, want string
	}{
		{"POSIX core", "/home/me/project", "/home/me/project/src/a.go", "src/a.go"},
		{"Windows core", `C:\Work\project`, `C:\Work\project\src\a.go`, `src\a.go`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch := "*** Begin Patch\n*** Update File: " + tc.path + "\n@@\n-old\n+new\n*** End Patch\n"
			input, err := json.Marshal(map[string]any{"tool_uses": []map[string]any{{
				"recipient_name": "functions.apply_patch", "parameters": map[string]string{"input": patch},
			}}})
			if err != nil {
				t.Fatal(err)
			}
			r := newParityRenderer()
			r.SetProjectRoot(tc.root)
			r.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel",
				ToolInput: string(input), ToolStatus: types.ToolStatusStreaming, ToolPreviewPhase: types.ToolPreviewStreaming}}, 1, 0)
			rows, _, _ := r.ExportMessageRows(200)
			if len(rows) != 1 {
				t.Fatalf("parallel patch rows = %d", len(rows))
			}
			text := strings.Join(rows[0].Lines, "\n")
			if !strings.Contains(text, tc.want) || strings.Contains(text, tc.root) {
				t.Fatalf("remote patch operand rendered incorrectly: %s", text)
			}
		})
	}
}

func TestRelativeToolTitlesMatchLiveAndHydratedEvidence(t *testing.T) {
	const root = "/project with spaces"
	patch := "*** Begin Patch\n*** Update File: /project with spaces/src/a.go\n@@\n-old\n+new\n*** Add File: /project with spaces/src/b.go\n+new\n*** End Patch\n"
	for _, tc := range []struct {
		projectionCase
		want, evidence string
	}{
		{projectionCase: projectionCase{tool: "read_file", rawArgs: `{"path":"/project with spaces/src/a.go"}`, content: "exact file contents /project with spaces/src/a.go"},
			want: "Read src/a.go", evidence: "exact file contents /project with spaces/src/a.go"},
		{projectionCase: projectionCase{tool: "grep_files", rawArgs: `{"pattern":"in /project with spaces","path":"/project with spaces/src"}`, operands: map[string]any{"pattern": "in /project with spaces", "path": "/project with spaces/src"}, content: "1 match\nsrc/a.go:1: exact evidence"},
			want: `Searched "in /project with spaces" in src`, evidence: "src/a.go:1: exact evidence"},
		{projectionCase: projectionCase{tool: "move_file", rawArgs: `{"source":"/project with spaces/src/a.go","destination":"/elsewhere/a.go"}`, content: "Moved /project with spaces/src/a.go to /elsewhere/a.go"},
			want: "Moved src/a.go → /elsewhere/a.go", evidence: "Moved /project with spaces/src/a.go to /elsewhere/a.go"},
		{projectionCase: projectionCase{tool: "apply_patch", rawArgs: patch, content: "Success. Updated files\n```diff\n--- a/src/a.go\n+++ b/src/a.go\n@@ -1 +1 @@\n-old\n+new\n```"},
			want: "Edited src/a.go, src/b.go", evidence: "new"},
		{projectionCase: projectionCase{tool: "lsp", rawArgs: `{"operation":"definition","symbol":"main","path":"/project with spaces/src/a.go"}`, content: "definition found"},
			want: "Language server definition · main · src/a.go", evidence: "definition found"},
		{projectionCase: projectionCase{tool: "read_file", rawArgs: `{"path":"/project with spaces-old/file.go"}`, content: "outside contents"},
			want: "Read /project with spaces-old/file.go", evidence: "outside contents"},
		{projectionCase: projectionCase{tool: "execute_command", rawArgs: `{"command":"cat '/project with spaces/src/a.go'"}`, operands: map[string]any{"command": "cat '/project with spaces/src/a.go'"}, content: `{"exit_code":0,"output":"literal /project with spaces/src/a.go\n"}`},
			want: "Ran cat '/project with spaces/src/a.go'", evidence: "literal /project with spaces/src/a.go"},
	} {
		t.Run(tc.tool+"/"+tc.want, func(t *testing.T) {
			live := liveToolMessages(t, "call", tc.projectionCase)
			hydrated := ChatMessagesFromViews(hydratedViews(t, "call", tc.projectionCase))
			before := live[0]
			left, right := newParityRenderer(), newParityRenderer()
			left.SetProjectRoot(root)
			right.SetProjectRoot(root)
			left.SyncMessages(live, 1, 0)
			right.SyncMessages(hydrated, 1, 0)
			lrows, rrows := exportRows(t, left, false), exportRows(t, right, false)
			assertSameRows(t, "relative collapsed", lrows[0], rrows[0])
			if text := strings.Join(lrows[0].Lines, "\n"); !strings.Contains(text, tc.want) {
				t.Fatalf("title missing %q: %s", tc.want, text)
			}
			// Only tools with disclosure need a second inspection. The retained
			// result is never shortened along with the title.
			if lrows[0].Toggle {
				if tc.tool == "apply_patch" {
					if text := strings.Join(lrows[0].Lines, "\n"); !strings.Contains(text, tc.evidence) {
						t.Fatalf("default-open patch evidence missing %q: %s", tc.evidence, text)
					}
				}
				left.ToggleDisclosure(lrows[0].ID)
				right.ToggleDisclosure(rrows[0].ID)
				lrows, _, _ = left.ExportMessageRows(200)
				rrows, _, _ = right.ExportMessageRows(200)
				assertSameRows(t, "relative expanded", lrows[0], rrows[0])
				if text := strings.Join(lrows[0].Lines, "\n"); tc.tool != "apply_patch" && !strings.Contains(text, tc.evidence) {
					t.Fatalf("expanded evidence missing %q: %s", tc.evidence, text)
				}
			}
			if !reflect.DeepEqual(before, live[0]) {
				t.Fatal("renderer changed the canonical input projection")
			}
		})
	}
}

func TestRelativeParallelToolRowsAndDisclosure(t *testing.T) {
	const root = "/project"
	input := `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"/project/src/a.go"}},{"recipient_name":"functions.grep_files","parameters":{"pattern":"needle","path":"/project/src"}}]}`
	result := `{"results":[{"recipient_name":"read_file","result":"literal /project/src/a.go","error":false},{"recipient_name":"grep_files","result":"1 match\nsrc/a.go:1: needle","error":false}]}`
	state := workspace.NewAgentState("agent", workspace.NewConversation(), nil)
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{
		"internal_id": "batch", "tool_id": "call", "name": "multi_tool_use.parallel", "args": input,
	}})
	state.ApplyEvent(workspace.AgentEvent{Type: "tool_result", Data: map[string]any{
		"internal_id": "batch", "tool_id": "call", "name": "multi_tool_use.parallel", "result": result,
	}})
	snapshot := state.Messages.Messages()[0]
	snapshot.ToolStatus, snapshot.ToolPreviewPhase = types.ToolStatusSuccess, types.ToolPreviewDone
	live := ChatMessagesFromViews([]workspace.ChatMessageView{snapshot})
	hydrated := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{
		Msg:  provider.ChatMessage{Role: "tool", ToolCallID: "call"},
		Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []types.ConversationMessage{snapshot}},
	}}))
	left, right := newParityRenderer(), newParityRenderer()
	left.SetProjectRoot(root)
	right.SetProjectRoot(root)
	left.SyncMessages(live, 1, 0)
	right.SyncMessages(hydrated, 1, 0)
	for _, expanded := range []bool{false, true} {
		lrows, _, _ := left.ExportMessageRows(200)
		rrows, _, _ := right.ExportMessageRows(200)
		if len(lrows) != 1 || len(rrows) != 1 {
			t.Fatalf("parallel rows = %d/%d", len(lrows), len(rrows))
		}
		assertSameRows(t, "parallel", lrows[0], rrows[0])
		text := strings.Join(lrows[0].Lines, "\n")
		if !strings.Contains(text, "Read src/a.go") || !strings.Contains(text, `Searched "needle" in src`) || strings.Contains(text, "/project") {
			t.Fatalf("parallel titles, expanded=%v: %s", expanded, text)
		}
		if !expanded {
			left.ToggleDisclosure(lrows[0].ID)
			right.ToggleDisclosure(rrows[0].ID)
		}
	}
	left.ToggleDisclosure("batch:0")
	rows, _, _ := left.ExportMessageRows(200)
	if text := strings.Join(rows[0].Lines, "\n"); !strings.Contains(text, "literal /project/src/a.go") {
		t.Fatalf("parallel disclosure lost absolute evidence: %s", text)
	}
	if snapshot.ToolInput != input || snapshot.Content != result {
		t.Fatal("parallel canonical input or result changed")
	}
}

func TestProjectPathRootChangeRefreshesMainAndCompactRows(t *testing.T) {
	reg := newSubPaneProjectionRegistry(theme.DefaultDark())
	reg.SetProjectRoot("/first")
	embedded := reg.ForOwner("parent")
	pane := workspace.NewSubPane("child", workspace.PaneAgent, "Child")
	pane.Messages.AppendMessage(workspace.Message{InternalID: "read", Role: types.RoleTool, ToolName: "read_file",
		ToolArgs: "/first/src/a.go", ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone})
	p := embedded.Get(pane.ID, pane.Type)
	p.renderer.SetSize(100, 24)
	p.renderer.SetChatAreaBounds(0, 24)
	embedded.SyncFromWorkspace(pane.ID, pane.Messages, false)
	assert := func(want string) {
		t.Helper()
		mainRows, _, _ := p.renderer.ExportMessageRows(100)
		compact := embedded.Render(pane.ID, pane, 100, 8, 0, true)
		if len(mainRows) != 1 || !strings.Contains(strings.Join(mainRows[0].Lines, "\n"), want) || !subPanePlainContains(compact, want) {
			t.Fatalf("main/compact rows do not both contain %q: %+v / %+v", want, mainRows, compact)
		}
	}
	assert("Read src/a.go")
	before := p.renderer.Version()
	reg.SetProjectRoot("/first")
	if p.renderer.Version() != before {
		t.Fatal("unchanged root invalidated retained rows")
	}
	reg.SetProjectRoot("/second")
	assert("Read /first/src/a.go")

	// Independent renderers used for another workspace do not borrow the root.
	other := chatprojection.NewRenderer(theme.DefaultDark())
	other.SetProjectRoot("/second")
	other.SetSize(100, 24)
	other.SetChatAreaBounds(0, 24)
	other.SyncMessages([]types.ChatMessage{{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "/second/src/b.go", ToolStatus: types.ToolStatusSuccess}}, 1, 0)
	rows, _, _ := other.ExportMessageRows(100)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Read src/b.go") {
		t.Fatalf("second workspace root missing: %+v", rows)
	}
}
