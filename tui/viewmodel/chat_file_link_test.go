package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
	"crude/types"
)

const fileLinkSource = "package demo\n\t界 target\n// سلام دنیا | שלום עולם\n"
const fileLinkDraft = "سلام دنیا | KEEP MY DRAFT"

func fileLinkApp(t *testing.T, width, height int) (*App, string) {
	t.Helper()
	app := newTranscriptAttachmentApp(t)
	app.width, app.height = width, height
	root := t.TempDir()
	wsOf(app).RootPath = root
	app.Tree.Visible = false
	app.BufferMgr = editor.NewBufferManager()
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fileLinkSource), 0600); err != nil {
		t.Fatal(err)
	}
	app.appendActiveMessage(types.ChatMessage{InternalID: "files", Role: types.RoleAssistant,
		Content: "Open `src/main.go:2:4`.\n\nسلام دنیا، این متن فارسی است.\n\nשלום עולם, mixed English 123."})
	app.Input.SetInput(fileLinkDraft)
	app.setFocusInput()
	return app, path
}

func paintedFileLink(app *App) (x, y int, ok bool) {
	app.paintShellFrame()
	buf := app.mainSurf.Buffer()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
		for x := app.chatRect.X; x < app.chatRect.X+app.chatRect.W; x++ {
			if link := buf.Get(x, y).Link; link.File() && link.Target() == "src/main.go:2:4" {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func verifyFileLinkLanding(app *App, path string) error {
	ed := app.EditorState.Editor
	if ed == nil || ed.FilePath() != path || ed.Value() != fileLinkSource || ed.CursorLine() != 1 || ed.CursorCol() != 7 {
		return fmt.Errorf("file link did not land on the original source at 2:4")
	}
	if app.Input.Value() != fileLinkDraft {
		return fmt.Errorf("file navigation changed the composer draft")
	}
	return nil
}

func TestChatFileLinksOpenFromPaintedTextAndPreserveDraft(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, path := fileLinkApp(t, size[0], size[1])
			x, y, ok := paintedFileLink(app)
			if !ok {
				t.Fatal("no file link painted")
			}
			t.Logf("FILE_LINK_POINT size=%dx%d point=%d,%d", size[0], size[1], x, y)
			footer := rowText(app, app.height-1)
			if strings.Contains(footer, "history") || strings.Contains(footer, "find in chat") || !strings.Contains(footer, "new line") ||
				size[0] >= 120 && !strings.Contains(footer, "attach file") {
				t.Fatalf("wrong composer guidance: %q", footer)
			}
			_, press := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if app.EditorState.ActiveView != "" {
				t.Fatal("press opened the file before release")
			}
			_, release := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			runAppCommand(t, app, press)
			runAppCommand(t, app, release)
			if err := verifyFileLinkLanding(app, path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatFileOpenKeepsTheCanonicalUnsavedDocumentAndFencesLateResults(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(fileLinkSource), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	ed := liveTestOpen(t, app, path)
	ed.SetCursorPosition(0, 0)
	ed.InsertRune('!')
	settleLive(t, app)
	runAppCommand(t, app, app.openChatFile("main.go#L2C4"))
	if app.EditorState.Editor.Value() != "!"+fileLinkSource || !app.EditorState.Editor.Modified() || app.EditorState.Editor.CursorLine() != 1 || app.EditorState.Editor.CursorCol() != 7 {
		t.Fatal("a file link replaced unsaved canonical text or lost its location")
	}
	late := app.openChatFile("main.go:1")
	msg := late().(paneFilesOpenedMsg)
	app.workspaceSwitchGen.Add(1)
	runAppCommand(t, app, app.applyPaneFilesOpened(msg))
	if app.EditorState.Editor.CursorLine() != 1 {
		t.Fatal("an old workspace open moved the current caret")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != fileLinkSource {
		t.Fatal("opening a file link wrote to disk", err)
	}
}

// Native capture uses ordinary mouse input. F4 only observes, F9 exits.
func TestChatFileLinksVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_FILE_LINKS") != "1" {
		t.Skip("requires scripts/tuishot.sh")
	}
	app, path := fileLinkApp(t, 160, 40)
	model := &fileLinkVisualModel{App: app, path: path}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if model.failure != nil || model.phase != 2 {
		t.Fatalf("file-link native journey: phase=%d err=%v", model.phase, model.failure)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != fileLinkSource {
		t.Fatal("native link navigation changed the source", err)
	}
}

type fileLinkVisualModel struct {
	*App
	path    string
	phase   int
	failure error
}

type fileLinkEvidenceMsg struct {
	err  error
	quit bool
}

func (m *fileLinkVisualModel) Init() tea.Cmd {
	// This synthetic App does not use NewApp's settings bootstrap. Publish the
	// same policy and host mode before native paint, especially in VTE.
	m.applyTextDirectionSettings(m.ws.LoadAppSettings())
	return tickCmd()
}
func (m *fileLinkVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(fileLinkEvidenceMsg); ok {
		if result.err != nil {
			m.failure = result.err
		}
		if m.failure != nil || result.quit {
			return m, tea.Quit
		}
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF4:
			var evidence string
			if m.phase == 0 {
				x, y, ok := paintedFileLink(m.App)
				if !ok {
					m.failure = fmt.Errorf("no file link in the native frame")
				} else {
					m.phase++
					evidence = fmt.Sprintf("FILE_LINK_POINT %d,%d\nFILE_LINK_READY phase=1\n", x, y)
				}
			} else {
				m.failure = verifyFileLinkLanding(m.App, m.path)
				if m.failure == nil {
					m.phase++
					evidence = "FILE_LINK_READY phase=2\n"
				}
			}
			if m.failure != nil {
				evidence = "FILE_LINK_FAILED " + m.failure.Error() + "\n"
			}
			return m, func() tea.Msg { return fileLinkEvidenceMsg{err: m.evidence(evidence)} }
		case tea.KeyF9:
			return m, func() tea.Msg { return fileLinkEvidenceMsg{err: m.evidence("FILE_LINK_SUCCESS\n"), quit: true} }
		}
	}
	_, command := m.App.Update(msg)
	return m, command
}

func (m *fileLinkVisualModel) evidence(value string) error {
	file, err := os.OpenFile(os.Getenv("CRUDE_FILE_LINK_RESULT"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}
