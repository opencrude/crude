package viewmodel

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"crude/core/shellport"
	"crude/tui/screens/confirmation"
	"crude/tui/ui"
)

// The Auto Review choice was decided once, when a request was queued, so a
// request that waited while the user turned Auto Review on kept offering it.
func TestWaitingConfirmationDropsAutoReviewChoiceOnceItIsOn(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("root")
	req := agentPromptRequest{kind: agentPromptConfirmation, agentID: "root", blockID: "b1", question: "Allow this unknown command?", risk: "unknown",
		autoReview: "Auto Review was not sure: it runs a script."}
	app.enqueueAgentConfirmation(req)
	state := app.agentPrompt
	if state == nil || !state.offerAutoReview {
		t.Fatal("a request while Auto Review is off did not offer it")
	}
	if !strings.Contains(ansi.Strip(app.View().RenderContent()), "Auto Review was not sure") {
		t.Fatal("the prompt did not say why Auto Review left the request to the user")
	}

	settings := app.ws.LoadAppSettings()
	settings.Safety.AutoPermission.Enabled = "on"
	if err := wsOf(app).SaveAppSettings(settings); err != nil {
		t.Fatal(err)
	}
	app.Update(shellport.SettingsChangedMsg{})

	if state.offerAutoReview || len(confirmation.Choices(state.offerAutoReview)) != 3 {
		t.Fatal("the waiting request still offers Auto Review after it was turned on")
	}
	if app.agentPrompt != state || !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) {
		t.Fatal("the waiting request was not presented again")
	}
	if strings.Contains(ansi.Strip(app.View().RenderContent()), "Auto Review…") {
		t.Fatal("the modal still shows the Auto Review choice")
	}
}
