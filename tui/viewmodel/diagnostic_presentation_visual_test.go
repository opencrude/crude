package viewmodel

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
	"crude/tui/ui"
)

// Native scripts/tuishot.ps1 fixture. F2/F3/F4 publish short, expanded and
// overflowing messages. The capture driver uses ordinary pointer/key input
// to open and read them. F9 quits. This checks presentation, not server quality.
func TestDiagnosticPresentationVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_DIAGNOSTIC_PRESENTATION") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	shell, service := newDiagnosticPresentationShell(t)
	app := shell.app
	ed := app.EditorState.Editor
	model := &diagnosticPresentationVisualModel{App: app, service: service}
	model.publish(tea.KeyF2)
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
	if !model.sawHover || ed.Value() != "scsc" {
		t.Fatalf("native reading did not preserve the source: opened=%t text=%q", model.sawHover, ed.Value())
	}
}

type diagnosticPresentationVisualModel struct {
	*App
	service  *diagnosticFixtureService
	sawHover bool
}

func (m *diagnosticPresentationVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *diagnosticPresentationVisualModel) publish(key rune) {
	message := `"scsc" is not defined`
	diags := []editor.LSPDiagnostic{{Line: 0, EndCol: 4, Severity: editor.DiagError, Source: "Pyright", Code: "reportUndefinedVariable"}}
	switch key {
	case tea.KeyF2:
		diags = append(diags, editor.LSPDiagnostic{Line: 0, EndCol: 4, Severity: editor.DiagWarning,
			Message: "Expression value is unused", Source: "Pyright", Code: "reportUnusedExpression"})
	case tea.KeyF3:
		diags[0].Code = "reportArgumentType"
		message = "Cannot assign this value to the parameter.\n\n" +
			"The parameter requires a string, but this value is a number.\n" +
			"Convert the value to a string before passing it to this function.\n\n" +
			"Related declaration:\n    def consume(value: str) -> None"
	case tea.KeyF4:
		diags[0].Code = "reportArgumentType"
		message = strings.Repeat("The argument does not match the required parameter type.\n", 30) +
			"Use a string for the final argument."
	}
	diags[0].Message = message
	m.clearDiagHoverPopup()
	m.closeDiagnosticDetail()
	m.service.version++
	facts := editorDiagnosticFacts(m.EditorState.Editor, diags)
	facts.ID, facts.Sequence = fmt.Sprintf("visual-diagnostics-%d", m.service.version), m.service.version
	m.service.facts[m.EditorState.Editor.FilePath()] = facts
	m.drainLSPDiagnostics()
}

func (m *diagnosticPresentationVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2, tea.KeyF3, tea.KeyF4:
			m.publish(key.Code)
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	m.sawHover = m.sawHover || m.overlayHost.IsOpen(ui.OverlayDiagHover)
	return m, cmd
}
