package viewmodel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/input"
	"crude/tui/screens/capabilitypicker"
	"crude/tui/ui"
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
		paint := ansi.Strip(app.View().RenderContent())
		if !strings.Contains(paint, route.label) || !strings.Contains(paint, "Install & manage") {
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

// The connected modal switches skills/MCP servers for the selected session
// only: the core session commits the gate, both shells' replicas observe it,
// and the registration, other sessions, the composer and MCP transports are
// untouched.
func TestConnectedCapabilityModalTogglesSessionGateOnly(t *testing.T) {
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
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:9", func(ws *workspace.Workspace) {
		defaults := ws.AppDefaults()
		defaults.Capabilities = captypes.Settings{
			Skills:      map[string]captypes.SkillConfig{"local:guide": {ID: "local:guide", Name: "Guide", Path: skillPath, Mode: "automatic", Scope: captypes.ScopeApplication}},
			Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation", Transport: "http", URL: server.URL, Scope: captypes.ScopeApplication}},
		}
		ws.SetAppDefaults(defaults)
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	first := remoteJourneyCreate(t, one)
	owner := one.app.ws.SessionForAgent(first)
	other := one.app.ws.SessionForAgent(remoteJourneyCreate(t, one))
	one.app.switchToAgentView(first)
	one.app.Input.SetInput("untouched draft")
	gate := func(app *App, id string) []string {
		session := app.ws.SessionsByID()[id]
		if session == nil || session.Settings == nil {
			return nil
		}
		return session.Settings.Effective.SessionDisabledCapabilities
	}
	for _, route := range []struct {
		command, id, label string
	}{{"/skills", "local:guide", "A concise guide."}, {"/mcps", "docs", "Documentation"}} {
		runAppCommand(t, one.app, one.app.executeSlashCommand(route.command))
		if one.app.capabilityPicker == nil || one.app.capabilityPicker.session != owner {
			t.Fatal("modal is not bound to the selected session", route.command)
		}
		remoteJourneyUntil(t, shells, func() bool { return strings.Contains(ansi.Strip(one.app.View().RenderContent()), route.label) })
		for _, enabled := range []bool{false, true} {
			cmd, handled := one.app.handleCapabilityPickerAction(ui.Action{ID: "capability-picker.toggle", Payload: route.id})
			if !handled || cmd == nil {
				t.Fatal("switch unavailable", route.command)
			}
			runAppCommand(t, one.app, cmd)
			want := !enabled
			remoteJourneyUntil(t, shells, func() bool {
				for _, shell := range shells {
					if slices.Contains(gate(shell.app, owner.SessionID), route.id) != want {
						return false
					}
				}
				return !one.app.capabilityPicker.screen.Busy()
			})
			if len(gate(one.app, other.SessionID)) != 0 {
				t.Fatal("switch leaked into another session")
			}
			caps := one.app.ws.LoadAppSettings().Capabilities
			if caps.Skills["local:guide"].Disabled || caps.Connections["docs"].Disabled {
				t.Fatal("session switch rewrote the registration")
			}
			if paint := ansi.Strip(one.app.View().RenderContent()); strings.Contains(paint, "Could not save") || one.app.Input.Value() != "untouched draft" {
				t.Fatal("toggle did not settle cleanly", paint)
			}
		}
		_, command := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
		runAppCommand(t, one.app, command)
		if one.app.overlayHost.IsOpen(ui.OverlayCapabilityPicker) {
			t.Fatal("connected Esc did not close capability modal", route.command)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("switching implicitly connected an MCP server", requests.Load())
	}
}

func TestCapabilityModalManageButtonOpensSettings(t *testing.T) {
	app := newFullTestApp(t)
	app.capabilitySnapshot = captypes.Snapshot{Settings: captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation"}}}}
	app.openCapabilityPickerTab(captypes.KindMCP, "")
	cmd, handled := app.handleCapabilityPickerAction(ui.Action{ID: "capability-picker.manage", Payload: capabilitypicker.ManageTarget{Kind: captypes.KindMCP}})
	if !handled || app.overlayHost.IsOpen(ui.OverlayCapabilityPicker) || !app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatal("Install & manage did not hand off to Settings")
	}
	_ = cmd
}
