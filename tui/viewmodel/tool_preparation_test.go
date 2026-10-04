package viewmodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/tui/chatprojection"
	ctypes "crude/types"
	"crude/workspace"
)

func TestParallelPreparationKeepsPlacementWhileArgumentsStream(t *testing.T) {
	for _, width := range []int{80, 160} {
		for _, prior := range []bool{false, true} {
			t.Run(fmt.Sprintf("width%d/prior%v", width, prior), func(t *testing.T) {
				app := newFullTestApp(t)
				app.width, app.height = width, 32
				app.AddOrchestrator("preparation")
				state := app.AgentMgr.Get("preparation").State
				groupID := "activity:batch:0"
				if prior {
					groupID = "activity:prior-a"
					for _, id := range []string{"prior-a", "prior-b"} {
						state.Messages.AppendMessage(workspace.Message{InternalID: id, Role: ctypes.RoleTool,
							ToolName: "read_file", ToolArgs: id + ".go", ToolStatus: ctypes.ToolStatusSuccess, Content: "package fixture"})
					}
				}
				app.syncAgentMessages(state.ID, false)
				paint := func() string {
					app.paintShellFrame()
					var rows []string
					for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H-2; y++ {
						rows = append(rows, shellBufferRow(app, y))
					}
					return strings.Join(rows, "\n")
				}
				previous := paint()
				const first = `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"src/first.go"}}`
				for _, step := range []struct {
					name, args, visible string
					children            int
					unchanged           bool
				}{
					{"envelope", `{`, "", 0, true},
					{"empty child", `{"tool_uses":[{`, "", 0, true},
					{"partial name", `{"tool_uses":[{"recipient_name":"re`, "", 0, true},
					{"name without closing quote", `{"tool_uses":[{"recipient_name":"read_file`, "", 0, true},
					{"name ready", `{"tool_uses":[{"recipient_name":"read_file"`, "Preparing to read", 1, false},
					{"partial path", `{"tool_uses":[{"recipient_name":"read_file","parameters":{"path":"src/fi`, "src/fi", 1, false},
					{"longer path", first, "src/first.go", 1, false},
					{"second empty child", first + `,{`, "src/first.go", 1, true},
					{"second partial name", first + `,{"recipient_name":"grep`, "src/first.go", 1, true},
					{"second arguments streaming", first + `,{"recipient_name":"grep_files","parameters":{"pattern":"needle`, "needle", 2, false},
				} {
					preview := agent.ExtractPreview("batch", "multi_tool_use.parallel", step.args, nil)
					data := map[string]any{"tool_id": "batch", "internal_id": "batch", "request_id": "request",
						"name": "multi_tool_use.parallel", "args": step.args, "tool_est_tokens": 1}
					if preview.Params["tool_uses"] != nil {
						input, err := json.Marshal(preview.Params)
						if err != nil {
							t.Fatal(err)
						}
						data["batch_input"] = string(input)
					}
					app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: state.ID, Type: "tool_call_streaming", Data: data})
					painted := paint()
					rows, _, _ := app.activeMessageList().ExportMessageRows(app.chatReflowInnerW)
					wantRows, wantLeaves := 0, step.children
					if prior {
						wantLeaves += 2
					}
					if wantLeaves > 0 {
						wantRows = 1
					}
					if len(rows) != wantRows {
						t.Fatalf("%s: rows=%d, want %d:\n%s", step.name, len(rows), wantRows, painted)
					}
					if wantRows > 0 {
						text := strings.Join(rows[0].Lines, "\n")
						leaves := strings.Count(text, "\u251c\u2500") + strings.Count(text, "\u2514\u2500")
						if rows[0].ID != groupID || leaves != wantLeaves {
							t.Fatalf("%s: id=%s leaves=%d, want %s/%d:\n%s", step.name, rows[0].ID, leaves, groupID, wantLeaves, painted)
						}
					}
					if step.unchanged && painted != previous {
						t.Fatalf("%s: incomplete identity moved the painted transcript:\nbefore:\n%s\nafter:\n%s", step.name, previous, painted)
					}
					if step.visible != "" && !strings.Contains(painted, step.visible) {
						t.Fatalf("%s: useful streaming preview %q missing:\n%s", step.name, step.visible, painted)
					}
					previous = painted
				}
				args := first + `,{"recipient_name":"grep_files","parameters":{"pattern":"needle","path":"src"}}]}`
				for _, event := range []ctypes.AgentEventMsg{
					{Type: "tool_call", Data: map[string]any{"tool_id": "batch", "internal_id": "batch", "name": "multi_tool_use.parallel", "args": args}},
					{Type: "tool_call_streaming", Data: map[string]any{"tool_id": "batch", "internal_id": "batch", "name": "multi_tool_use.parallel", "content": `{"results":[{"recipient_name":"read_file","phase":"running"},null]}`}},
					{Type: "tool_result", Data: map[string]any{"tool_id": "batch", "internal_id": "batch", "name": "multi_tool_use.parallel", "result": `{"results":[{"recipient_name":"read_file","result":"package fixture"},{"recipient_name":"grep_files","result":"one match"}]}`}},
				} {
					event.AgentID = state.ID
					app.handleAgentEvent(event)
					paint()
					rows, _, _ := app.activeMessageList().ExportMessageRows(app.chatReflowInnerW)
					if len(rows) != 1 || rows[0].ID != groupID {
						t.Fatalf("%s reparented the prepared children: %+v", event.Type, rows)
					}
				}
				for i := range 3 {
					state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
				}
				app.syncAgentMessages(state.ID, false)
				paint()
				live, _, _ := app.activeMessageList().ExportMessageRows(app.chatReflowInnerW)
				snapshots, _ := chatMessagesFromConversation(state.Messages)
				recovered := chatprojection.NewRenderer(app.Theme)
				recovered.SetSize(app.chatReflowInnerW, 32)
				recovered.SetChatAreaBounds(0, app.chatRect.H-2)
				recovered.SyncMessages(snapshots, 1)
				resumed, _, _ := recovered.ExportMessageRows(app.chatReflowInnerW)
				if len(live) != 1 || len(resumed) != 1 {
					t.Fatalf("live/recovered batch rows=%d/%d", len(live), len(resumed))
				}
				assertSameRows(t, "settled batch", live[0], resumed[0])
			})
		}
	}
}

func TestAgentToolStatusRejectsStructuredToolNames(t *testing.T) {
	if got := agentToolDetail(map[string]any{"name": `{"private_id":"agent-8f21"}`}); got != "Running tool" {
		t.Fatalf("structured name leaked into operation detail: %q", got)
	}
}
