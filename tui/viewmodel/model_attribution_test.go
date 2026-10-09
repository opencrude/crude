package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/screens/sessionsettings"
	"crude/types"
	"crude/workspace"
)

func TestSessionSettingsSaveRefreshesRetainedLegacyModelFallback(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.ModelName = "deepseek/deepseek-v4-flash-vision-exp"
	rootID := addBackendTestOrchestrator(t, app)
	app.ActivateSessionByAgent(rootID)
	session := app.ActiveSession

	node := app.AgentMgr.Get(rootID)
	node.State.Messages.AppendMessage(workspace.Message{
		InternalID: "legacy", Role: workspace.MessageRoleAssistant, Content: "legacy answer",
	})
	app.syncAgentMessages(rootID, false)

	target := app.sessionProjections.Get(session).settings
	settings := app.BackendSession(session.SessionID).SettingsSnapshot()
	settings.SessionOverrides = map[string]types.CategorySettings{"default": {AccountID: settings.AccountID, Model: "kimi-k3"}}
	_, command := app.Update(sessionsettings.SaveMsg{Target: target, Settings: settings})
	runAppCommand(t, app, command)

	renderer := app.subPaneProjections.GetExisting(rootID).renderer
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
