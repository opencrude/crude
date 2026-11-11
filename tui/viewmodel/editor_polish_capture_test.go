package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"os"
	"path/filepath"
	"testing"
)

// The driver uses native Tab, Ctrl+/, then F9. F9 inspects only state and the
// last painted surface; it never makes the expected edit or calls View.
func TestEditorPolishNative(t *testing.T) {
	if os.Getenv("CRUDE_POLISH_CAPTURE") != "1" {
		t.Skip("native capture only")
	}
	app, ws := newRealShellApp(t)
	restrictFixture(t, ws)
	path := filepath.Join(ws.RootPath, "polish.html")
	if err := os.WriteFile(path, []byte("<section>\n  <p>hello</p>\n</section>\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	app.setFocusEditor()
	app.EditorState.Editor.SetCursorPosition(1, 2)
	m := &polishCapture{App: app}
	program := tea.NewProgram(m)
	app.SetProgram(program)
	_, err := program.Run()
	app.StopAsyncIO()
	if err != nil {
		t.Fatal(err)
	}
	if !m.checked {
		t.Fatal("native Tab/comment did not produce expected text and paint")
	}
	if p := os.Getenv("CRUDE_POLISH_RESULT"); p != "" {
		if err := os.WriteFile(p, []byte("NATIVE_EDITOR_POLISH_PASS\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

type polishCapture struct {
	*App
	checked bool
}

func (m *polishCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		m.checked = m.EditorState.Editor.Value() == "<section>\n    <!-- <p>hello</p> -->\n</section>\n" && bufferContainsPlain(m.mainSurf.Buffer(), "<!-- <p>hello</p> -->")
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
