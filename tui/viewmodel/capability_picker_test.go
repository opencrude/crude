package viewmodel

import (
	"testing"

	"crude/tui/screens/capabilitypicker"
	"crude/tui/ui"
)

func TestCapabilityPickerStagesExactSkillAndRejectsChangedComposer(test *testing.T) {
	app := newFullTestApp(test)
	agentID := addBackendTestOrchestrator(test, app)
	app.switchToAgentView(agentID)
	app.Input.SetValuePreserveReferences("preserve this draft")
	app.openCapabilityPicker()
	owner := app.capabilityPicker
	if !app.capabilityPickerOwnsDraft(owner) {
		test.Fatal("picker did not capture the active draft owner")
	}
	app.Input.SetValuePreserveReferences("newer draft")
	app.handleCapabilityPickerAction(ui.Action{ID: "capability-picker.insert", Payload: capabilitypicker.Selection{Kind: "skills", ID: "vendor:review"}})
	if app.Input.Value() != "newer draft" {
		test.Fatal("old picker replaced newer draft")
	}
	app.Input.SetValuePreserveReferences(owner.draft)
	app.handleCapabilityPickerAction(ui.Action{ID: "capability-picker.insert", Payload: capabilitypicker.Selection{Kind: "skills", ID: "vendor:review"}})
	if app.Input.Value() != "/skill vendor:review preserve this draft" || app.overlayHost.IsOpen(ui.OverlayCapabilityPicker) {
		test.Fatal("skill selection did not stage once and return without sending")
	}
}
