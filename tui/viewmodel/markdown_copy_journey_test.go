package viewmodel

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/rendering"
	"crude/types"
	"crude/workspace"
)

const markdownCopyFixture = "Run `alpha beta` next.\n\n- Example:\n\n  ```python\n  if ready:\n      print(\"hi\")\n  ```"
const markdownCopyCode = "if ready:\n    print(\"hi\")\n"

type markdownCopyTargets struct{ startX, endX, inlineY, codeX, codeY int }

func markdownCopyCoordinates(app *App) (markdownCopyTargets, error) {
	var target markdownCopyTargets
	foundText, foundCode := false, false
	app.paintShellFrame()
	for y := 0; y < app.height; y++ {
		text := rowText(app, y)
		if pos := strings.Index(text, "alpha beta"); pos >= 0 {
			target.startX = rendering.VisibleWidth(text[:pos]) - 1
			target.endX = target.startX + 12
			target.inlineY = y
			foundText = true
		}
		if pos := strings.Index(text, "Copy code"); pos >= 0 {
			target.codeX = rendering.VisibleWidth(text[:pos]) + 2
			target.codeY = y
			foundCode = true
		}
	}
	if !foundText || !foundCode {
		return target, fmt.Errorf("copy controls not painted:\n%s", shellText(app))
	}
	return target, nil
}
func seedMarkdownCopy(app *App) {
	state := app.ws.AgentMgr().Get(app.AgentView.ActiveID()).State
	state.Messages.AppendMessage(workspace.Message{Role: types.RoleAssistant, Content: markdownCopyFixture})
	app.syncAgentMessages(state.ID, false)
	app.Input.SetInput("Keep this draft.")
}

func TestMarkdownSourceCopyThroughMountedHost(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {160, 40}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app := setupChatInputApp(t, size[0], size[1], false)
			wsOf(app).Logger = stubLogger{}
			seedMarkdownCopy(app)
			next := recordingClipboard(t, app)
			target, err := markdownCopyCoordinates(app)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("COPY_TARGET %dx%d drag:%d,%d,%d,%d click:%d,%d", size[0], size[1], target.startX, target.inlineY, target.endX, target.inlineY, target.codeX, target.codeY)
			run := func(msg tea.Msg) { _, cmd := app.Update(msg); runAppCommand(t, app, cmd); app.paintShellFrame() }
			run(tea.MouseClickMsg{X: target.startX, Y: target.inlineY, Button: tea.MouseLeft})
			run(tea.MouseMotionMsg{X: target.endX, Y: target.inlineY, Button: tea.MouseLeft})
			if app.chatScreen.SelectedText() != "alpha beta" {
				t.Fatalf("native selection=%q", app.chatScreen.SelectedText())
			}
			// The shell copy command uses the same source selection as release-copy;
			// cancel the test drag without touching the user's actual clipboard.
			run(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if got := next(); got != "alpha beta" {
				t.Fatalf("clipboard selection=%q", got)
			}
			app.rootHost.CancelPointerCapture()
			app.chatScreen.ClearSelection()
			run(tea.MouseClickMsg{X: target.startX, Y: target.inlineY, Button: tea.MouseLeft})
			run(tea.MouseMotionMsg{X: target.endX, Y: target.inlineY, Button: tea.MouseLeft})
			run(tea.MouseReleaseMsg{X: target.endX, Y: target.inlineY, Button: tea.MouseLeft})
			if got := next(); got != "alpha beta" {
				t.Fatalf("release clipboard=%q", got)
			}
			run(tea.MouseClickMsg{X: target.codeX, Y: target.codeY, Button: tea.MouseLeft})
			run(tea.MouseReleaseMsg{X: target.codeX, Y: target.codeY, Button: tea.MouseLeft})
			if got := next(); got != markdownCopyCode {
				t.Fatalf("button clipboard=%q", got)
			}
			activateMenuItem(t, app.buildChatContextMenu(tea.Mouse{}), "Copy last code block")
			if got := next(); got != markdownCopyCode {
				t.Fatalf("last-block clipboard=%q", got)
			}
			if app.Input.Value() != "Keep this draft." {
				t.Fatal("copy consumed the composer draft")
			}
		})
	}
}

// This fixture is driven by scripts/tuishot.sh. Clipboard output is observed at
// the existing I/O worker boundary so native acceptance does not replace the
// user's clipboard. The script drags and releases a selection, then clicks the
// parsed block's actual chip. No test-only action substitutes for those inputs.
func TestMarkdownCopyVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_MD_COPY") != "1" {
		t.Skip("dedicated native Markdown copy journey")
	}
	app := setupChatInputApp(t, 160, 40, false)
	wsOf(app).Logger = stubLogger{}
	seedMarkdownCopy(app)
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	model := &markdownCopyVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	restoreClipboard := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		writeText: func(text string) error { program.Send(markdownVisualCopied(text)); return nil },
	})
	defer restoreClipboard()

	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if path := os.Getenv("CRUDE_COPY_RESULT"); path != "" {
		data := strings.Join(model.evidence, "\n") + fmt.Sprintf("\ninline=%t block=%t error=%v\n", model.inline, model.block, model.err)
		if model.err == nil && model.inline && model.block {
			data += "COPY_NATIVE_SUCCESS\n"
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if model.err != nil {
		t.Fatal(model.err)
	}
	if !model.inline || !model.block {
		t.Fatal("native copy journey did not finish")
	}

}

type markdownVisualCopied string
type markdownCopyVisualModel struct {
	*App
	last          string
	inline, block bool
	evidence      []string
	err           error
}

func (m *markdownCopyVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *markdownCopyVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if copied, ok := msg.(markdownVisualCopied); ok {
		m.last = string(copied)
		m.evidence = append(m.evidence, fmt.Sprintf("copied=%q", copied))
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			target, err := markdownCopyCoordinates(m.App)
			m.err = err
			m.evidence = append(m.evidence, fmt.Sprintf("size=%dx%d targets=%+v", m.width, m.height, target))
			if err != nil {
				return m, tea.Quit
			}
			return m, nil
		case tea.KeyF3:
			if m.last != "alpha beta" {
				m.err = fmt.Errorf("native inline clipboard=%q", m.last)
				return m, tea.Quit
			}
			m.inline = true
			return m, nil
		case tea.KeyF4:
			if m.last != markdownCopyCode {
				m.err = fmt.Errorf("native block clipboard=%q", m.last)
				return m, tea.Quit
			}
			m.block = true
			return m, nil
		case tea.KeyF9:
			if m.Input.Value() != "Keep this draft." {
				m.err = fmt.Errorf("native copy changed draft %q", m.Input.Value())
			}
			return m, tea.Quit
		}
	}
	before := m.chatScreen.SelectedText()
	_, cmd := m.App.Update(msg)
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.KeyPressMsg:
		a, b, c, d := m.chatScreen.SelectionRange()
		m.evidence = append(m.evidence, fmt.Sprintf("input=%#v before=%q after=%q range=%d:%d-%d:%d selecting=%t", msg, before, m.chatScreen.SelectedText(), a, b, c, d, m.chatScreen.IsSelecting()))
	}
	return m, cmd
}
