package widgets

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/widget"
)

type WorkspaceSettingsSaveMsg struct {
	Settings ttypes.AppSettings
}

type WorkspaceSettingsCancelMsg struct{}

type WorkspaceSettings struct {
	model *settings.Model
	th    *theme.Theme
}

func NewWorkspaceSettings(th *theme.Theme, appSets ttypes.AppSettings, presets []ttypes.ProviderPreset) *WorkspaceSettings {
	return &WorkspaceSettings{
		model: settings.New(th, appSets, presets),
		th:    th,
	}
}

func (ws *WorkspaceSettings) HandleEvent(msg tea.Msg) tea.Cmd {
	updated, cmd := ws.model.Update(msg)
	ws.model = &updated
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		result := cmd()
		switch r := result.(type) {
		case *settings.SaveMsg:
			return WorkspaceSettingsSaveMsg{Settings: r.AppSettings}
		case *settings.CancelMsg:
			return WorkspaceSettingsCancelMsg{}
		}
		return result
	}
}

func (ws *WorkspaceSettings) Render(surf *surface.Surface, ctx *widget.RenderContext) {
	w, h := surf.Width(), surf.Height()
	if w < 30 || h < 10 {
		return
	}

	lines := ws.model.View(w, h)
	result := strings.Join(lines, "\n")
	rendering.RenderToSurface(surf, result)
}

func (ws *WorkspaceSettings) Measure(availW, availH int) (int, int) {
	return availW, availH
}
