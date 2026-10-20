package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
	"crude/types"
)

func newActivityRenderer() *Renderer {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(100, 25)
	r.SetChatAreaBounds(0, 20)
	return r
}

func TestActivityProjectionSettledExpandAndBoundaries(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "user", Role: types.RoleUser, Content: "Fix it"},
		{InternalID: "progress", Role: types.RoleAssistant, Content: "Inspecting the code."},
		{InternalID: "think", Role: types.RoleThinking, Content: "reason", ThinkingDuration: 2 * time.Second},
		{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "main.go", Content: "contents", ToolStatus: "success"},
		{InternalID: "cmd", Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test", ToolCommand: "go test ./...", Content: "PASS", ToolStatus: "success"},
		{InternalID: "edit", Role: types.RoleTool, ToolName: "edit_file", ToolArgs: "main.go", Content: "Replaced 1 occurrence", ToolStatus: "success"},
		{InternalID: "progress2", Role: types.RoleAssistant, Content: "Running checks."},
		{InternalID: "fail", Role: types.RoleTool, ToolName: "execute_command", ToolStatus: "error", Content: "command failed"},
		{InternalID: "write", Role: types.RoleTool, ToolName: "write_file", ToolStatus: "success", Content: "Written 1 lines"},
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 7 || rows[2].ID != "activity:think" || !rows[2].Toggle {
		t.Fatalf("activity was not one expandable item: %+v", rows)
	}
	plain := strings.Join(rows[2].Lines, "\n")
	for _, want := range []string{"Thought for 2s", "Read 1 file", "Ran 1 command"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("summary missing %q: %s", want, plain)
		}
	}
	if strings.Contains(plain, "contents") || !strings.Contains(plain, "PASS") {
		t.Fatalf("read evidence must stay compact while command preview remains readable: %s", plain)
	}
	for index, id := range map[int]string{3: "edit", 4: "progress2", 5: "fail", 6: "write"} {
		if rows[index].ID != id {
			t.Fatalf("boundary %d = %q want %q", index, rows[index].ID, id)
		}
	}
	r.ToggleDisclosure(rows[2].ID)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 7 || rows[2].LeafToggles[2] != "think" || rows[2].LeafToggles[3] != "read" || rows[2].LeafToggles[4] != "cmd" {
		t.Fatalf("expanded activity lost chronological calls: %+v", rows)
	}
	if len(rows[2].Lines) != 6 {
		t.Fatalf("child disclosure or spacing: %+v", rows[2].Lines)
	}
	r.ToggleDisclosure("cmd")
	rows, _, _ = r.ExportMessageRows(100)
	if !strings.Contains(strings.Join(rows[2].Lines, "\n"), "PASS") {
		t.Fatalf("expanded child lost output: %+v", rows[2].Lines)
	}
	r.ToggleDisclosure("activity:think")
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 7 || rows[2].ID != "activity:think" {
		t.Fatalf("collapse did not restore compact geometry: %+v", rows)
	}
}

func TestActivityProjectionParallelRunningTreeClosesAtCorrectBranch(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "one", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "a.go", ToolStatus: "calling"},
		{InternalID: "two", Role: types.RoleTool, ToolName: "grep_files", ToolArgs: `"symbol" in tui/`, ToolStatus: "calling"},
		{InternalID: "three", Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test ./...", ToolStatus: "calling"},
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ID != "activity:one" || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "├─ ● Reading a.go") || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "└─ ● Running go test ./...") {
		t.Fatalf("parallel active branches not drawn: %+v", rows)
	}
	messages[0].ToolStatus, messages[0].Content = "success", "a.go contents"
	messages[1].ToolStatus, messages[1].Content = "success", "1 match"
	r.SyncMessages(messages, 2)
	rows, _, _ = r.ExportMessageRows(100)
	plain := strings.Join(rows[0].Lines, "\n")
	if len(rows) != 1 || strings.Count(plain, "└─") != 1 || strings.Count(plain, "├─") != 2 || !strings.Contains(plain, "Searched \"symbol\"") || !strings.Contains(plain, "Running go test ./...") {
		t.Fatalf("recent settled branch disappeared: %+v", rows)
	}
}

