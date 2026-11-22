package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/findinfiles"
	"crude/workspace"
)

// The capture driver uses normal shortcut, query, Down and Enter events. F4
// only inspects the resulting window; F9 exits without performing an action.
func TestGoToFileNativeCapture(t *testing.T) {
	out := os.Getenv("CRUDE_GO_TO_FILE_CAPTURE")
	if out == "" {
		t.Skip("requires tuishot")
	}
	app, path := fileLinkApp(t, 160, 40)
	root := wsOf(app).RootPath
	other := filepath.Join(root, "second", "main.go")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("package second\n// Second source file.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app.eventBus = workspace.NewEventBus()
	wsOf(app).SetEventBus(app.eventBus)
	v := &goToFileCapture{App: app}
	p := tea.NewProgram(v)
	app.SetProgram(p)
	defer app.SetProgram(nil)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !v.previewed || app.findInFiles != nil || app.EditorState.Editor == nil || app.EditorState.Editor.FilePath() != v.previewPath || app.Input.Value() != fileLinkDraft {
		t.Fatalf("native file journey: preview=%t chosen=%q expected one of %q, %q", v.previewed, v.previewPath, path, other)
	}
	if err := os.WriteFile(out, []byte("PASS\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

type goToFileCapture struct {
	*App
	previewed   bool
	previewPath string
}

func (v *goToFileCapture) Init() tea.Cmd { return tickCmd() }
func (v *goToFileCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.Code {
		case tea.KeyF4:
			s := v.findInFiles
			if s != nil && s.Mode() == findinfiles.ModeFiles && s.Query() == "main.go" && s.DisplayRowCount() == 2 && s.SyntaxEditor() != nil {
				v.previewed = true
				v.previewPath = v.chatFilePath(s.SelectedFilePath())
			}
			return v, nil
		case tea.KeyF9:
			return v, tea.Quit
		}
	}
	_, cmd := v.App.Update(msg)
	return v, cmd
}
