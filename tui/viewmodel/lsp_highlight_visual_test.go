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
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
)

// Native capture: CRUDE_VISUAL_OCCURRENCES=1, CRUDE_SEMANTIC_GOPLS, a prebuilt
// viewmodel test binary and CRUDE_ARGS=-test.run=^TestOccurrencesVisualAcceptance$.
// The caret rests on a parameter; F4 waits for the installed server's answer and
// the painted fill, then writes OCCURRENCE_READY. F9 exits.
func TestOccurrencesVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_OCCURRENCES") != "1" {
		t.Skip("dedicated occurrence capture")
	}
	runOccurrencesVisual(t, false, 0, 0)
}

func TestOccurrencesVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) { runOccurrencesVisual(t, true, size[0], size[1]) })
	}
}

type occurrenceVisualModel struct {
	*featureVisualModel
	declaration, use int
	busy, done       bool
}
type occurrenceVisualDone struct{ err error }

func runOccurrencesVisual(t *testing.T, headless bool, width, height int) {
	f := newInstalledSemanticFixture(t)
	source := installedSemanticSource
	line := strings.Index(source, "func")
	m := &occurrenceVisualModel{declaration: strings.Index(source, "name string") - line, use: strings.LastIndex(source, "name") - line}
	ctx, cancel := context.WithCancel(t.Context())
	m.featureVisualModel = &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: f.shell.app, ctx: ctx, path: f.path},
		settled:            make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_OCCURRENCE_VISUAL_RESULT"),
	}
	f.shell.app.setFocusEditor()
	f.shell.app.Input.SetInput("private occurrence draft")
	f.editor.SetCursorPosition(2, m.declaration+1)
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
			case msg := <-f.shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	})
	if headless {
		workers.Go(func() {
			program.Send(tea.KeyPressMsg{Code: tea.KeyF4})
			select {
			case <-m.settled:
				program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
			case <-ctx.Done():
			}
		})
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if !m.done {
		t.Fatal("occurrence capture ended before its check")
	}
}

func (m *occurrenceVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case occurrenceVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = v.err
			return m, tea.Quit
		}
		m.done = true
		select {
		case m.settled <- struct{}{}:
		default:
		}
		return m, func() tea.Msg {
			if err := m.evidence("OCCURRENCE_READY"); err != nil {
				return occurrenceVisualDone{err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && v.Mod == 0 {
			if m.busy {
				return m, nil
			}
			return m, tea.Quit
		}
		if v.Code == tea.KeyF4 && v.Mod == 0 {
			if m.busy || m.done {
				return m, nil
			}
			m.busy = true
			return m, func() tea.Msg { return occurrenceVisualDone{m.observe()} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

// observe waits for the installed server's occurrences to paint at the use of
// the parameter while the source, caret and draft stay unchanged.
func (m *occurrenceVisualModel) observe() error {
	var frame string
	err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil || ed.Value() != installedSemanticSource || ed.Modified() || ed.HasSelection() || v.Input.Value() != "private occurrence draft" {
			return fmt.Errorf("occurrences changed source, selection or draft")
		}
		if ed.CursorLine() != 2 || ed.CursorCol() != m.declaration+1 {
			return fmt.Errorf("occurrences moved the caret")
		}
		frame = ansi.Strip(v.View().RenderContent())
		if !ed.OccurrencesCoverCaret() {
			return fmt.Errorf("no occurrences yet")
		}
		r := v.editorRect
		x := r.X + ed.PadL + ed.GutterWidth()
		tokens := ui.NewTokenSet(v.Theme)
		buf := v.mainSurf.Buffer()
		// The declaration is visible at every size; the use only when the
		// editor is wide enough to show the whole line.
		columns := []int{m.declaration}
		if x+m.use < r.X+r.W-1 {
			columns = append(columns, m.use)
		}
		for _, col := range columns {
			at, plain := buf.Get(x+col, r.Y+2), buf.Get(x+col-2, r.Y+2)
			if at.Char != 'n' || (!at.BG.Equal(tokens.OccurrenceFill(false)) && !at.BG.Equal(tokens.OccurrenceFill(true))) || plain.BG.Equal(at.BG) {
				return fmt.Errorf("occurrence at column %d is not painted", col)
			}
		}
		if v.width >= 120 && len(columns) != 2 {
			return fmt.Errorf("the desktop frame did not show both occurrences")
		}
		return nil
	})
	if err != nil {
		_ = m.evidence(fmt.Sprintf("OCCURRENCE_FAILED %v\n%s", err, frame))
		return err
	}
	return m.evidence(fmt.Sprintf("OCCURRENCE_VERIFIED %dx%d\n%s", m.width, m.height, frame))
}