func TestActivityProjectionRetainsLastTreeUntilNewGroupStarts(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "a.go", ToolStatus: "calling"},
		{InternalID: "cmd", Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test ./...", ToolStatus: "calling"},
	}
	r.SyncMessages(messages, 1)
	messages[0].ToolStatus, messages[1].ToolStatus = "success", "success"
	messages[0].Content, messages[1].Content = "file", "PASS"
	r.SyncMessages(messages, 2)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "└─ ● Ran go test ./...") {
		t.Fatalf("settlement removed tree: %+v", rows)
	}
	messages = append(messages, types.ChatMessage{InternalID: "progress", Role: types.RoleAssistant, Content: "Now verifying."})
	r.SyncMessages(messages, 3)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 2 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "└─ ● Ran go test ./...") {
		t.Fatalf("progress removed tree before another group: %+v", rows)
	}
	messages = append(messages, types.ChatMessage{InternalID: "new", Role: types.RoleTool, ToolName: "grep_files", ToolArgs: "needle", ToolStatus: "calling"})
	r.SyncMessages(messages, 4)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 3 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "└─") || rows[2].ID != "new" {
		t.Fatalf("new group retired prior round too early: %+v", rows)
	}
	messages[3].ToolStatus, messages[3].Content = "success", "1 match"
	r.SyncMessages(messages, 5)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 3 || rows[2].ID != "new" || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "└─") || !strings.Contains(strings.Join(rows[2].Lines, "\n"), "Searched needle") {
		t.Fatalf("completed new group restored stale tree: %+v", rows)
	}
}

func TestActivityProjectionKeepsSuccessfulCallDuringPreviewDwell(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "a.go", ToolStatus: "success", Content: "file"},
		{InternalID: "cmd", Role: types.RoleTool, ToolName: "execute_command", ToolCommand: "go test ./...", ToolStatus: "calling", ToolPreviewPhase: types.ToolPreviewPreparing},
	}
	r.SyncMessages(messages, 1)
	messages[1].ToolStatus, messages[1].ToolPreviewPhase, messages[1].Content = "success", types.ToolPreviewPreparing, "PASS"
	r.SyncMessages(messages, 2)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ID != "activity:read" || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "└─ ● Ran go test ./...") {
		t.Fatalf("tool completion dropped group during preview dwell: %+v", rows)
	}
}

func TestActivityProjectionSkipsEmptyAssistantEnvelopesButKeepsRepliesAndFailures(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "progress", Role: types.RoleAssistant, Content: "Checking."},
		{InternalID: "empty-a", Role: types.RoleAssistant},
		{InternalID: "read-a", Role: types.RoleTool, ToolName: "read_file", Content: "one", ToolStatus: "success"},
		{InternalID: "thought", Role: types.RoleThinking, Content: "reason"},
		{InternalID: "empty-b", Role: types.RoleAssistant},
		{InternalID: "read-b", Role: types.RoleTool, ToolName: "read_file", Content: "two", ToolStatus: "success"},
		{InternalID: "reply:real", Role: types.RoleAssistant, Content: "A real progress update."},
		{InternalID: "empty-c", Role: types.RoleAssistant},
		{InternalID: "fail", Role: types.RoleTool, ToolName: "read_file", ToolStatus: "error", Content: "Error: missing"},
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 4 || rows[1].ID != "activity:read-a" || rows[2].ID != "reply:real" || rows[3].ID != "fail" || !strings.Contains(strings.Join(rows[1].Lines, " "), "Read 2 files") {
		t.Fatalf("invisible envelopes split activity or hid boundaries: %+v", rows)
	}
	r.ToggleDisclosure("activity:read-a")
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 4 || rows[1].LeafToggles[2] != "read-a" || rows[1].LeafToggles[3] != "thought" || rows[1].LeafToggles[4] != "read-b" {
		t.Fatalf("expanded group lost members or exposed envelopes: %+v", rows)
	}
}

func TestActivityTreeCommandPreviewWrapsAndLeafExpands(t *testing.T) {
	r := newActivityRenderer()
	r.SetSize(42, 30)
	r.SetChatAreaBounds(0, 28)
	command := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi psi omega"
	messages := []types.ChatMessage{
		{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", Content: "file", ToolStatus: "success"},
		{InternalID: "cmd", Role: types.RoleTool, ToolName: "execute_command", ToolCommand: command, ToolStatus: "calling"},
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(42)
	leaf := rows[0]
	if leaf.ID != "activity:read" || !leaf.Toggle || strings.Contains(strings.Join(leaf.Lines, "\n"), "more command lines") || !strings.Contains(strings.Join(leaf.Lines, "\n"), "omega") {
		t.Fatalf("full command absent: %+v", leaf)
	}
	if leaf.LeafToggles[3] != "cmd" || len(leaf.LeafToggles) < 8 {
		t.Fatalf("command leaf has no click target: %+v", leaf.LeafToggles)
	}
	for _, line := range leaf.Native {
		if line.VisibleWidth() > 42 {
			t.Fatalf("command overflows: %q", line.PlainText())
		}
	}
	r.ToggleDisclosure("cmd")
	rows, _, _ = r.ExportMessageRows(42)
	if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "omega") || strings.Contains(strings.Join(rows[0].Lines, "\n"), "more command lines") {
		t.Fatalf("click did not reveal full command: %+v", rows[0])
	}
	r.ToggleDisclosure("activity:read")
	rows, _, _ = r.ExportMessageRows(42)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "omega") {
		t.Fatalf("expanded group lost command: %+v", rows)
	}
	r.ToggleDisclosure("cmd")
	rows, _, _ = r.ExportMessageRows(42)
	if len(rows) != 1 || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "omega") || strings.Contains(strings.Join(rows[0].Lines, "\n"), "more command lines") {
		t.Fatalf("expanded group's compact command preview missing: %+v", rows)
	}
}

