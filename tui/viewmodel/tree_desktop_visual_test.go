package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// This fixture mounts the actual file tree and context menu for the repository
// capture workflow. It never opens an external application or the user's data.
// F2 opens the tree menu, F3 opens an inactive file tab's menu, F9 exits.
func TestTreeDesktopRevealVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_TREE_REVEAL") != "1" {
		t.Skip("dedicated sequential native capture")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reveal.txt"), []byte("Tree desktop reveal acceptance"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden.txt"), []byte("Hidden file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("Other tab"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	app.registerOverlayHandlers()
	// The visual fixture starts after dismissing the unrelated trust question.
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	for _, name := range []string{"reveal.txt", "other.txt"} {
		if _, err := app.openEditorTab(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	app.setActivity(activityFiles, true)
	model := &treeDesktopVisualModel{App: app, t: t, target: filepath.Join(root, "reveal.txt")}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type treeDesktopVisualModel struct {
	*App
	t      *testing.T
	target string
}

func (*treeDesktopVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *treeDesktopVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			x, y := editorTreePoint(m.t, m.App, "reveal.txt")
			_, cmd := m.App.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
			return m, cmd
		case tea.KeyF3:
			tab := m.Win.TabByTitle(m.editorTabTitle(m.target))
			if tab == nil {
				m.t.Fatal("visual fixture file tab is missing")
			}
			x, y := paneTabPoint(m.t, m.App, tab.ID)
			_, cmd := m.App.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
			return m, cmd
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
