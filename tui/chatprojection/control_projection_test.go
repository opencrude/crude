package chatprojection

import (
	"crude/tui/theme"
	"crude/types/conversation"
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
			msg := conversation.ChatMessage{InternalID: "control", ToolID: "call", Role: conversation.RoleTool, ToolName: tc.name, ToolInput: tc.input, Content: tc.content, ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone}
			batchInput, _ := json.Marshal(map[string]any{"tool_uses": []any{map[string]any{"recipient_name": tc.name, "parameters": json.RawMessage(tc.input)}}})
			batchBody, _ := json.Marshal(map[string]any{"results": []any{map[string]any{"recipient_name": tc.name, "result": tc.content}}})
			children := parallelMessages(&conversation.ChatMessage{InternalID: "batch", ToolID: "batch-call", Role: conversation.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: string(batchInput), Content: string(batchBody), ToolStatus: conversation.ToolStatusSuccess})
			if len(children) != 1 {
				t.Fatal("missing batch leaf")
			}
			encoded, _ := json.Marshal(msg)
			var hydrated conversation.ChatMessage
			if err := json.Unmarshal(encoded, &hydrated); err != nil {
				t.Fatal(err)
			}
			var baseline []string
			for _, candidate := range []conversation.ChatMessage{msg, hydrated, *children[0]} {
				for _, disclosure := range []DisclosureState{DisclosureCollapsed, DisclosureExpanded} {
					rows := renderToolMessageLines(candidate, theme.DefaultDark(), 100, 100, 100, 0, 24, "done", disclosure)
					text := strings.Join(rows.lines, "\n")
					want := tc.want
					if disclosure == DisclosureCollapsed {
						switch tc.name {
						case "agent_update_claims":
							want = "1 owned path"
						case "agent_narrow_claims":
							want = "0 owned paths"
						}
					}
					if !strings.Contains(text, want) || strings.Contains(text, `{"`) {
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

// A diagnostic investigation mixes command discovery, history and inspection.
// It should be one activity group; state changes and incomplete reads must
// remain independent, even when delivered as successful tool transports.
func TestRoutineControlReadsShareHistoryActivity(t *testing.T) {
	tool := func(id, name, input, content string) conversation.ChatMessage {
		return conversation.ChatMessage{InternalID: id, Role: conversation.RoleTool, ToolName: name,
			ToolInput: input, Content: content, ToolStatus: conversation.ToolStatusSuccess}
	}
	const done = `{"request_id":"read-receipt","state":"committed","output":{"label":"Current quota evidence"}}`
	messages := []conversation.ChatMessage{
		tool("discovery", "crude_commands", `{"query":"history"}`, `[{"id":"history.prompts","effect":"read"}]`),
		tool("search", "search_history", `{"query":"quota"}`, `{"sessions":[],"turns":[]}`),
		tool("history", "crude_control", `{"command":"history.prompts"}`, done),
		tool("diagnostics", "crude_control", `{"command":"diagnostics.workspace"}`, done),
		tool("inspect", "agent_inspect", `{"agent_id":"owner"}`, `{"agent_id":"owner"}`),
		tool("mutation", "crude_control", `{"command":"terminal.close"}`, done),
		tool("pending", "crude_control", `{"command":"diagnostics.workspace"}`, `{"request_id":"pending","state":"pending_decision"}`),
		tool("partial", "crude_control", `{"command":"diagnostics.workspace"}`, `{"request_id":"partial","state":"partial"}`),
		tool("uncertain", "crude_control", `{"command":"diagnostics.workspace"}`, `{"request_id":"uncertain","state":"uncertain"}`),
		tool("failed", "crude_control", `{"command":"diagnostics.workspace"}`, `{"request_id":"failed","state":"failed"}`),
		tool("fault", "crude_control", `{"command":"diagnostics.workspace"}`, `{"request_id":"fault","state":"committed","fault":{"code":"unavailable","message":"Read failed"}}`),
		tool("no-receipt", "crude_control", `{"command":"diagnostics.workspace"}`, `{"state":"committed"}`),
		tool("unknown", "crude_control", `{"command":"command.unknown"}`, done),
		tool("receipt", "crude_control", `{"command":"operation.receipt"}`, done),
	}
	live, loaded := newActivityRenderer(), newActivityRenderer()
	live.SyncMessages(messages[:2], 1, 0)
	live.SyncMessages(messages, 2, 0)
	loaded.SyncMessages(messages, 1, 0)
	for _, r := range []*Renderer{live, loaded} {
		rows, _, _ := r.ExportMessageRows(160)
		if len(rows) != 1+len(messages[5:]) || rows[0].ID != "activity:discovery" || !rows[0].Toggle {
			t.Fatalf("investigation was split or hid a boundary: %+v", rows)
		}
		if text := strings.Join(rows[0].Lines, "\n"); !strings.Contains(text, "Searched history") || !strings.Contains(text, "Inspected Crude 2 times") {
			t.Fatalf("activity counts do not describe the reads: %s", text)
		}
		for i, want := range messages[5:] {
			if rows[i+1].ID != want.InternalID {
				t.Fatalf("boundary %q was hidden or reordered", want.InternalID)
			}
		}
		r.ToggleDisclosure(rows[0].ID)
		rows, _, _ = r.ExportMessageRows(160)
		var order []string
		for i := range rows[0].Lines {
			if id := rows[0].LeafToggles[i]; id != "" && (len(order) == 0 || order[len(order)-1] != id) {
				order = append(order, id)
			}
		}
		if !reflect.DeepEqual(order, []string{"discovery", "search", "history", "diagnostics", "inspect"}) {
			t.Fatalf("expanded leaves lost their exact order: %v", order)
		}
		r.ToggleDisclosure("diagnostics")
		rows, _, _ = r.ExportMessageRows(160)
		if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Current quota evidence") {
			t.Fatal("grouped diagnostic lost its exact result on disclosure")
		}
	}
}

func TestCrudeReadKeepsItsBranchThroughLiveSettlement(t *testing.T) {
	r := newActivityRenderer()
	messages := []conversation.ChatMessage{
		{InternalID: "file", Role: conversation.RoleTool, ToolName: "read_file", ToolStatus: "success", Content: "file body"},
		{InternalID: "discover", Role: conversation.RoleTool, ToolName: "crude_commands", ToolStatus: "success", Content: `[{"id":"history.prompts","effect":"read"}]`},
		{InternalID: "read", Role: conversation.RoleTool, ToolName: "crude_control", ToolStatus: "streaming", ToolPreviewPhase: conversation.ToolPreviewPreparing},
	}
	r.SyncMessages(messages, 1, 0)
	rows, _, _ := r.ExportMessageRows(130)
	if len(rows) != 2 || rows[0].ID != "activity:file" || rows[1].ID != "read" {
		t.Fatalf("an unresolved control effect was grouped: %+v", rows)
	}
	// The command identity can finish streaming before the status changes.
	// Eligibility must see the new exact input, not reuse a stale decision.
	messages[2].ToolInput = `{"command":"history.prompts"}`
	r.SyncMessages(messages, 2, 0)
	rows, _, _ = r.ExportMessageRows(130)
	if len(rows) != 1 || rows[0].ID != "activity:file" || rows[0].LeafToggles[4] != "read" {
		t.Fatalf("resolved read did not keep its place in the live tree: %+v", rows)
	}
	previous := rows[0]
	messages[2].ToolStatus, messages[2].Content = "success", `{"request_id":"history","state":"committed","output":{}}`
	// A preview can outlive the actual result; geometry must not blink.
	r.SyncMessages(messages, 3, 0)
	rows, _, _ = r.ExportMessageRows(130)
	if len(rows) != 1 || rows[0].ID != previous.ID || len(rows[0].Lines) != len(previous.Lines) || !reflect.DeepEqual(rows[0].LeafToggles, previous.LeafToggles) {
		t.Fatalf("settlement moved the activity tree: before=%+v after=%+v", previous, rows)
	}
}