func TestActivityProjectionSingleRunningCallAndFailureBoundary(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "a", Role: types.RoleAssistant, Content: "Checking."},
		{InternalID: "cmd", Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test ./...", ToolStatus: "calling", ToolPreviewPhase: "preparing"},
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 2 || rows[1].ID != "cmd" || !strings.Contains(strings.Join(rows[1].Lines, "\n"), "go test ./...") {
		t.Fatalf("single active command lost target: %+v", rows)
	}
	messages[1].ToolStatus, messages[1].ToolPreviewPhase, messages[1].Content = "error", "error", "Command failed: test crashed"
	r.SyncMessages(messages, 2)
	rows, _, _ = r.ExportMessageRows(100)
	if len(rows) != 2 || rows[1].ID != "cmd" || !strings.Contains(strings.Join(rows[1].Lines, "\n"), "failed") {
		t.Fatalf("failed command buried in group: %+v", rows)
	}
}

func TestActivityProjectionActiveThinkingAndEditWriteKeepOriginalRows(t *testing.T) {
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "thinking", Role: types.RoleThinking, ThinkingActive: true, Content: "reasoning"},
		{InternalID: "edit", Role: types.RoleTool, ToolName: "edit_file", ToolArgs: "x.go", Content: "Replaced 1 occurrence", ToolStatus: "success"},
		{InternalID: "write", Role: types.RoleTool, ToolName: "write_file", ToolArgs: "y.go", Content: "Written 1 lines", ToolStatus: "success"},
	}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 3 || rows[0].ID != "thinking" || rows[1].ID != "edit" || rows[2].ID != "write" ||
		!strings.Contains(strings.Join(rows[0].Lines, "\n"), "reasoning") || strings.Contains(strings.Join(rows[0].Lines, "\n"), "Thought") {
		t.Fatalf("activity grouping changed protected projections: %+v", rows)
	}
}

