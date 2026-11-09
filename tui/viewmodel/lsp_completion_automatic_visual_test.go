package viewmodel

import (
	"context"
	"os"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// scripts/tuishot.ps1: type:i, capture the suggestions, Tab, capture the
// inserted name, F9. The driver supplies real input; the fixture only observes.
func TestAutomaticCompletionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_AUTO_COMPLETION") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	shells, eds, _, _, _ := newTypingCompletionFixture(t, "pr")
	model := &automaticCompletionVisualModel{App: shells[0].app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	model.SetProgram(program)
	ctx, cancel := context.WithCancel(t.Context())
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case msg := <-shells[0].fifo:
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
	if !model.suggestionsSeen || eds[0].Value() != "privateValue" || model.Input.Value() != "private draft" {
		t.Fatal("native typing did not show and accept suggestions without changing the private draft")
	}
}

type automaticCompletionVisualModel struct {
	*App
	suggestionsSeen bool
}

func (m *automaticCompletionVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *automaticCompletionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	m.suggestionsSeen = m.suggestionsSeen || m.completion != nil && m.completion.screen != nil
	return m, cmd
}
