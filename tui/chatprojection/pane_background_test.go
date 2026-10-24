package chatprojection

import (
	"testing"

	"crude/tui/theme"
	"crude/types"
)

func TestCollaborationToolBackgroundSurvivesEveryDisplayStage(t *testing.T) {
	for _, name := range []string{
		"agent_list", "agent_inspect", "agent_start", "agent_send", "agent_respond", "agent_control",
		"publish_report", "artifact_read", "agent_wait", "board_read",
		"read_file", "execute_command", "remember", "capability_search",
	} {
		for _, status := range []string{
			types.ToolStatusStreaming, types.ToolStatusCalling, types.ToolStatusSuccess,
			types.ToolStatusError, types.ToolStatusCancelled, "interrupted",
		} {
			t.Run(name+"/"+status, func(t *testing.T) {
				r := NewRenderer(theme.DefaultDark())
				r.SetSize(80, 30)
				r.SetChatAreaBounds(0, 20)
				r.SyncMessages([]types.ChatMessage{{
					InternalID: "call", Role: types.RoleTool, ToolName: name, ToolStatus: status,
					ToolInput: `{"kind":"note","body":"Exact evidence"}`,
					Content:   `{"message":"Exact evidence"}`,
				}}, 1, 0)
				r.ToggleDisclosure("call")
				rows, _, _ := r.ExportMessageRows(76)
				if len(rows) != 1 || len(rows[0].Native) < 2 {
					t.Fatalf("missing projected content: %+v", rows)
				}
				if rows[0].Native[0].PaneBackground {
					t.Fatal("leading transcript spacer inherited the pane band")
				}
				want := name != "read_file" && name != "execute_command" && name != "remember" && name != "capability_search"
				for _, row := range rows[0].Native[1:] {
					if row.PaneBackground != want {
						t.Fatalf("row %q pane background=%v, want %v", row.PlainText(), row.PaneBackground, want)
					}
				}
			})
		}
	}
}

func TestSpecialOnlyActivitySummaryKeepsPaneBackground(t *testing.T) {
	r := NewRenderer(theme.DefaultDark())
	r.SetSize(80, 30)
	r.SetChatAreaBounds(0, 20)
	r.SyncMessages([]types.ChatMessage{
		{InternalID: "one", Role: types.RoleThinking, Content: "First thought"},
		{InternalID: "two", Role: types.RoleThinking, Content: "Second thought"},
	}, 1, 0)
	rows, _, _ := r.ExportMessageRows(76)
	if len(rows) != 1 || len(rows[0].Native) != 2 {
		t.Fatalf("unexpected collapsed activity: %+v", rows)
	}
	if rows[0].Native[0].PaneBackground || !rows[0].Native[1].PaneBackground {
		t.Fatal("special-only summary must retain its band, excluding the spacer")
	}
}
