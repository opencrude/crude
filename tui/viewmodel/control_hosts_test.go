package viewmodel

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types/control"

	"github.com/charmbracelet/x/ansi"
)

func TestConnectedControlFloatingInventoryLifetimeAndModalIsolation(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	m := one.app
	target := m.ws.(shellport.ShellControl).ControlIdentity()
	m.openApplicationSettings()
	m.View()
	page, err := m.inspectShellControls(target, control.PageRequest{Limit: 128})
	if err != nil || len(page.Nodes) == 0 {
		t.Fatalf("settings not inspectable: %+v %v", page, err)
	}
	var first control.Node
	for _, node := range page.Nodes {
		if !strings.HasPrefix(node.ID, "floating/workspace-settings/") {
			t.Fatalf("underlying root leaked through settings: %s", node.ID)
		}
		if strings.Contains(string(node.Value), "api-key") {
			t.Fatal("credential leaked")
		}
		if first.ID == "" {
			first = node
		}
	}
	if _, _, err := m.resolveControlHost("shell", target); err == nil {
		t.Fatal("underlying root remained addressable")
	}
	m.overlayHost.Close(ui.OverlayWorkspaceSettings)
	m.openApplicationSettings()
	m.View()
	if _, _, err := m.resolveControlHost(first.ID, target); err == nil {
		t.Fatal("closed/reopened floating host retained identity")
	}
}

func TestConnectedSettingsControlEditSaveAndClose(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	two.app.Input.SetInput("peer private draft")
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "workspace"}, nil)
	invoke := func(suffix string, op ui.ControlOperation) control.Result {
		t.Helper()
		one.app.View()
		target := one.app.ws.(shellport.ShellControl).ControlIdentity()
		var id string
		cursor := ""
		for {
			page, err := one.app.inspectShellControls(target, control.PageRequest{Limit: 128, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range page.Nodes {
				if strings.HasSuffix(n.ID, "::"+suffix) {
					id = n.ID
				}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if id == "" {
			t.Fatal("missing", suffix)
		}
		intent := shellControlIntent{ComponentID: id, Operation: op}
		d, node, err := one.app.describeShellControl(intent, target)
		if err != nil {
			t.Fatal(suffix, err)
		}
		intent.ExpectedAction = d.ID
		return requestShellForTest(t, runtime, shells, one, "shell.control", intent, func(e *coreprotocol.ControlEnvelope) {
			e.Descriptor = d
			e.Descriptor.ID = "shell.control"
			e.Request.Target.ResourceID = id
			e.Request.Preconditions = control.Preconditions{Revision: &node.Revision, OpenEpoch: &node.OpenEpoch}
		})
	}
	if r := invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Editor"`)}); r.State != control.Applied {
		t.Fatal("search", r)
	}
	if r := invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatal("navigate", r)
	}
	if !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Word wrap") {
		t.Fatal("native editor page not painted")
	}
	if r := invoke("workspace-settings/form/word-wrap", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"off"`)}); r.State != control.Applied {
		t.Fatal("toggle", r)
	}
	if r := invoke("workspace-settings/form/syntax-theme", ui.ControlOperation{Verb: "select", ItemID: "dracula"}); r.State != control.Applied {
		t.Fatal("choice", r)
	}
	if r := invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"Local History"`)}); r.State != control.Applied {
		t.Fatal("search history", r)
	}
	if r := invoke(string(settingsui.WorkspaceSettingsSearchID), ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatal("navigate history", r)
	}
	if r := invoke("workspace-settings/form/history-ignores", ui.ControlOperation{Verb: "set_value", Value: json.RawMessage(`"build/**\ncache/**"`)}); r.State != control.Applied {
		t.Fatal("textarea", r)
	}
	if !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "build/**") {
		t.Fatal("textarea edit not painted")
	}
	// Footer button IDs are retained native ActionBar children.
	if r := invoke(string(settingsui.WorkspaceSettingsSaveID), ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatal("save", r)
	}
	// Save keeps Settings open on the saved values; Close is the way out.
	if body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings)); body == nil || len(body.Model().Edits()) != 0 {
		t.Fatal("successful save closed settings or left unsaved edits")
	}
	settings := one.app.ws.LoadAppSettings()
	if settings.Editor.WordWrap || settings.Editor.SyntaxTheme != "dracula" {
		t.Fatalf("core did not persist: %+v", settings.Editor)
	}
	if strings.Join(settings.LocalHistory.ExtraIgnorePatterns, "|") != "build/**|cache/**" {
		t.Fatal("textarea not persisted", settings.LocalHistory)
	}
	if two.app.Input.Value() != "peer private draft" || two.app.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil {
		t.Fatal("peer state changed")
	}
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "application"}, nil)
	// A human edit on another page cannot be laundered through ordinary Save.
	body := one.app.settingsSurface(one.app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	for index, category := range body.Model().Categories() {
		if category.Name == "Command Safety" {
			body.Model().SetValueForTest(index, "safety-confirm-at", "destructive")
		}
	}
	if r := invoke(string(settingsui.WorkspaceSettingsSaveID), ui.ControlOperation{Verb: "activate"}); r.State != control.Failed || r.Fault == nil || r.Fault.Code != control.Denied {
		t.Fatal("security dirty Save laundered", r)
	}
	// Discard remains human-owned: an agent cannot close over unsaved edits.
	if r := invoke(string(settingsui.WorkspaceSettingsCancelID), ui.ControlOperation{Verb: "activate"}); r.State == control.Applied || one.app.overlayHost.Window(ui.OverlayWorkspaceSettings) == nil {
		t.Fatal("agent close discarded human edits", r)
	}
	one.app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	// Close the exact decision overlay before reopening the settings lifetime.
	one.app.overlayHost.Close(ui.OverlaySettingsPrompt)
	requestShellForTest(t, runtime, shells, one, "settings.open", map[string]any{"scope": "application"}, nil)
	if r := invoke(string(settingsui.WorkspaceSettingsCancelID), ui.ControlOperation{Verb: "activate"}); r.State != control.Applied {
		t.Fatal("close", r)
	}
	if one.app.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil {
		t.Fatal("close receipt falsely applied")
	}
}

func TestConnectedControlCrossHostCursorRejectsSourceChange(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	one := newRemoteJourneyShell(t, runtime, t.TempDir())
	m := one.app
	target := m.ws.(shellport.ShellControl).ControlIdentity()
	m.openApplicationSettings()
	m.View()
	page, err := m.inspectShellControls(target, control.PageRequest{Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("missing bounded continuation: %+v %v", page, err)
	}
	m.overlayHost.Close(ui.OverlayWorkspaceSettings)
	if _, err := m.inspectShellControls(target, control.PageRequest{Limit: 1, Cursor: page.NextCursor}); err == nil {
		t.Fatal("cursor crossed modal lifetime")
	}
}
