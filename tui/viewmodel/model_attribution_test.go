package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/types"
	"crude/workspace"
)

func TestSessionSettingsSaveRefreshesRetainedLegacyModelFallback(t *testing.T) {
	app := newTestApp(t)
	app.ModelName = "deepseek/deepseek-v4-flash-vision-exp"
	session := app.ActiveSession
	session.SessionID = "session"
	app.SessionsByID = map[string]*workspace.Session{"session": session}
	app.SessionsByAgent = map[string]*workspace.Session{"root": session}
	app.AddOrchestrator("root")

	node := app.AgentMgr.Get("root")
	node.State.Messages.AppendMessage(workspace.Message{
		InternalID: "legacy", Role: workspace.MessageRoleAssistant, Content: "legacy answer",
	})
	app.syncAgentMessages("root", false)

	target := app.sessionProjections.Get(session).settings
	app.Update(sessionSettingsSaveResultMsg{
		target: target,
		settings: types.Settings{
			Model: "kimi-k3",
		},
	})

	renderer := app.subPaneProjections.GetExisting("root").renderer
	renderer.SetSize(100, 30)
	renderer.SetChatAreaBounds(0, 25)
	_ = renderer.Render(100)
	messages := renderer.Messages()
	if len(messages) != 1 || !strings.Contains(strings.Join(messages[0].CachedLines, "\n"), "Crude kimi-k3") {
		if len(messages) == 0 {
			t.Fatal("legacy message disappeared after session save")
		}
		t.Fatalf("legacy fallback was not refreshed after session save: %q", messages[0].CachedLines)
	}
}
