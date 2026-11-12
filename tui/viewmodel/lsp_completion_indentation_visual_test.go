package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/indentation"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

const indentCompletionInitial = "package p\r\n\r\n\tpr\r\nend\r\n"
const indentCompletionTyped = "package p\r\n\r\n\tpri\r\nend\r\n"
const indentCompletionAccepted = "package p\r\nimport \"fmt\"\r\n\r\n\tif (界) {\r\n\t\tuse(界);\r\n\t}\r\nend\r\n"
const indentCompletionEdited = "package p\r\nimport \"fmt\"\r\n\r\n\tif (Z) {\r\n\t\tuse(Z);\r\n\t}\r\nend\r\n"

// This fixture supplies deterministic protocol evidence. Native keys, the real
// completion coordinator, shared source, snippet owner and Undo perform the work.
// It is not evidence about a separately installed server's offered items.
func TestCompletionIndentVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPLETION_INDENT") != "1" {
		t.Skip("dedicated native multiline completion journey")
	}
	runCompletionIndentVisual(t, false, 0, 0)
}
func TestCompletionIndentVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runCompletionIndentVisual(t, true, size[0], size[1]) })
	}
}

type completionIndentVisualModel struct {
	*featureVisualModel
	retained       *workspace.Workspace
	phase, choices int
}
type completionIndentVisualDone struct {
	phase int
	err   error
}

func runCompletionIndentVisual(t *testing.T, headless bool, width, height int) {
	var m *completionIndentVisualModel
	t.Cleanup(func() {
		if m != nil {
			if err := m.evidence(fmt.Sprintf("COMPLETION_INDENT_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
				t.Error(err)
			}
		}
	})
	shells, eds, retained, provider, path := newTypingCompletionFixture(t, indentCompletionInitial)
	if err := eds[0].SetIndentationChoices(indentation.Choices{Style: indentation.Tabs, Size: 4, TabWidth: 4}); err != nil {
		t.Fatal(err)
	}
	eds[0].SetCursorPosition(2, 6)
	provider.base.mu.Lock()
	provider.response = `[{"label":"print block","filterText":"printBlock","insertTextFormat":2,"insertTextMode":2,"textEdit":{"range":{"start":{"line":2,"character":1},"end":{"line":2,"character":4}},"newText":"if (${1:界}) {\n${2:    use($1);}\n}$0"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\r\n"}]}]`
	provider.base.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	m = &completionIndentVisualModel{featureVisualModel: &featureVisualModel{controlVisualModel: &controlVisualModel{App: shells[0].app, ctx: ctx, path: path}, settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_COMPLETION_INDENT_RESULT")}, retained: retained}
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	workers.Go(func() {
		for {
			select {
			case msg := <-shells[0].fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	})
	if headless {
		workers.Go(func() {
			send := func(msg tea.Msg) { program.Send(msg) }
			observe := func() bool {
				send(tea.KeyPressMsg{Code: tea.KeyF4})
				select {
				case <-m.settled:
					return true
				case <-ctx.Done():
					return false
				}
			}
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'i', Text: "i"})
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: tea.KeyTab})
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if observe() {
				send(tea.KeyPressMsg{Code: tea.KeyF9})
			}
		})
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 6 || m.choices != 0 {
		t.Fatal("completion journey incomplete or required a choice", m.phase, m.choices)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != indentCompletionInitial {
		t.Fatal("completion changed disk", err)
	}
}
func (m *completionIndentVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *completionIndentVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case completionIndentVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default:
		}
		phase := m.phase
		return m, func() tea.Msg {
			if err := m.evidence(fmt.Sprintf("COMPLETION_INDENT_READY phase=%d", phase)); err != nil {
				return completionIndentVisualDone{-1, err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF4 {
			if m.busy || m.phase >= 6 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return completionIndentVisualDone{phase, m.observeIndentCompletion(phase)} }
		}
		if v.Code == tea.KeyDown || v.Code == tea.KeyUp {
			m.choices++
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *completionIndentVisualModel) observeIndentCompletion(phase int) error {
	want := indentCompletionInitial
	switch phase {
	case 1, 5:
		want = indentCompletionTyped
	case 2, 4:
		want = indentCompletionAccepted
	case 3:
		want = indentCompletionEdited
	}
	var frame, documentID string
	var revision uint64
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		state := ed.DocumentState()
		documentID, revision = state.DocumentID, state.Revision
		if ed.Value() != want || state.InFlight || state.PendingEdits != 0 || state.HistoryQueued || state.UndoPending || state.Conflict != nil {
			return fmt.Errorf("source or history not settled: %q", ed.Value())
		}
		if v.Input.Value() != "private draft" || v.focusTarget() != FocusEditor || m.choices != 0 {
			return fmt.Errorf("editor, draft or choice ownership changed")
		}
		frame = ansi.Strip(v.View().RenderContent())
		if phase == 1 {
			s := v.completion
			if s == nil || s.screen == nil || !s.automatic || s.screen.SelectedID() != "" || s.screen.SoleID() == "" || !strings.Contains(frame, "print block") {
				return fmt.Errorf("adjusted suggestion not visible and passive")
			}
		} else if v.completion != nil {
			return fmt.Errorf("completion stayed open after input")
		}
		if phase == 2 && (!ed.SnippetActive() || ed.SelectedText() != "界" || !strings.Contains(frame, "use(") || !strings.Contains(frame, "import")) {
			return fmt.Errorf("adjusted snippet or import was not painted")
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("COMPLETION_INDENT_FAILED phase=%d %v\n%s", phase, err, frame))
		return err
	}
	snapshot, err := m.retained.LiveDocuments.Snapshot(documentID)
	if err != nil || snapshot.Revision != revision || string(snapshot.Text) != want {
		return fmt.Errorf("canonical source differs: %v", err)
	}
	return m.evidence(fmt.Sprintf("COMPLETION_INDENT_VERIFIED phase=%d choices=0\n%s", phase, frame))
}
