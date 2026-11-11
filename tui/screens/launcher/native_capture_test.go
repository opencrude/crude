package launcher

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/compositor"
	"crude/tui/ui"
	"crude/workspace"
	"os"
	"testing"
)

// Opt-in native inspection of the exact golden fixture. F9 exits; tests never
// print while the terminal renderer is active.
func TestLauncherNativeGolden(t *testing.T) {
	if os.Getenv("CRUDE_POLISH_CAPTURE") != "1" {
		t.Skip("native capture only")
	}
	_, host, _ := testScreen(t, 110, 16, []workspace.WorkspaceInfo{{ID: "alpha", ProjectName: "alpha", ProjectDir: "/projects/alpha", SessionCount: 2}})
	m := &nativeLauncher{host: host, paint: compositor.NewCompositor(110, 16)}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		t.Fatal(err)
	}
}

type nativeLauncher struct {
	host  *ui.Host
	paint *compositor.Compositor
}

func (m *nativeLauncher) Init() tea.Cmd { return nil }
func (m *nativeLauncher) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	if s, ok := msg.(tea.WindowSizeMsg); ok {
		m.host.Resize(s.Width, s.Height)
		m.paint.Resize(s.Width, s.Height)
	}
	m.host.Handle(msg)
	return m, nil
}
func (m *nativeLauncher) View() tea.View {
	m.paint.BeginFrame()
	m.host.Layout()
	m.host.Paint(m.paint.Root().Buffer())
	m.paint.Composite()
	v := tea.NewView(m.paint.Present())
	v.AltScreen = true
	return v
}
