package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/gitdiff"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh fixture. All pointer, palette, search and editor
// interactions are production routes; only Git data and theme/exit keys are seeded.
func TestIDEQualityVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_IDE_QUALITY") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.Tree.Visible = false
	short := "package sample\n\nfunc Greet() string {\n\treturn \"landing_target\"\n}\n"
	path := filepath.Join(app.ws.RootPath(), "sample.go")
	if err := os.WriteFile(path, []byte(short), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app.ws.RootPath(), "navigation.go"), []byte("package navigation\n\n// destination_marker\nfunc Destination() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	openEditorTabs(t, app, "notes.md", "sample.go")
	app.EditorState.Editor.ReplaceDocument(strings.ReplaceAll(short, "landing_target", "landing_target refined"))
	model := &ideQualityVisualModel{App: app, t: t, before: short, source: path}
	p := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(p)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type ideQualityVisualModel struct {
	*App
	t              *testing.T
	before, source string
	light          bool
}

func (*ideQualityVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *ideQualityVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF5:
			m.activateEditorTab(m.source)
			m.FocusStack.Clear()
			return m, nil
		case tea.KeyF6:
			m.openGitReview("", "")
			m.activeGitReview().screen.ShowChanges(gitdiff.PrepareComparisons([]workspace.GitComparison{{Path: "sample.go", BaseLabel: "HEAD", TargetLabel: "Index", Before: m.before, After: "package sample\n\n// Reviewed source version\nfunc Greet() string {\n\treturn \"git_target\"\n}\n", Fingerprint: "visual-read-only", Historical: true, BeforePresent: true, AfterPresent: true}}), "main")
			m.activeGitReview().screen.SetPresentation(gitdiff.Split)
			return m, nil
		case tea.KeyF7:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			if m.rootHost.HasPointerCapture() {
				m.t.Error("capture left armed")
			}
			if ed := m.EditorState.Tabs[m.source].Editor; !ed.Modified() || !strings.Contains(ed.Value(), "refined") {
				m.t.Error("read-only review destroyed dirty source")
			}
			if out := os.Getenv("CRUDE_VISUAL_PROOF"); out != "" {
				if err := os.WriteFile(out, []byte(fmt.Sprintf("completed tabs=%d local-comparisons=%d\n", m.Win.TabCount(), len(m.localDiffs))), 0o600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
