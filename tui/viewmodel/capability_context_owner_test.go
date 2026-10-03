package viewmodel

import (
	"testing"

	"crude/sessiondb"
	"crude/tui/ui"
)

func TestMCPContextCannotAttachToDifferentAgentWithSameSessionAndDraft(test *testing.T) {
	app := newFullTestApp(test)
	agentID := addBackendTestOrchestrator(test, app)
	app.switchToAgentView(agentID)
	app.Input.SetValuePreserveReferences("same draft")
	message := capabilityContextMsg{generation: app.workspaceSwitchGen.Load(), owner: app.ActiveSession, agentID: "another-agent", draft: "same draft", reference: sessiondb.Reference{ID: "external", Kind: sessiondb.KindMCPContext, Label: "[MCP context]"}}
	app.Update(message)
	if len(app.Input.RefRanges()) != 0 || app.Input.Value() != "same draft" {
		test.Fatal("foreign active-agent result attached to matching session/draft")
	}
	app.openCapabilityPicker()
	owner := app.capabilityPicker
	app.overlayHost.Close(ui.OverlayCapabilityPicker)
	app.applyCapabilityPickerResult(capabilityPickerResultMsg{owner: owner, content: `{"Content":[{"Text":"late data"}]}`})
	if app.capabilityPicker != nil {
		test.Fatal("late result resurrected closed picker")
	}
}
