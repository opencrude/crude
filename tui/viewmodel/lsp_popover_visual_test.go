package viewmodel

import (
	"context"
	"os"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
)

// Native capture: F2, type a name, click the source to cancel; Alt+Enter,
// click the source to dismiss; F9 exits. The connected core and editor are
// real; the language server supplies fixed actions, not an acceptance oracle.
func TestEditorPopoverVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_EDITOR_POPOVERS") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	fixture, _ := newActionConnectedFixture(t, actionConnectedRaw)
	shell := fixture.shells[0]
	model := &editorPopoverVisualModel{App: shell.app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	shell.app.SetProgram(program)
	ctx, cancel := context.WithCancel(t.Context())
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); worker.Wait() }()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.renameSeen || !model.actionsSeen || shell.app.TextPrompt != nil || shell.app.codeActions != nil {
		t.Fatal("native input did not open and dismiss both editor popovers")
	}
	fixture.assertBytes(t, renameConnectedMain, renameConnectedPeer)
}

type editorPopoverVisualModel struct {
	*App
	renameSeen, actionsSeen bool
}

func (m *editorPopoverVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *editorPopoverVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	m.renameSeen = m.renameSeen || m.overlayHost.IsOpen(ui.OverlayTextPrompt)
	m.actionsSeen = m.actionsSeen || m.overlayHost.IsOpen(codeActionsOverlay)
	return m, cmd
}