func TestActivityProjectionUnknownAndChildFailureStayVisible(t *testing.T) {
	for _, c := range []struct{ name, content string }{
		{name: "agent_wait", content: `{"id":"run-failed","agent_id":"agent-1","state":"failed","version":2}`},
		{name: "check_async_command", content: `{"status":"failed","exit_code":1}`},
		{name: "execute_command", content: "Command failed: exit 1"},
		{name: "unexpected_tool", content: "ok"},
	} {
		r := newActivityRenderer()
		r.SyncMessages([]types.ChatMessage{
			{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", Content: "ok", ToolStatus: "success"},
			{InternalID: "failure", Role: types.RoleTool, ToolName: c.name, Content: c.content, ToolStatus: "success"},
		}, 1)
		rows, _, _ := r.ExportMessageRows(100)
		if len(rows) != 2 || rows[1].ID != "failure" {
			t.Fatalf("%s result silently grouped: %+v", c.name, rows)
		}
	}
}

func TestActivityProjectionRealAsyncResultsClassifiedHonestly(t *testing.T) {
	for _, c := range []struct {
		name, content string
		grouped       bool
	}{
		{"check_async_command", `{"exit_code":0,"output":"","output_descriptor":{"handle":"job","status":"completed","total_bytes":4}}`, true},
		{"check_async_command", `{"exit_code":1,"output":"","output_descriptor":{"handle":"job","status":"failed","total_bytes":4}}`, false},
		{"read_async_output", `{"output":"PASS","output_descriptor":{"handle":"job","status":"completed","total_bytes":4},"page":{"since_byte":0,"next_byte":4,"has_more":false}}`, true},
		{"agent_wait", `{"id":"run-ok","agent_id":"agent-1","state":"succeeded","version":2,"outcome":"completed"}`, true},
		{"agent_wait", `{"id":"run-failed","agent_id":"agent-1","state":"failed","version":2,"outcome":"provider failed"}`, false},
	} {
		r := newActivityRenderer()
		r.SyncMessages([]types.ChatMessage{
			{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", Content: "ok", ToolStatus: "success"},
			{InternalID: "result", Role: types.RoleTool, ToolName: c.name, Content: c.content, ToolStatus: "success"},
		}, 1)
		rows, _, _ := r.ExportMessageRows(100)
		if (len(rows) == 1) != c.grouped {
			t.Fatalf("%s result classification grouped=%v want %v, rows=%+v", c.name, len(rows) == 1, c.grouped, rows)
		}
	}
}

func TestActivityProjectionGroupingInvalidatesOnAppendAndSourceRemoval(t *testing.T) {
	r := newActivityRenderer()
	messages := []types.ChatMessage{
		{InternalID: "read", Role: types.RoleTool, ToolName: "read_file", Content: "one", ToolStatus: "success"},
		{InternalID: "cmd", Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test", Content: "PASS", ToolStatus: "success"},
	}
	r.SyncMessages(messages, 1)
	first := r.ExportMessageRowsSnapshot(100)
	if len(first.Rows) != 1 || first.Rows[0].ID != "activity:read" || !strings.Contains(strings.Join(first.Rows[0].Lines, "\n"), "Ran 1 command") {
		t.Fatalf("initial summary: %+v", first.Rows)
	}
	messages = append(messages, types.ChatMessage{InternalID: "third", Role: types.RoleTool, ToolName: "read_file", Content: "two", ToolStatus: "success"})
	r.SyncMessages(messages, 2)
	second := r.ExportMessageRowsSnapshot(100)
	if len(second.Rows) != 1 || !strings.Contains(strings.Join(second.Rows[0].Lines, "\n"), "Read 2 files") || second.Generation == first.Generation {
		t.Fatalf("append left stale group: %+v", second.Rows)
	}
	r.ToggleDisclosure("activity:read")
	third := r.ExportMessageRowsSnapshot(100)
	if len(third.Rows) != 1 || third.Rows[0].LeafToggles[5] != "third" {
		t.Fatalf("expanded appended child missing: %+v", third.Rows)
	}
	r.SyncMessages(messages[:1], 3)
	last := r.ExportMessageRowsSnapshot(100)
	if len(last.Rows) != 1 || last.Rows[0].ID != "read" || r.DisclosureExpanded("activity:read") {
		t.Fatalf("removed group kept stale disclosure: %+v", last.Rows)
	}
}

func TestActivityProjectionSingleSettledToolKeepsItsExistingRow(t *testing.T) {
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "one", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "one.go", Content: "contents", ToolStatus: "success"}}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ID != "one" || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Read one.go") {
		t.Fatalf("single completed tool should retain its ordinary informative row: %+v", rows)
	}
}

func TestActivityProjectionLiveAndRecoveredShareShape(t *testing.T) {
	base := []types.ChatMessage{
		{InternalID: "a", Role: types.RoleAssistant, Content: "Looking."},
		{InternalID: "one", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "one.go", ToolStatus: "success", Content: "one"},
		{InternalID: "two", Role: types.RoleTool, ToolName: "execute_command", ToolArgs: "go test", ToolStatus: "calling", ToolPreviewPhase: "streaming", Content: "running"},
	}
	live, resumed := newActivityRenderer(), newActivityRenderer()
	live.SyncMessages(base[:2], 1)
	live.SyncMessages(base, 2)
	active, _, _ := live.ExportMessageRows(100)
	if len(active) != 2 || active[1].ID != "activity:one" || !strings.Contains(strings.Join(active[1].Lines, "\n"), "└─") {
		t.Fatalf("active tree branch missing: %+v", active)
	}
	base[2].ToolStatus, base[2].ToolPreviewPhase, base[2].Content = "success", "done", "PASS"
	live.SyncMessages(base, 3)
	resumed.SyncMessages(base, 1)
	got, _, _ := live.ExportMessageRows(100)
	want, _, _ := resumed.ExportMessageRows(100)
	if len(got) != len(want) || len(got) != 2 {
		t.Fatalf("live/recovered row counts %d/%d", len(got), len(want))
	}
	for i := range got {
		if got[i].ID != want[i].ID || strings.Join(got[i].Lines, "\n") != strings.Join(want[i].Lines, "\n") {
			t.Fatalf("live/recovered mismatch row %d: %+v vs %+v", i, got[i], want[i])
		}
	}
	live.ToggleDisclosure(got[1].ID)
	resumed.ToggleDisclosure(want[1].ID)
	got, _, _ = live.ExportMessageRows(100)
	want, _, _ = resumed.ExportMessageRows(100)
	if len(got) != len(want) || len(got) != 2 {
		t.Fatalf("expanded parity counts %d/%d", len(got), len(want))
	}
	for i := range got {
		if strings.Join(got[i].Lines, "\n") != strings.Join(want[i].Lines, "\n") {
			t.Fatalf("expanded mismatch row %d: %+v vs %+v", i, got[i], want[i])
		}
	}
}
