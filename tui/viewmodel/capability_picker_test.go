package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"crude/core"
	"crude/tui/input"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

func TestCapabilitySlashCommandsOpenSimpleModalWithoutChangingDraft(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = noopLogger{}
	app.Input.SetInput("keep my draft")
	app.capabilitySnapshot = captypes.Snapshot{Generation: 1, Settings: captypes.Settings{
		Skills:      map[string]captypes.SkillConfig{"guide": {ID: "guide", Name: "Guide"}},
		Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation"}},
	}}
	for _, route := range []struct{ command, label string }{{"/skills", "Guide"}, {"/mcps", "Documentation"}} {
		commands := app.handleInputEvent(input.SlashCommandEvent{Command: route.command})
		if len(commands) != 1 || commands[0] == nil || !app.overlayHost.IsOpen(ui.OverlayCapabilityPicker) || app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
			t.Fatal("slash route did not open the simple capabilities modal", route.command)
		}
		paint := ansi.Strip(app.View().Content)
		if !strings.Contains(paint, route.label) || !strings.Contains(paint, "Deactivate") {
			t.Fatalf("wrong tab or missing action for %s:\n%s", route.command, paint)
		}
		for _, removed := range []string{"+ Capability", "stage in composer", "Inspect / select", "Resources", "Prompts"} {
			if strings.Contains(paint, removed) {
				t.Fatal("removed capability UI remains", removed)
			}
		}
		if app.Input.Value() != "keep my draft" {
			t.Fatal("opening modal changed the draft")
		}
		_, command := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		runAppCommand(t, app, command)
		if app.overlayHost.IsOpen(ui.OverlayCapabilityPicker) {
			t.Fatal("Esc did not close capability modal", route.command)
		}
	}
	for _, cmd := range input.AvailableCmds() {
		if cmd.Name == "skill" || cmd.Name == "mcp" {
			t.Fatal("removed singular capability slash command remains", cmd.Name)
		}
	}
	app.executeSlashCommand("/skill guide")
	app.executeSlashCommand("/mcp")
	if app.capabilityPicker != nil || app.Input.Value() != "keep my draft" {
		t.Fatal("removed command invoked a skill or mutated the draft")
	}
}

func TestCapabilityModalIgnoresClosedReopenedAndOldWorkspaceResults(t *testing.T) {
	app := newFullTestApp(t)
	app.openCapabilityPicker()
	old := app.capabilityPicker
	app.openCapabilityPicker()
	current := app.capabilityPicker
	late := captypes.Snapshot{Generation: 99, Settings: captypes.Settings{Skills: map[string]captypes.SkillConfig{"foreign": {ID: "foreign", Name: "Foreign skill"}}}}
	app.applyCapabilityPickerResult(capabilityPickerResultMsg{owner: old, sequence: old.sequence, snapshot: late})
	if app.capabilitySnapshot.Generation == 99 {
		t.Fatal("reopened modal accepted an old result")
	}
	app.workspaceSwitchGen.Add(1)
	app.applyCapabilityPickerResult(capabilityPickerResultMsg{owner: current, sequence: current.sequence, snapshot: late})
	if app.capabilitySnapshot.Generation == 99 {
		t.Fatal("old workspace result replaced current capabilities")
	}
}

func TestConnectedCapabilityModalPersistsTogglesWithoutLaunchOrComposerMutation(t *testing.T) {
	root := t.TempDir()
	skillPath := filepath.Join(root, "guide")
	if err := os.MkdirAll(skillPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillPath, "SKILL.md"), []byte("---\nname: guide\ndescription: A concise guide.\n---\nRead the project carefully.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	settingsPath := filepath.Join(t.TempDir(), "settings.json")
	baseline := types.AppSettings{Capabilities: captypes.Settings{
		Skills:      map[string]captypes.SkillConfig{"local:guide": {ID: "local:guide", Name: "Guide", Path: skillPath, Mode: "disabled", Disabled: true, Scope: captypes.ScopeApplication}},
		Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation", Transport: "http", URL: server.URL, Disabled: true, Scope: captypes.ScopeApplication}},
	}}
	var commits atomic.Int32
	runtime, err := core.New(func(project string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(project, "", "tester", types.Settings{}, noopLogger{})
		ws.SetAppDefaults(baseline)
		baseline = ws.AppDefaults()
		ws.OnAppPreferencesCommit = func(before, after types.AppSettings) (types.AppSettings, error) {
			if !reflect.DeepEqual(before.Capabilities, baseline.Capabilities) {
				return types.AppSettings{}, errors.New("stale settings")
			}
			data, err := json.Marshal(after)
			if err != nil {
				return types.AppSettings{}, err
			}
			if err := os.WriteFile(settingsPath, data, 0600); err != nil {
				return types.AppSettings{}, err
			}
			baseline = after
			commits.Add(1)
			return after, nil
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	app.Input.SetInput("untouched draft")
	for _, route := range []struct {
		command, id string
		kind        captypes.Kind
	}{{"/skills", "local:guide", captypes.KindSkill}, {"/mcps", "docs", captypes.KindMCP}} {
		runAppCommand(t, app, app.executeSlashCommand(route.command))
		for _, active := range []bool{true, false} {
			cmd, handled := app.handleCapabilityPickerAction(ui.Action{ID: "capability-picker.toggle"})
			if !handled || cmd == nil {
				t.Fatal("visible activation action unavailable", route.command)
			}
			runAppCommand(t, app, cmd)
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				caps := app.ws.LoadAppSettings().Capabilities
				if route.kind == captypes.KindSkill {
					return caps.Skills[route.id].Disabled == !active
				}
				return caps.Connections[route.id].Disabled == !active
			})
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				t.Fatal(err)
			}
			var saved types.AppSettings
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if route.kind == captypes.KindSkill {
				if value := saved.Capabilities.Skills[route.id]; value.Disabled != !active || value.Mode != "automatic" {
					t.Fatal("skill toggle did not persist effective mode", value)
				}
			} else if saved.Capabilities.Connections[route.id].Disabled != !active {
				t.Fatal("MCP toggle did not persist")
			}
			paint := ansi.Strip(app.View().Content)
			expected := "Activate"
			if active {
				expected = "Deactivate"
			}
			if !strings.Contains(paint, expected) || app.Input.Value() != "untouched draft" || app.capabilityPicker.screen.Busy() {
				t.Fatal("toggle did not settle cleanly", paint)
			}
		}
		_, command := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		runAppCommand(t, app, command)
		if app.overlayHost.IsOpen(ui.OverlayCapabilityPicker) {
			t.Fatal("connected Esc did not close capability modal", route.command)
		}
	}
	if commits.Load() != 4 || requests.Load() != 0 {
		t.Fatal("toggles replayed effects or implicitly connected MCP", commits.Load(), requests.Load())
	}
}
