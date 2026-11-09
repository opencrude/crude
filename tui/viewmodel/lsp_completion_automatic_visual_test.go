package viewmodel

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
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

// The driver changes the visible preferences and saves them with ordinary
// input. F6 only opens the existing page; F9 verifies the connected result.
func TestCompletionSettingsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPLETION_SETTINGS") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	shells, _, _, _, _ := newTypingCompletionFixture(t, "pr")
	model := &completionSettingsVisualModel{App: shells[0].app}
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
	settings := model.ws.LSPMgr().Settings()
	body := model.settingsSurface(model.overlayHost.Window(ui.OverlayWorkspaceSettings))
	edits := -1
	if body != nil {
		edits = len(body.Model().Edits())
	}
	passed := edits == 0 && !settings.AutoComplete && settings.CompletionDelay() == 0 &&
		settings.CompletionAcceptance() == "enter" && model.Input.Value() == "private draft"
	result := fmt.Sprintf("COMPLETION_SETTINGS_VISUAL passed=%t auto=%t delay=%d accept=%s edits=%d draftPreserved=%t\n",
		passed, settings.AutoComplete, settings.CompletionDelay(), settings.CompletionAcceptance(), edits,
		model.Input.Value() == "private draft")
	if path := os.Getenv("CRUDE_COMPLETION_SETTINGS_VISUAL_RESULT"); path != "" {
		if err := os.WriteFile(path, []byte(result), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if !passed {
		t.Fatal(result)
	}
}

type completionSettingsVisualModel struct{ *App }

func (m *completionSettingsVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *completionSettingsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF6:
			m.openWorkspaceSettings()
			fw := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
			if body := m.settingsSurface(fw); body != nil {
				for i, category := range body.Model().Categories() {
					if category.Name == "Code Intelligence" {
						body.Model().SelectCategory(i)
						break
					}
				}
				body.SetModel(body.Model())
				if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
					frame.Host().MarkLayout()
				}
				fw.MarkContentDirty()
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
