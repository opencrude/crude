package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Internal candidate injection, NOT automatic prediction/provider acceptance.
// Native capture: CRUDE_VISUAL_GHOST=1, -test.run=^TestEditorGhostVisualAcceptance$.
// F2 supplies the first candidate; F3 supplies the dismissal candidate. F4 only
// observes settled source/paint. All edits, acceptance, history and dismissal use
// ordinary terminal keys. F9 verifies the observations and exits without saving.
func TestEditorGhostVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_GHOST") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	runEditorGhostVisual(t, false, 0, 0)
}

func TestEditorGhostVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runEditorGhostVisual(t, true, size[0], size[1])
		})
	}
}

func TestGhostVisualObservationWaitsForAppliedDocumentTopic(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		restrictFixture(t, w)
	})
	root := t.TempDir()
	path := filepath.Join(root, "ghost-topic.txt")
	if err := os.WriteFile(path, []byte("consume()"), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	ed.SetCursorPosition(0, 8)
	shell.app.setFocusEditor()
	shell.app.Input.SetInput("private ghost visual draft")
	if err := ed.SetGhostText(ed.Revision(), 8, "value + next"); err != nil {
		t.Fatal(err)
	}
	_, cmd := shell.app.Update(ghostVisualProbe{})
	runAppCommand(t, shell.app, cmd)
	_, cmd = shell.app.Update(tea.KeyPressMsg{Code: 'v', Text: "val"})
	receipt, topic := connectedGhostHold(t, shell, ed, cmd)

	m := &ghostVisualModel{App: shell.app, ed: ed, phase: 1, settled: make(chan int, 1)}
	_, cmd = m.Update(receipt)
	runAppCommand(t, shell.app, cmd)
	state := shell.app.liveEditors[ed]
	if _, current := ed.CurrentInsightSource(); !current || !state.sync {
		t.Fatalf("fixture did not hold the receipt-before-publication boundary: current=%v live=%+v", current, state)
	}
	// The canonical receipt is not the applied topic: F4 must retain its check.
	m.Update(tea.KeyPressMsg{Code: tea.KeyF4})
	if !m.checkPending || m.phase != 1 || m.failure != nil {
		t.Fatalf("observation ran before the held topic was applied: phase=%d pending=%v failure=%v", m.phase, m.checkPending, m.failure)
	}
	_, cmd = m.Update(topic)
	runAppCommand(t, shell.app, cmd)
	if m.checkPending || m.phase != 2 || m.failure != nil {
		t.Fatalf("observation did not finish on the actual topic: phase=%d pending=%v failure=%v", m.phase, m.checkPending, m.failure)
	}
}

type ghostVisualModel struct {
	*App
	ed           *editor.Editor
	phase        int
	checkPending bool
	checkUntil   time.Time
	failure      error
	observations []string
	settled      chan int
}

type ghostVisualProbe struct{}

var ghostVisualExpected = []struct {
	source, painted, rest string
	anchor                int
}{
	{"consume()", "consume(value + next)", "value + next", 8},
	{"consume(val)", "consume(value + next)", "ue + next", 11},
	{"consume(value)", "consume(value + next)", " + next", 13},
	{"consume(value + next)", "consume(value + next)", "", 0},
	{"consume(value)", "consume(value)", "", 0},
	{"consume(value + next)", "consume(value + next)", "", 0},
	{"consume(value + next)", "consume(value + next + tail)", " + tail", 20},
	{"consume(value + next)", "consume(value + next)", "", 0},
	{"consume(value + next    )", "consume(value + next    )", "", 0},
	{"consume(value + next)", "consume(value + next)", "", 0},
}

func (m *ghostVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var command tea.Cmd
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2, tea.KeyF3:
			phase, text := 0, "value + next"
			if key.Code == tea.KeyF3 {
				phase, text = 6, " + tail"
			}
			if m.phase != phase || m.ed.Value() != ghostVisualExpected[phase].source {
				m.failure = fmt.Errorf("fixture candidate offered outside its exact source/phase: %d", m.phase)
				return m, tea.Quit
			}
			start, end, valid := m.ed.SourceByteRange()
			if !valid || start != end || start != ghostVisualExpected[phase].anchor {
				m.failure = fmt.Errorf("fixture candidate did not retain the native source caret: %d:%d", start, end)
				return m, tea.Quit
			}
			if err := m.ed.SetGhostText(m.ed.Revision(), start, text); err != nil {
				m.failure = err
				return m, tea.Quit
			}
			// Only fixture supply is intercepted. Adoption still runs on Update.
			_, command = m.App.Update(ghostVisualProbe{})
			return m, command
		case tea.KeyF4:
			m.checkPending = true
			m.checkUntil = time.Now().Add(8 * time.Second)
		case tea.KeyF9:
			if m.phase != len(ghostVisualExpected) || m.failure != nil {
				m.failure = fmt.Errorf("native ghost journey incomplete: %d/%d: %v", m.phase, len(ghostVisualExpected), m.failure)
			}
			return m, tea.Quit
		default:
			_, command = m.App.Update(msg)
		}
	} else {
		_, command = m.App.Update(msg)
	}
	if m.checkPending {
		// Observation waits for actual ordered document installation. It neither
		// fabricates an ACK nor repairs source, caret or candidate state.
		state := m.liveEditors[m.ed]
		_, current := m.ed.CurrentInsightSource()
		if state != nil && current && state.topicReady && !state.busy && !state.sync {
			if err := m.observe(); err != nil {
				m.failure = err
				return m, tea.Quit
			}
			m.checkPending = false
		} else if time.Now().After(m.checkUntil) {
			m.failure = fmt.Errorf("phase %d did not settle its actual canonical document: %+v", m.phase, m.ed.DocumentState())
			return m, tea.Quit
		} else {
			command = tea.Batch(command, tea.Tick(10*time.Millisecond, func(time.Time) tea.Msg { return ghostVisualProbe{} }))
		}
	}
	return m, command
}

