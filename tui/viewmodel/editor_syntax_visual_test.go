package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
)

// The repository tuishot driver opens this isolated real shell. Navigation,
// editing, undo and resizing remain production paths; F6/F7 jump far from the
// delimiter, F8 changes theme and F9 exits without changing a user's workspace.
func TestEditorSyntaxVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_SYNTAX") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	app, _ := newRealShellApp(t)
	app.Tree.Visible = false
	path := filepath.Join(app.ws.RootPath(), "long.py")
	src := "def explain():\n    \"\"\"\n" + strings.Repeat("    documentation = not_executable_code  # still a docstring\n", 900) + "    \"\"\"\n    return 42\n\n# actual comment\ndef after():\n    return 7\n"
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	openEditorTabs(t, app, "long.py")
	ed := app.EditorState.Tabs[path].Editor
	model := &syntaxVisualModel{App: app, editor: ed}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type syntaxVisualModel struct {
	*App
	editor *editor.Editor
	light  bool
}

func (*syntaxVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *syntaxVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF6:
			m.editor.SetCursorPosition(600, 4)
			m.editor.ScrollToCursor()
			return m, nil
		case tea.KeyF7:
			m.editor.SetCursorPosition(903, 4)
			m.editor.ScrollToCursor()
			return m, nil
		case tea.KeyF8:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
