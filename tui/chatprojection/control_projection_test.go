package chatprojection

import (
	"crude/tui/theme"
	"crude/types"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestControlAndClaimsRenderedParity(t *testing.T) {
	for _, tc := range []struct{ name, input, content, want string }{
		{"crude_control", `{"command":"terminal.close","arguments":{"id":"t1"}}`, `{"request_id":"exact-request","state":"committed","output":{"closed":true}}`, "Committed"},
		{"crude_control", `{"command":"terminal.open","arguments":{}}`, `{"request_id":"exact-request","state":"partial","fault":{"code":"unavailable","message":"Original shell detached"}}`, "Partial"},
		{"crude_control", `{"command":"terminal.open","arguments":{}}`, `{"request_id":"exact-request","state":"uncertain"}`, "Outcome uncertain"},
		{"crude_control", `{"command":"settings.set","arguments":{}}`, `{"request_id":"exact-request","state":"pending_decision"}`, "Pending decision"},
		{"agent_update_claims", `{"agent_id":"child"}`, `{"released":[],"claims":[{"id":"claim","path":"owned.go","version":2}]}`, "owned.go"},
		{"agent_narrow_claims", `{"paths":[]}`, `{"released":[{"id":"claim","path":"owned.go"}],"claims":[]}`, "No file scope retained"},
	} {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			msg := types.ChatMessage{InternalID: "control", ToolID: "call", Role: types.RoleTool, ToolName: tc.name, ToolInput: tc.input, Content: tc.content, ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone}
			batchInput, _ := json.Marshal(map[string]any{"tool_uses": []any{map[string]any{"recipient_name": tc.name, "parameters": json.RawMessage(tc.input)}}})
			batchBody, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"recipient_name": tc.name, "result": tc.content}}})
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
					if !strings.Contains(text, tc.want) || strings.Contains(text, `{"`) {
						t.Fatalf("unreadable result: %s", text)
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
