package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"github.com/charmbracelet/x/ansi"
)

type integrationCodeToolsHost struct {
	shellport.Workspace
	reads, previews, removes int
	tool                     types.CodeTool
}

func (h *integrationCodeToolsHost) ReadCodeToolInventory(context.Context, int, int) (types.CodeToolInventory, error) {
	h.reads++
	out := types.CodeToolInventory{TotalBytes: 120}
	if h.removes == 0 {
		out.Tools = []types.CodeTool{h.tool}
	}
	return out, nil
}
func (h *integrationCodeToolsHost) PreviewCodeToolRemoval(context.Context, []string, bool) (types.CodeToolRemovalPreview, error) {
	h.previews++
	return types.CodeToolRemovalPreview{Tools: []types.CodeTool{h.tool}, Paths: []string{h.tool.Dir}, Bytes: 128, Token: "one-native-preview"}, nil
}
func (h *integrationCodeToolsHost) RemoveCodeTools(context.Context, types.CodeToolRemovalPreview) error {
	h.removes++
	return nil
}

func integrationCodeToolsSettings(t *testing.T, app *App) *settingsui.WorkspaceSettingsSurface {
	t.Helper()
	body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil {
		t.Fatal("Settings not mounted")
	}
	for i, c := range body.Model().Categories() {
		if c.Name == "Language Tools" {
			body.Model().SelectCategory(i)
			body.SetModel(body.Model())
			return body
		}
	}
	t.Fatal("Language Tools page absent")
	return nil
}

func TestCodeToolsMountedSettingsJourneyAndSafeConfirmation(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			host := &integrationCodeToolsHost{Workspace: app.ws, tool: types.CodeTool{ID: "fixture", Package: "fixture", Version: "v1", Dir: "C:/managed/fixture", Size: 120, Source: "fixture registry", FileEntries: []types.CodeToolFile{{Path: "files/server", Size: 120, Kind: "file"}}}}
			app.ws = host
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			app.openApplicationSettings()
			body := integrationCodeToolsSettings(t, app)
			if host.reads != 0 {
				t.Fatal("opening settings did blocking I/O")
			}
			runAppCommand(t, app, app.drainRemoteUICommands())
			body.FocusForm()
			for _, r := range "fixture" {
				app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			paint := ansi.Strip(app.View().RenderContent())
			if !strings.Contains(paint, "Language Tools") || !strings.Contains(paint, "fixture v1") || !strings.Contains(paint, "Crude") {
				t.Fatalf("mounted inventory unavailable:\n%s", paint)
			}
			runAppCommand(t, app, body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove-unused"}).Cmd)
			if host.removes != 0 || host.previews != 1 {
				t.Fatal("preview changed files")
			}
			runAppCommand(t, app, body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"}).Cmd)
			if app.settingsPrompt == nil {
				t.Fatal("native confirmation not mounted")
			}
			_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, cmd)
			if host.removes != 0 {
				t.Fatal("safe default removed tools")
			}
			runAppCommand(t, app, body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"}).Cmd)
			runAppCommand(t, app, app.activateSettingsPrompt(0))
			if host.removes != 1 {
				t.Fatal("explicit native removal not routed")
			}
			late := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.refresh"}).Cmd()
			app.overlayHost.Close(ui.OverlayWorkspaceSettings)
			app.openApplicationSettings()
			replacement := integrationCodeToolsSettings(t, app)
			runAppCommand(t, app, func() tea.Msg { return late })
			if replacement.Model() == body.Model() {
				t.Fatal("reopened Settings reused old owner")
			}
		})
	}
}
