package viewmodel

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/rendering"
	"crude/types"
	"github.com/charmbracelet/x/ansi"
)

// A settled assistant message with the reporter's mix of Persian prose, an
// ordered list and a quote with embedded Latin text, wrapped table cells, an
// English phrase, a file link, and a section break.
const persianReview = `اما برای ادعای برتری نسبت به همه روش` + "\u200c" + `ها کافی نیست.

1. آزمایش اول با Go 1.24 اجرا شد.
2. نتیجه دوم: 12 مورد از 40 مورد.

> این بخش نقل قول است و کلمه English دارد.

---

### جمع` + "\u200c" + `بندی نهایی داور

| مورد | نظر من |
| --- | --- |
| آیا کل ایده تکراری است؟ | در مقالات بررسی` + "\u200c" + `شده، یک مقاله را ندیدم که دقیقاً همه اجزای صورت` + "\u200c" + `بندی شما را با همین هدف و شرایط کنار هم قرار دهد. |
| آزمون | active loop closure |
| منبع | [notes.md](notes.md) |

توصیه: کار بعدی را دقیق تعریف کنید.`

func tableVisualShell(t *testing.T) (remoteJourneyShell, string) {
	t.Helper()
	responder := &planReviewServer{replies: []string{persianReview}}
	srv := httptest.NewServer(http.HandlerFunc(responder.handler))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	path := writePaneFile(t, root, "notes.md", "# Reviewer notes\nA real local target.\n")
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, srv.URL), root)
	id := remoteJourneyCreate(t, shell)
	shell.app.Tree.Visible = false
	runAppCommand(t, shell.app, shell.app.submitInput(id, "Show the review.", nil))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.ws.AgentActivity(id).Status.Phase == types.AgentCompleted })
	shell.app.setFocusInput()
	return shell, path
}

type tablePointerTargets struct{ wordX, wordY, linkX, linkY int }

func tableTargets(app *App) (tablePointerTargets, error) {
	var pts tablePointerTargets
	pts.wordY = -1
	pts.linkY = -1
	for y := 0; y < app.height; y++ {
		line := rowText(app, y)
		if i := strings.Index(line, "active loop closure"); i >= 0 {
			pts.wordX, pts.wordY = rendering.VisibleWidth(line[:i]), y
		}
		if i := strings.Index(line, "notes.md"); pts.linkY < 0 && i >= 0 {
			pts.linkX, pts.linkY = rendering.VisibleWidth(line[:i])+2, y
		}
	}
	if pts.wordY < 0 || pts.linkY < 0 {
		return pts, fmt.Errorf("table targets not visible: %+v", pts)
	}
	return pts, nil
}

// Drives the same app at both widths. The emitted *cell* coordinates are also
// usable by the native capture driver; they come from the painted surface, not
// the bidi mapping under test.
func TestMixedTableLinkAndSelectionAfterResize(t *testing.T) {
	shell, path := tableVisualShell(t)
	app := shell.app
	next := recordingClipboard(t, app)
	for _, sz := range [][2]int{{160, 40}, {80, 24}, {160, 40}} {
		app.Update(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		app.paintShellFrame()
		p, err := tableTargets(app)
		if err != nil {
			t.Fatalf("%v\n%s", err, frameText(app))
		}
		t.Logf("grid=%dx%d drag:%d,%d,%d,%d click:%d,%d", sz[0], sz[1], p.wordX, p.wordY, p.wordX+len("active loop closure"), p.wordY, p.linkX, p.linkY)
		for _, msg := range []tea.Msg{tea.MouseClickMsg{X: p.wordX, Y: p.wordY, Button: tea.MouseLeft}, tea.MouseMotionMsg{X: p.wordX + 19, Y: p.wordY, Button: tea.MouseLeft}, tea.MouseReleaseMsg{X: p.wordX + 19, Y: p.wordY, Button: tea.MouseLeft}} {
			_, cmd := app.Update(msg)
			runAppCommand(t, app, cmd)
		}
		if got := next(); got != "active loop closure" {
			t.Fatalf("copy at %v = %q", sz, got)
		}
		app.chatScreen.ClearSelection()
	}
	p, _ := tableTargets(app)
	for _, msg := range []tea.Msg{tea.MouseClickMsg{X: p.linkX, Y: p.linkY, Button: tea.MouseLeft}, tea.MouseReleaseMsg{X: p.linkX, Y: p.linkY, Button: tea.MouseLeft}} {
		_, cmd := app.Update(msg)
		runAppCommand(t, app, cmd)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.EditorState.Editor != nil && app.EditorState.Editor.FilePath() == path })
}

// Use scripts/tuishot on a dedicated terminal. F8 observes pointer targets;
// F9 verifies real drag-release clipboard bytes and the file that opened.
func TestMixedTableNativeCapture(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_MIXED_TABLE") != "1" {
		t.Skip("native terminal capture")
	}
	shell, path := tableVisualShell(t)
	m := &tableCapture{App: shell.app, t: t, path: path}
	program := tea.NewProgram(m, tea.WithWidthMethod(ansi.GraphemeWidth))
	m.SetProgram(program)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	restore := installAsyncClipboardBackendForTest(m.App, clipboardAsyncBackend{writeText: func(s string) error { program.Send(markdownVisualCopied(s)); return nil }})
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type tableCapture struct {
	*App
	t            *testing.T
	path, copied string
}

func (m *tableCapture) Init() tea.Cmd { return tickCmd() }
func (m *tableCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if copied, ok := msg.(markdownVisualCopied); ok {
		m.copied = string(copied)
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF8:
			p, err := tableTargets(m.App)
			if err != nil {
				m.t.Error(err)
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path+".positions", []byte(fmt.Sprintf("%dx%d drag:%d,%d,%d,%d click:%d,%d\n", m.width, m.height, p.wordX, p.wordY, p.wordX+19, p.wordY, p.linkX, p.linkY)), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, nil
		case tea.KeyF9:
			actual := ""
			if m.EditorState.Editor != nil {
				actual = m.EditorState.Editor.FilePath()
			}
			if m.copied != "active loop closure" || actual != m.path {
				m.t.Errorf("native clipboard=%q; opened file=%q", m.copied, actual)
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("copied=%q open=%s passed=%t\n", m.copied, filepath.Base(actual), !m.t.Failed())), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
