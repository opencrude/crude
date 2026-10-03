package viewmodel

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/activitybar"
	settingsui "crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestCapabilityDeepLinkReflowsRetainedSettingsBeforeFirstPaint(test *testing.T) {
	app := newFullTestApp(test)
	app.width, app.height = 160, 40
	app.capabilitySnapshot = captypes.Snapshot{Generation: 1, Settings: captypes.Settings{Skills: map[string]captypes.SkillConfig{"local:fixture": {ID: "local:fixture", Name: "Evidence fixture", Mode: "manual"}}}, Entries: []captypes.Entry{{ID: "local:fixture", Name: "Evidence fixture", Kind: captypes.KindSkill, State: "available", Installed: true}}}
	app.openCapabilityManager(activitybar.ViewSkills, "", "")
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	frame := window.Content.(*ui.WindowFrameWidget)
	width, height := window.Size()
	paint := func() string {
		frame.Host().Resize(width, height)
		frame.Host().Layout()
		buffer := surface.NewCellBuffer(width, height)
		frame.Host().Paint(buffer)
		var text strings.Builder
		for row := 0; row < height; row++ {
			for column := 0; column < width; column++ {
				text.WriteRune(buffer.Get(column, row).Char)
			}
			text.WriteByte('\n')
		}
		return text.String()
	}
	first := paint()
	if !strings.Contains(first, "Installed") || !strings.Contains(first, "Evidence fixture") || strings.Contains(first, "Search agents and roles") {
		test.Fatalf("Skills deep-link painted another retained catalog:\n%s", first)
	}
	app.openCapabilityManager(activitybar.ViewMCP, "", "")
	if !strings.Contains(paint(), "Search MCP") || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != window {
		test.Fatal("MCP deep-link did not retain and reflow the same Settings frame")
	}
}

func TestCapabilityReadCancellationPrecedesWorkspaceTeardown(test *testing.T) {
	app := newFullTestApp(test)
	command := app.capabilitySnapshotCmd()
	app.cancelCapabilityOperations()
	message, ok := command().(capabilitySnapshotMsg)
	if !ok || !errors.Is(message.err, context.Canceled) {
		test.Fatalf("read survived its cancellation boundary: %+v", message)
	}
}

func TestCapabilityResultReachesOwningModelWhileCatalogControlHasFocus(test *testing.T) {
	app := newFullTestApp(test)
	app.width, app.height = 160, 40
	command := app.openCapabilityManager(activitybar.ViewSkills, "", "")
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	var request settingsui.CapabilityRequestMsg
	batch, ok := command().(tea.BatchMsg)
	if !ok {
		test.Fatal("manager commands were not returned")
	}
	for _, operation := range batch {
		if message, ok := operation().(settingsui.CapabilityRequestMsg); ok {
			request = message
		}
	}
	if request.Target == nil {
		test.Fatal("manager emitted no request")
	}
	message := settingsui.CapabilityResultMsg{Target: request.Target, Sequence: request.Sequence, Action: request.Action, Snapshot: captypes.Snapshot{Generation: 1}}
	app.applyCapabilityResult(message)
	frame := window.Content.(*ui.WindowFrameWidget)
	frame.Host().MarkLayout()
	frame.Host().Layout()
	buffer := surface.NewCellBuffer(124, 28)
	frame.Host().Paint(buffer)
	var painted strings.Builder
	for row := 0; row < buffer.Height(); row++ {
		for column := 0; column < buffer.Width(); column++ {
			painted.WriteRune(buffer.Get(column, row).Char)
		}
	}
	if strings.Contains(painted.String(), "Cancel operation") {
		test.Fatal("finished request remained pending because a focused text control consumed the async result")
	}
}
