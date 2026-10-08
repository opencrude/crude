package viewmodel

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"github.com/charmbracelet/x/ansi"
)

// Sequential scripts/tuishot.sh fixture; isolated settings, no provider or server.
func TestLSPVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_LSP") != "1" {
		t.Skip("dedicated-terminal visual acceptance only")
	}
	app, _ := newRealShellApp(t)
	model := &lspVisualModel{App: app}
	p := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
}

type lspVisualModel struct{ *App }

func (m *lspVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *lspVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF6:
			m.openApplicationSettings()
			fw := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
			if fw != nil {
				body := m.settingsSurface(fw)
				for i, category := range body.Model().Categories() {
					if category.Name == "Language Servers" {
						body.Model().SelectCategory(i)
						break
					}
				}
				body.SetModel(body.Model())
				if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
					frame.Host().MarkLayout()
				}
				fw.MarkContentDirty()
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
