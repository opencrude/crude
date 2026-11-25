package viewmodel

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Run with scripts/tuishot.ps1. Send two identical emoji as separate Unicode
// key gestures, a combining/variation/ZWJ sequence, then an ASCII key and F9.
func TestWindowsComposerEmojiVisual(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_EMOJI") != "1" {
		t.Skip("requires the dedicated Windows Terminal capture")
	}
	// The capture launcher sets TERM. Native Windows console sessions can
	// leave it unset, which selects the renderer's basic VT capabilities.
	t.Setenv("TERM", "")
	app, _ := newRealShellApp(t)
	app.Tree.Visible = false
	app.AddOrchestrator("emoji-input")
	app.setFocusInput()
	app.Input.SetInput("prefix ")
	model := &windowsComposerEmojiModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
	const want = "prefix 😂😂e\u0301☀️👩‍💻x"
	if got := app.Input.Value(); got != want {
		t.Fatalf("native composer draft %q; want %q", got, want)
	}
}

type windowsComposerEmojiModel struct{ *App }

func (*windowsComposerEmojiModel) Init() tea.Cmd { return tickCmd() }

func (m *windowsComposerEmojiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
