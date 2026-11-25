package chatprojection

import (
	"reflect"
	"strings"
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/conversation"
)

func TestCompactToolRowsFollowMembershipAndPlanDisclosure(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	active := conversation.ChatMessage{InternalID: "active", Role: conversation.RoleTool, ToolName: "read_file", ToolArgs: "main.go", ToolStatus: conversation.ToolStatusCalling, ToolPreviewPhase: conversation.ToolPreviewStreaming}
	plan := conversation.ChatMessage{InternalID: "plan", Role: conversation.RoleTool, ToolName: "update_plan", ToolStatus: conversation.ToolStatusSuccess, ToolPreviewPhase: conversation.ToolPreviewDone,
		Content: `{"id":"plan-1","revision":1,"objective":"Check work","status":"active","steps":[{"id":"first","description":"Verify old marker","status":"pending"}]}`}
	const width = 38
	r.SyncMessages([]conversation.ChatMessage{plan, active}, 1, 1)
	check := func() []SubPaneLine {
		t.Helper()
		rows := r.CompactRows(width)
		for _, frame := range []int{0, ui.ActivityBlinkPhaseMillis} {
			want, _ := r.BuildSubPaneToolLines(r.Messages(), width, frame)
			if rows.Height() != len(want) {
				t.Fatalf("content height %d != full render %d", rows.Height(), len(want))
			}
			for start := range want {
				if got := rows.AppendVisible(nil, start, 2, frame); !reflect.DeepEqual(got, want[start:min(start+2, len(want))]) {
					t.Fatalf("compact window differs from full render at line %d, frame %d", start, frame)
				}
			}
		}
		return rows.AppendVisible(nil, 0, rows.Height(), ui.ActivityBlinkPhaseMillis)
	}
	initial := check()
	next := plan
	next.InternalID = "new-plan"
	next.Content = strings.ReplaceAll(strings.ReplaceAll(plan.Content, "old marker", "new marker"), `"revision":1`, `"revision":2`)
	if !r.SyncChanges([]conversation.ChatMessage{next}, nil, []string{"plan", "active", "new-plan"}, 2, 1) {
		t.Fatal("cannot append a new plan")
	}
	newer := check()
	if len(newer) <= len(initial) || r.compact.byID["plan"].disclosure != DisclosureCollapsed {
		t.Fatal("newer plan did not supersede the old plan")
	}
	// Coalesce a phase-only update and removal before the next compact paint.
	active.ToolPreviewPhase = conversation.ToolPreviewDone
	if !r.SyncChanges([]conversation.ChatMessage{active}, nil, nil, 3, 1) ||
		!r.SyncChanges(nil, []string{"new-plan"}, []string{"active", "plan"}, 4, 1) {
		t.Fatal("cannot update the tool phase and remove the newer plan")
	}
	if _, found := r.compact.byID["new-plan"]; found || len(r.compact.ordered) != 2 {
		t.Fatal("removed plan retained until next compact paint")
	}
	last := check()
	if !strings.Contains(last[0].Text, "●") || r.compact.byID["plan"].disclosure != DisclosureExpanded {
		t.Fatal("settled phase or restored plan body missing after reordering")
	}
}