func (m *ghostVisualModel) observe() error {
	if m.phase >= len(ghostVisualExpected) {
		return fmt.Errorf("unexpected extra ghost observation")
	}
	want := ghostVisualExpected[m.phase]
	anchor, rest, offered := m.ed.GhostText()
	popupID := ""
	if popup := m.focusedPopup(); popup != nil {
		popupID = popup.ID
	}
	if !m.ed.IsOpen() || m.EditorState.Editor != m.ed || m.ed.Value() != want.source ||
		rest != want.rest || offered != (want.rest != "") || offered && anchor != want.anchor {
		return fmt.Errorf("phase %d: source=%q ghost=(%d,%q,%t), want source=%q ghost=(%d,%q), popup=%q",
			m.phase, m.ed.Value(), anchor, rest, offered, want.source, want.anchor, want.rest, popupID)
	}
	if m.Input.Value() != "private ghost visual draft" || m.focusTarget() != FocusEditor || popupID != "" {
		return fmt.Errorf("phase %d consumed private input or changed native editor focus: draft=%q focus=%v popup=%q",
			m.phase, m.Input.Value(), m.focusTarget(), popupID)
	}
	if paint := ansi.Strip(m.App.View().RenderContent()); !strings.Contains(paint, want.painted) {
		return fmt.Errorf("phase %d: literal source/candidate was not painted: %q", m.phase, want.painted)
	}
	if offered {
		buffer := m.mainSurf.Buffer()
		found := false
		for y := 0; y < buffer.Height(); y++ {
			for x := 0; x+len(want.painted) <= buffer.Width(); x++ {
				matches := true
				for i, r := range want.painted {
					matches = matches && buffer.Get(x+i, y).Char == r
				}
				if !matches {
					continue
				}
				for i, r := range want.rest {
					c := buffer.Get(x+want.anchor+i, y)
					if c.Char != r || c.FG != m.rootHost.Tokens().Color(ui.ColorMuted) {
						return fmt.Errorf("phase %d: mounted candidate is not literal muted ink", m.phase)
					}
				}
				found = true
			}
		}
		if !found {
			return fmt.Errorf("phase %d: no mounted source row", m.phase)
		}
	}
	m.observations = append(m.observations, fmt.Sprintf("F4 VERIFIED phase=%d source=%q ghost=%q\n", m.phase, want.source, want.rest))
	m.phase++
	m.settled <- m.phase
	return nil
}

func runEditorGhostVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	// This journey exercises editor input, not the first-use Trust decision. Make
	// the same durable restricted choice as a user before attaching the shell.
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		restrictFixture(t, w)
	})
	root := t.TempDir()
	path := filepath.Join(root, "prediction-projection.txt")
	const original = "consume()"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	if shell.app.ws.LoadAppSettings().Analysis.DecisionNeeded() {
		t.Fatal("ghost journey attached before its restricted Trust decision")
	}
	ed := liveTestOpen(t, shell.app, path)
	ed.SetCursorPosition(0, 8)
	shell.app.setFocusEditor()
	shell.app.Input.SetInput("private ghost visual draft")
	m := &ghostVisualModel{App: shell.app, ed: ed, settled: make(chan int, len(ghostVisualExpected))}
	options := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		options = append(options, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, options...)
	m.SetProgram(program)
	pumpCtx, cancelPump := context.WithCancel(t.Context())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	finished := make(chan struct{})
	if headless {
		go func() {
			groups := [][]tea.KeyPressMsg{
				{{Code: tea.KeyF2}},
				{{Code: 'v', Text: "val"}},
				{{Code: tea.KeyRight, Mod: tea.ModCtrl}},
				{{Code: tea.KeyTab}},
				{{Code: 'z', Mod: tea.ModCtrl}},
				{{Code: 'z', Mod: tea.ModCtrl | tea.ModShift}},
				{{Code: tea.KeyF3}},
				{{Code: tea.KeyEsc}},
				{{Code: tea.KeyTab}},
				{{Code: 'z', Mod: tea.ModCtrl}},
			}
			for _, group := range groups {
				for _, key := range group {
					program.Send(key)
				}
				// A real periodic tick may open first-use overlays. Exercise it
				// at every phase rather than depending on scheduler timing.
				program.Send(types.TickMsg(time.Now()))
				program.Send(tea.KeyPressMsg{Code: tea.KeyF4})
				select {
				case <-m.settled:
				case <-finished:
					return
				case <-t.Context().Done():
					return
				}
			}
			program.Send(tea.KeyPressMsg{Code: tea.KeyF9})
		}()
	}
	_, runErr := program.Run()
	close(finished)
	cancelPump()
	<-pumpDone
	m.StopAsyncIO()
	closeErr := m.CloseWorkspaceAttachment()
	if runErr != nil || closeErr != nil || m.failure != nil || m.phase != len(ghostVisualExpected) {
		t.Fatalf("ghost visual journey: run=%v close=%v failure=%v observations=%d", runErr, closeErr, m.failure, m.phase)
	}
	if disk, err := os.ReadFile(path); err != nil || string(disk) != original {
		t.Fatalf("display/acceptance/history implicitly saved source: %q, %v", disk, err)
	}
	if result := os.Getenv("CRUDE_GHOST_VISUAL_RESULT"); !headless && result != "" {
		report := strings.Join(m.observations, "") + "GHOST_VISUAL_PREREQUISITE_SUCCESS; internal supply, real keys and canonical history, no provider/consent acceptance\n"
		if err := os.WriteFile(result, []byte(report), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
