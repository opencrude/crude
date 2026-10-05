package viewmodel

import (
	"strings"
	"testing"
	"time"

	"crude/tui/screens/activitybar"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

func TestRuntimeCapabilityObservationUpdatesRetainedSettingsAndSidebar(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		t.Run(string(scope), func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = 160, 40
			app.openSettings(scope)
			app.openCapabilityManager(activitybar.ViewSkills, "", "")

			snapshot := capabilityVisibilitySnapshot(2, "agent:live-review", "Live review")
			app.launcherCapabilitySequence = 7
			app.applyLauncherCapabilities(launcherCapabilitiesMsg{
				screen:    app.launcherScreen,
				workspace: app.workspaceSwitchGen.Load(),
				sequence:  7,
				snapshot:  snapshot,
			})

			painted := paintCapabilitySettings(t, app)
			if !strings.Contains(painted, "Live review") {
				t.Fatalf("live runtime registration was absent from retained %s Settings:\n%s", scope, painted)
			}
			items := app.capabilitySidebarItems(activitybar.ViewSkills)
			if len(items) != 2 || items[1].ID != "capability:agent:live-review" || items[1].Label != "Live review" {
				t.Fatalf("sidebar did not receive the same runtime snapshot: %+v", items)
			}
		})
	}
}

func TestRuntimeCapabilityObservationRejectsForeignWorkspaceWithoutReplacingSettings(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.applyObservedCapabilitySnapshot(capabilityVisibilitySnapshot(2, "local:kept", "Kept skill"), nil, false)
	app.openSettings(types.SettingsScopeWorkspace)
	app.openCapabilityManager(activitybar.ViewSkills, "", "")
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)

	app.launcherCapabilitySequence = 4
	app.applyLauncherCapabilities(launcherCapabilitiesMsg{
		screen:    app.launcherScreen,
		workspace: app.workspaceSwitchGen.Load() + 1,
		sequence:  4,
		snapshot:  capabilityVisibilitySnapshot(3, "foreign:late", "Foreign late skill"),
	})

	if app.overlayHost.Window(ui.OverlayWorkspaceSettings) != window {
		t.Fatal("foreign runtime result replaced the retained Settings owner")
	}
	painted := paintCapabilitySettings(t, app)
	if !strings.Contains(painted, "Kept skill") || strings.Contains(painted, "Foreign late skill") {
		t.Fatalf("foreign workspace result changed retained Settings:\n%s", painted)
	}
	items := app.capabilitySidebarItems(activitybar.ViewSkills)
	if len(items) != 2 || items[1].ID != "capability:local:kept" {
		t.Fatalf("foreign workspace result changed the sidebar: %+v", items)
	}
}

func TestCapabilityPresentationRefreshDoesNotInitializeOrConfigureRuntime(t *testing.T) {
	app := newFullTestApp(t)
	message, ok := app.launcherCapabilitiesCmd(time.Now())().(launcherCapabilitiesMsg)
	if !ok {
		t.Fatalf("presentation refresh returned %T", message)
	}
	if message.err != nil || message.snapshot.Generation != 0 {
		t.Fatalf("passive runtime observation initialized or configured capabilities: %+v", message)
	}
}

func capabilityVisibilitySnapshot(generation uint64, id, name string) captypes.Snapshot {
	return captypes.Snapshot{
		Generation: generation,
		Settings: captypes.Settings{Skills: map[string]captypes.SkillConfig{
			id: {ID: id, Name: name, Mode: "manual", Scope: captypes.ScopeApplication},
		}},
		Entries: []captypes.Entry{{
			ID:        id,
			Name:      name,
			Kind:      captypes.KindSkill,
			Origin:    captypes.ScopeApplication,
			State:     "available",
			Mode:      "manual",
			Installed: true,
		}},
	}
}

func paintCapabilitySettings(t *testing.T, app *App) string {
	t.Helper()
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window == nil {
		t.Fatal("Settings window is not open")
	}
	frame, ok := window.Content.(*ui.WindowFrameWidget)
	if !ok {
		t.Fatalf("Settings content is %T", window.Content)
	}
	width, height := window.Size()
	frame.Host().Resize(width, height)
	frame.Host().Layout()
	buffer := surface.NewCellBuffer(width, height)
	frame.Host().Paint(buffer)
	var painted strings.Builder
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			painted.WriteRune(buffer.Get(column, row).Char)
		}
		painted.WriteByte('\n')
	}
	return painted.String()
}
