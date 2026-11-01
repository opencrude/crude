package chatprojection

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/types"
)

// Goal tools answer the model in JSON. During a long goal run the transcript
// showed that JSON verbatim; the row must read as a goal on every path.
func TestGoalToolRowsRenderGoalNotJSON(t *testing.T) {
	active := `{"goal":{"id":"goal-7ef8f6093609","objective":"start implementing the plan and dont stop until its through and through complete","status":"active","tokens_used":1978793969,"time_used":138010392321469,"created_at":"2026-10-30T07:50:46.852429525-04:00","updated_at":"2026-10-31T22:12:02.355253106-04:00"}}`
	for _, tc := range []struct {
		name, tool, input, content string
		want                       []string
	}{
		{"read active", "get_goal", `{}`, active, []string{"Read goal", "active", "start implementing the plan", "1d 14h active", "2.0B tokens"}},
		{"no goal", "get_goal", `{}`, `{"goal":null}`, []string{"Read goal", "This session has no goal."}},
		{"blocked", "update_goal", `{"status":"blocked","reason":"Needs the original terminal"}`,
			`{"goal":{"id":"goal-1","objective":"Ship it","status":"blocked","reason":"Needs the original terminal","tokens_used":1200,"time_used":0}}`,
			[]string{"Updated goal", "blocked", "Ship it", "Needs the original terminal"}},
		{"verifying", "update_goal", `{"status":"complete","evidence":"all gates pass"}`,
			`{"status":"verifying","verifier_id":"agent-verifier","next":"An independent verifier is checking the current state."}`,
			[]string{"Updated goal", "verifier checking", "An independent verifier is checking"}},
		{"created", "create_goal", `{"objective":"Ship it"}`,
			`{"goal":{"id":"goal-2","objective":"Ship it","status":"active","tokens_used":0,"time_used":0}}`,
			[]string{"Created goal", "Ship it"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := types.ChatMessage{InternalID: "goal", ToolID: "call", Role: types.RoleTool, ToolName: tc.tool, ToolInput: tc.input, Content: tc.content, ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone}
			batchInput, _ := json.Marshal(map[string]any{"tool_uses": []any{map[string]any{"recipient_name": tc.tool, "parameters": json.RawMessage(tc.input)}}})
			batchBody, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"recipient_name": tc.tool, "result": tc.content}}})
			children := parallelMessages(&types.ChatMessage{InternalID: "batch", ToolID: "batch-call", Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: string(batchInput), Content: string(batchBody), ToolStatus: types.ToolStatusSuccess})
			if len(children) != 1 {
				t.Fatal("missing batch leaf")
			}
			encoded, _ := json.Marshal(msg)
			var hydrated types.ChatMessage
			if err := json.Unmarshal(encoded, &hydrated); err != nil {
				t.Fatal(err)
			}
			var baseline []string
			for _, candidate := range []types.ChatMessage{msg, hydrated, *children[0]} {
				for _, disclosure := range []DisclosureState{DisclosureCollapsed, DisclosureExpanded} {
					rows := renderToolMessageLines(candidate, theme.DefaultDark(), 100, 100, 100, 0, 24, "done", disclosure)
					text := strings.Join(rows.lines, "\n")
					if strings.Contains(text, `{"`) || strings.Contains(text, `"goal"`) {
						t.Fatalf("goal row shows raw JSON:\n%s", text)
					}
					for _, want := range tc.want {
						if !strings.Contains(text, want) {
							t.Fatalf("goal row lacks %q:\n%s", want, text)
						}
					}
					if disclosure == DisclosureCollapsed {
						if baseline == nil {
							baseline = rows.lines
						} else if !reflect.DeepEqual(baseline, rows.lines) {
							t.Fatal("live/hydrated/batch rows differ")
						}
					}
				}
			}
		})
	}
}
