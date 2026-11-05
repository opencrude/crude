package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// This fixture mounts a real shell with a file tree and three file tabs for
// the repository capture workflow (scripts/tuishot.sh), which drags tabs and
// tree rows with the real pointer. F9 exits.
func TestDragVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_DRAG") != "1" {
		t.Skip("dedicated sequential native capture")
	}
	app, _ := newRealShellApp(t)
	root := app.ws.RootPath()
	for path, data := range map[string]string{
		"alpha.go": "package alpha\n", "beta.go": "package beta\n", "gamma.go": "package gamma\n",
		"notes.txt": "notes", "target/inner.txt": "inner",
	} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app.registerOverlayHandlers()
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	openEditorTabs(t, app, "alpha.go", "beta.go", "gamma.go")
	app.setActivity(activityFiles, true)
	runAppCommand(t, app, app.Tree.Model.RefreshFromDisk())
	model := &dragVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type dragVisualModel struct {
	*App
}

func (*dragVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *dragVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
