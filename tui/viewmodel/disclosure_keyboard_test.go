package viewmodel

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// TestCtrlAltE_TogglesNewestDisclosure verifies the keyboard shortcut toggles
// the most recent expandable block, matching the click path.
func TestCtrlAltE_TogglesNewestDisclosure(t *testing.T) {
	app := newTestApp(t)
	th := theme.DefaultDark()
	ml := chatprojection.NewRenderer(th)
	ml.SetSize(80, 30)
	ml.SetChatAreaBounds(0, 30)
	sp := app.PaneMgr.GetOrCreate("ses_1_orch_1", workspace.PaneAgent, "ses_1_orch_1")
	proj := app.subPaneProjections.Get("ses_1_orch_1", workspace.PaneAgent)
	proj.renderer = ml
	app.AgentMgr.AddOrchestrator("ses_1_orch_1", &workspace.AgentState{Messages: sp.Messages})
	app.AgentView.SetActive("ses_1_orch_1")

	// Seed two expandable messages: an early completed thought (collapsed by
	// default) and the newest one. Ctrl+Alt+E must toggle only the newest.
	ml.SyncMessages([]ttypes.ChatMessage{
		{InternalID: "thought-1", Role: ttypes.RoleThinking, Content: "a b c", Time: time.Now(), ThinkingDuration: 2 * time.Second},
		{InternalID: "tool-1", Role: ttypes.RoleTool, ToolName: "execute_command", ToolStatus: "success", Content: "ls\nfoo", Time: time.Now()},
	}, 1)

	app.dispatchKeyPress(tea.KeyPressMsg{Text: "ctrl+alt+e", Mod: tea.ModCtrl | tea.ModAlt, Code: 'e'})

	rows, _, _ := ml.ExportMessageRows(80)
	if len(rows) != 1 || !ml.DisclosureExpanded("activity:thought-1") || rows[0].LeafToggles[3] != "tool-1" {
		t.Fatalf("Ctrl+Alt+E should expand the newest activity group for audit: %+v", rows)
	}
	if ml.DisclosureExpanded("tool-1") {
		t.Fatal("Group expansion must not also expand the child tool output")
	}
}
