package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

const soleVisualInitial = "package p\n\npr"
const soleVisualAccepted = "package p\nimport \"fmt\"\n\nfmt.Println(value)"

// A deterministic protocol reply isolates the sole unchosen-row path (not the
// already-covered inline preview). Real terminal input owns i, Tab, Undo, Redo.
func TestSoleCompletionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_SOLE_COMPLETION") != "1" {
		t.Skip("dedicated native completion journey")
	}
	runSoleCompletionVisual(t, false, 0, 0)
}
func TestSoleCompletionVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runSoleCompletionVisual(t, true, size[0], size[1]) })
	}
}

type soleCompletionVisualModel struct {
	*featureVisualModel
	retained *workspace.Workspace
	phase    int
	choices  int
}
type soleCompletionVisualDone struct {
	phase int
	err   error
}

func runSoleCompletionVisual(t *testing.T, headless bool, width, height int) {
	var m *soleCompletionVisualModel
	// Registered before the connected fixture: this record follows its joined
	// shell/transport/provider cleanup, not merely Program.Run returning.
	t.Cleanup(func() {
		if m != nil {
			if err := m.evidence(fmt.Sprintf("SOLE_JOIN phase=%d passed=%t headless=%t", m.phase, !t.Failed(), headless)); err != nil {
				t.Error(err)
			}
		}
	})
	shells, eds, retained, engine, path := newTypingCompletionFixture(t, soleVisualInitial)
	eds[0].SetCursorPosition(2, 2)
	engine.base.mu.Lock()
	engine.response = `[{"label":"printValue","filterText":"printValue","kind":3,"insertTextFormat":2,"textEdit":{"range":{"start":{"line":2,"character":0},"end":{"line":2,"character":3}},"newText":"fmt.Println(${1:value})$0"},"additionalTextEdits":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import \"fmt\"\n"}]}]`
	engine.base.mu.Unlock()
	ctx, cancel := context.WithCancel(t.Context())
	m = &soleCompletionVisualModel{featureVisualModel: &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: shells[0].app, ctx: ctx, path: path},
		settled:            make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_SOLE_RESULT"),
	}, retained: retained}
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
			send(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			if !observe() {
				return
			}
			send(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
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
	if m.phase != 5 || m.choices != 0 {
		t.Fatal("sole suggestion required a choice or journey was incomplete", m.phase, m.choices)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != soleVisualInitial {
		t.Fatal("completion changed disk", string(data), err)
	}
}
func (m *soleCompletionVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *soleCompletionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case soleCompletionVisualDone:
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
			if err := m.evidence(fmt.Sprintf("SOLE_READY phase=%d", phase)); err != nil {
				return soleCompletionVisualDone{-1, err}
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
			if m.busy || m.phase >= 5 {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return soleCompletionVisualDone{phase, m.observeSole(phase)} }
		}
		if v.Code == tea.KeyDown || v.Code == tea.KeyUp {
			m.choices++
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
func (m *soleCompletionVisualModel) observeSole(phase int) error {
	want := soleVisualInitial
	if phase == 1 || phase == 3 {
		want += "i"
	}
	if phase == 2 || phase == 4 {
		want = soleVisualAccepted
	}
	var frame, documentID string
	var revision uint64
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		state := ed.DocumentState()
		documentID, revision = state.DocumentID, state.Revision
		if ed.Value() != want || state.InFlight || state.PendingEdits != 0 || state.HistoryQueued || state.UndoPending || state.Conflict != nil {
			return fmt.Errorf("phase %d source/history not settled: %q %+v", phase, ed.Value(), state)
		}
		if v.Input.Value() != "private draft" || v.focusTarget() != FocusEditor || m.choices != 0 {
			return fmt.Errorf("editor/draft/choice ownership changed")
		}
		frame = ansi.Strip(v.View().RenderContent())
		if phase == 1 {
			s := v.completion
			if s == nil || s.screen == nil || !s.automatic || s.screen.SelectedID() != "" || s.screen.SoleID() == "" || !strings.Contains(frame, "printValue") {
				return fmt.Errorf("sole passive row not painted")
			}
		} else if v.completion != nil {
			return fmt.Errorf("completion retained after input")
		}
		if phase == 2 {
			if !ed.SnippetActive() || ed.SelectedText() != "value" || !strings.Contains(frame, "fmt.Println(value)") {
				return fmt.Errorf("Tab lost snippet or import")
			}
		}
		return nil
	})
	if err != nil {
		return errors.Join(err, m.evidence(fmt.Sprintf("SOLE_FAILED phase=%d: %v", phase, err)))
	}
	snapshot, snapshotErr := m.retained.LiveDocuments.Snapshot(documentID)
	if snapshotErr != nil || snapshot.Revision != revision || string(snapshot.Text) != want {
		return fmt.Errorf("canonical completion/history differs: %v", snapshotErr)
	}
	return m.evidence(fmt.Sprintf("SOLE_VERIFIED phase=%d choices=0\n%s", phase, frame))
}
