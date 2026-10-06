package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/tui/screens/activitybar"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestRuntimeCapabilityObservationUpdatesRetainedSettingsAndSidebar(t *testing.T) {
	for _, size := range []struct {
		name          string
		width, height int
	}{{name: "readable", width: 160, height: 40}, {name: "constrained", width: 80, height: 24}} {
		t.Run(size.name, func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = size.width, size.height
			app.EditorState.ActiveView = "editor-hides-launcher"
			app.openSettings(types.SettingsScopeWorkspace)
			app.openCapabilityManager(activitybar.ViewSkills, "", "")

			snapshot := capabilityVisibilitySnapshot(2, "agent:live-review", "Live review", captypes.ScopeWorkspace)
			app.launcherCapabilitySequence = 7
			app.applyLauncherCapabilities(launcherCapabilitiesMsg{
				screen:    app.launcherScreen,
				workspace: app.workspaceSwitchGen.Load(),
				sequence:  7,
				snapshot:  snapshot,
			})

			painted := paintCapabilitySettings(t, app)
			if !strings.Contains(painted, "Live review") {
				t.Fatalf("live workspace registration was absent from retained Settings at %dx%d:\n%s", size.width, size.height, painted)
			}
			items := app.capabilitySidebarItems(activitybar.ViewSkills)
			if len(items) != 2 || items[1].ID != "capability:agent:live-review" || items[1].Label != "Live review" {
				t.Fatalf("sidebar did not receive the same runtime snapshot: %+v", items)
			}
			if app.launcherHostVisible() {
				t.Fatal("test did not keep the launcher hidden behind the active editor")
			}
			if !app.capabilityObservationVisible() {
				t.Fatal("retained Settings did not keep passive capability observation visible")
			}
			if cmd := app.launcherCapabilitiesCmd(time.Now().Add(3 * time.Second)); cmd == nil {
				t.Fatal("Settings-open passive observation was not scheduled while launcher was hidden")
			}

			app.openSettings(types.SettingsScopeApplication)
			app.openCapabilityManager(activitybar.ViewSkills, "", "")
			if application := paintCapabilitySettings(t, app); strings.Contains(application, "Live review") {
				t.Fatalf("workspace registration leaked into Application Settings at %dx%d:\n%s", size.width, size.height, application)
			}
		})
	}
}

func TestRuntimeCapabilityObservationWithActiveAgentAndNoLauncherScreen(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	projectDir := t.TempDir()
	app.Workspace = workspace.NewWorkspace(projectDir, "test-model", "tester", types.Settings{}, stubLogger{})
	skillDir := filepath.Join(projectDir, "agent-review")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: agent-review\ndescription: Agent-installed review workflow.\n---\nReview the exact evidence.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, _, _, err := app.CapabilityAction(t.Context(), captypes.Action{
		Operation: "add-local",
		Kind:      captypes.KindSkill,
		Scope:     captypes.ScopeWorkspace,
		Skill: &captypes.SkillConfig{
			ID:   "local:agent-review",
			Name: "agent-review",
			Path: skillDir,
			Mode: "manual",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Generation == 0 {
		t.Fatal("real workspace capability service did not publish the registration")
	}

	app.openSettings(types.SettingsScopeWorkspace)
	app.openCapabilityManager(activitybar.ViewSkills, "", "")
	app.launcherScreen = nil // AddOrchestrator removes the home screen while an agent is active.
	if !app.capabilityObservationVisible() {
		t.Fatal("open Settings should require observation even without a launcher screen")
	}
	cmd := app.launcherCapabilitiesCmd(time.Now().Add(3 * time.Second))
	if cmd == nil {
		t.Fatal("active-agent Settings observation was incorrectly gated on a launcher screen")
	}
	raw := cmd()
	message, ok := raw.(launcherCapabilitiesMsg)
	if !ok {
		t.Fatalf("runtime observer returned %T", raw)
	}
	app.applyLauncherCapabilities(message)

	painted := paintCapabilitySettings(t, app)
	if !strings.Contains(painted, "agent-review") {
		t.Fatalf("real workspace registration did not reach retained Settings:\n%s", painted)
	}
	items := app.capabilitySidebarItems(activitybar.ViewSkills)
	if len(items) != 2 || items[1].ID != "capability:local:agent-review" {
		t.Fatalf("real workspace registration did not reach the sidebar: %+v", items)
	}
}

func TestRuntimeCapabilityObservationRejectsForeignWorkspaceWithoutReplacingSettings(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.applyObservedCapabilitySnapshot(capabilityVisibilitySnapshot(2, "local:kept", "Kept skill", captypes.ScopeWorkspace), nil, false)
	app.openSettings(types.SettingsScopeWorkspace)
	app.openCapabilityManager(activitybar.ViewSkills, "", "")
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)

	app.launcherCapabilitySequence = 4
	app.applyLauncherCapabilities(launcherCapabilitiesMsg{
		screen:    app.launcherScreen,
		workspace: app.workspaceSwitchGen.Load() + 1,
		sequence:  4,
		snapshot:  capabilityVisibilitySnapshot(3, "foreign:late", "Foreign late skill", captypes.ScopeWorkspace),
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

func TestRuntimeCapabilityObservationRejectsStaleSequence(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 160, 40
	app.applyObservedCapabilitySnapshot(capabilityVisibilitySnapshot(4, "local:kept", "Kept skill", captypes.ScopeWorkspace), nil, false)
	app.openSettings(types.SettingsScopeWorkspace)
	app.openCapabilityManager(activitybar.ViewSkills, "", "")

	app.launcherCapabilitySequence = 9
	app.applyLauncherCapabilities(launcherCapabilitiesMsg{
		screen:    app.launcherScreen,
		workspace: app.workspaceSwitchGen.Load(),
		sequence:  8,
		snapshot:  capabilityVisibilitySnapshot(5, "stale:late", "Stale late skill", captypes.ScopeWorkspace),
	})

	painted := paintCapabilitySettings(t, app)
	if !strings.Contains(painted, "Kept skill") || strings.Contains(painted, "Stale late skill") {
		t.Fatalf("stale observation changed retained Settings:\n%s", painted)
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

func capabilityVisibilitySnapshot(generation uint64, id, name string, scope captypes.Scope) captypes.Snapshot {
	return captypes.Snapshot{
		Generation: generation,
		Settings: captypes.Settings{Skills: map[string]captypes.SkillConfig{
			id: {ID: id, Name: name, Mode: "manual", Scope: scope},
		}},
		Entries: []captypes.Entry{{
			ID:        id,
			Name:      name,
			Kind:      captypes.KindSkill,
			Origin:    scope,
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
