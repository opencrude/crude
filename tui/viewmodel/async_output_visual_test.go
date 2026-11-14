package viewmodel

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// scripts/tuishot.sh supplies pointer/key input to the real App. Only F2/F3
// control the fixture process's output; opening, scrolling and closing use
// ordinary product controls.
func TestAsyncOutputWindowVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_ASYNC_OUTPUT") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	shell, process, agentID := commandOutputJourney(t)
	app := shell.app
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	app.ApplyThemePreset("dark")
	model := &asyncOutputWindowVisualModel{App: app, process: process, agentID: agentID}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
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
	if !model.sawOutput || !model.sawReveal || len(app.commandOutputs) != 0 {
		t.Fatal("native command journey did not open, reveal and close the output")
	}
}

type asyncOutputWindowVisualModel struct {
	*App
	process              net.Conn
	agentID              string
	sawOutput, sawReveal bool
}

func (*asyncOutputWindowVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *asyncOutputWindowVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2, tea.KeyF3:
			instruction := "history"
			if key.Code == tea.KeyF3 {
				instruction = "live"
			}
			return m, func() tea.Msg { _, _ = fmt.Fprintln(m.process, instruction); return nil }
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	m.sawOutput = m.sawOutput || len(m.commandOutputs) != 0
	projection := m.subPaneProjections.GetExisting(m.agentID)
	for _, msg := range m.ws.AgentMgr().Get(m.agentID).State.Messages.Messages() {
		if msg.ToolName == "async_execute_command" && projection != nil && projection.renderer.DisclosureExpanded(msg.InternalID) {
			m.sawReveal = true
		}
	}
	return m, cmd
}
