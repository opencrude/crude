package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/workspace"
)

// Dedicated scripts/tuishot.sh fixture: F2 short prose, F3 tiny tall list,
// F4 long typed/restored draft, F5 substantial eight-line paste, F6 attachment,
// F7 theme, F9 quit. No real provider calls or saved user data.
func TestComposerSizingVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPOSER_SIZE") != "1" {
		t.Skip("requires the dedicated terminal capture workflow")
	}
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.AddOrchestrator("draft-owner")
	app.ActiveSession = workspace.NewSession("draft-owner", t.TempDir(), nil, stubLogger{})
	app.maybeHydrateComposerDraft()
	app.AgentMgr.Get("draft-owner").State.Messages.AppendMessage(workspace.Message{
		Role:    workspace.MessageRoleAssistant,
		Content: "The conversation stays readable while the composer grows a little, then scrolls internally. Small notes and lists stay inline; a substantial diagnostic becomes an attachment.",
	})
	app.syncAgentMessages("draft-owner", false)
	model := &composerSizingVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type composerSizingVisualModel struct {
	*App
	light bool
}

func (m *composerSizingVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *composerSizingVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.Input.ClearInput()
			_, command := m.App.Update(tea.PasteMsg{Content: "A short note stays here.\nPlease check the context.\nThanks."})
			return m, command
		case tea.KeyF3:
			m.Input.ClearInput()
			_, command := m.App.Update(tea.PasteMsg{Content: strings.Repeat("- small item\n", 24)})
			return m, command
		case tea.KeyF4:
			m.Input.SetInput(strings.Repeat("This is a complete draft line, kept editable inside a compact viewport.\n", 59) + "FINAL DRAFT LINE")
			return m, nil
		case tea.KeyF5:
			m.Input.ClearInput()
			_, command := m.App.Update(tea.PasteMsg{Content: strings.Repeat("A substantial diagnostic line with complete context for inspection.\n", 8)})
			return m, command
		case tea.KeyF6:
			for _, ref := range m.RefStore().ListReferences() {
				return m, m.openAttachment(ref.ID, false)
			}
			return m, nil
		case tea.KeyF7:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(msg)
	return m, command
}
