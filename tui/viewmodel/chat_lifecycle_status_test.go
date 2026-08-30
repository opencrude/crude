package viewmodel

import (
	"strings"
	"testing"

	"crude/tui/types"
)

func TestSubmitProjectsLifecycleIntoChatNotBottomStatus(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	agentID := addBackendTestOrchestrator(t, app)

	_ = app.submitInput(agentID, "inspect lifecycle", nil)
	messages := app.AgentMgr.Get(agentID).State.Messages.Messages()
	if len(messages) != 2 || messages[1].Role != types.RoleStatus || messages[1].LifecyclePhase != "preparing" {
		t.Fatalf("submitted conversation = %#v", messages)
	}
	if bottom := app.StatusBar.ActivityView(); strings.Contains(bottom, "Getting next turn ready") || strings.Contains(bottom, "uploading context") {
		t.Fatalf("per-agent lifecycle leaked into bottom status: %q", bottom)
	}
	if bottom := app.StatusBar.ActivityView(); !strings.Contains(bottom, "1 agent active") {
		t.Fatalf("bottom status lost aggregate agent reporting: %q", bottom)
	}
}
