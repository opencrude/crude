package viewmodel

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/theme"
	"crude/workspace"
)

// Opt-in native tuishot fixture: F2 starts a pending catalog refresh, F3
// settles it and F4 switches theme. All paint, focus and input use the App.
func TestSidebarControlsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_SIDEBAR_CONTROLS") != "1" {
		t.Skip("requires the dedicated tuishot terminal")
	}
	root := t.TempDir()
	app := newEditorPaintApp(t, root)
	wsOf(app).RootPath = root
	app.launcherScreen = nil
	app.workspaceInfos = []workspace.WorkspaceInfo{
		{ProjectDir: root, ProjectName: "ox"},
		{ProjectDir: filepath.Join(filepath.Dir(root), "crude"), ProjectName: "crude"},
	}
	app.Tree.Width = 36
	app.setActivity(activityWorkspaces, true)
	app.setFocusTree()
	model := &sidebarControlsVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type sidebarControlsVisualModel struct{ *App }

func (m *sidebarControlsVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *sidebarControlsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.refreshLauncherAsync(m.ws.RootPath())
			return m, nil
		case tea.KeyF3:
			m.applyWorkspaceListing(launcherWorkspacesMsg{Gen: m.launcherLoadGen.Load(), Infos: m.workspaceInfos})
			return m, nil
		case tea.KeyF4:
			m.Theme = theme.DefaultLight()
			m.rootHost.SetTheme(m.Theme)
			m.renderCtx.Theme = m.Theme
			m.activityList.SetTheme(m.Theme)
			return m, nil
		case tea.KeyF9:
			return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return tea.QuitMsg{} })
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
