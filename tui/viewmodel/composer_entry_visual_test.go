package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/agent/session"
	"crude/agent/sessiondb"
	"crude/core/shellport"
	"crude/types/setting"
	"crude/workspace"
)

// Run only through scripts/tuishot with this isolated test binary. The draft
// puts an attachment chip where it does not fit at the end of a composer row;
// keys and paint are production. F9 checks that the chip is still one bound
// range and exits.
func TestComposerChipVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPOSER_CHIPS") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	app, ws := newRealShellApp(t)
	defer ws.Close()
	app.Tree.Visible = false
	sess := workspace.NewSession("Alpha", t.TempDir(), nil, nil)
	app.ws.SessionsByAgent()["Alpha"] = sess
	app.AddOrchestrator("Alpha")
	ref, err := sess.RefStore.CreateReference(sessiondb.CreateRequest{Kind: sessiondb.KindPastedText, Content: strings.Repeat("pasted line\n", 20)})
	if err != nil {
		t.Fatal(err)
	}
	model := &composerChipVisualModel{App: app, t: t, ref: ref}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type composerChipVisualModel struct {
	*App
	t      *testing.T
	ref    sessiondb.Reference
	seeded bool
}

func (*composerChipVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *composerChipVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		if ids := m.Input.ReferencedIDs(); len(ids) != 1 || ids[0] != m.ref.ID {
			m.t.Errorf("chip binding after edits: %v", ids)
		}
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	if _, ok := msg.(tea.WindowSizeMsg); ok && !m.seeded {
		// Fill the first row so the chip starts a few cells before its end:
		// the chip must move to the next row whole, not split.
		m.seeded = true
		label := m.ref.DisplayName()
		width := max(20, m.inputRect.W-4)
		prose := strings.Repeat("word ", width/5)
		prose = prose[:max(0, width-len(label)/2)]
		m.Input.SetInput(prose)
		m.Input.AttachReference(m.ref)
		// The caret waits before the chip, where Space pushes it forward.
		if ranges := m.Input.RefRanges(); len(ranges) == 1 {
			m.Input.SetCursorFlat(ranges[0].Start)
		}
	}
	return m, cmd
}

// Run only through scripts/tuishot with this isolated test binary: the first
// AI setup reminder at the real terminal size. F9 exits.
func TestAISetupReminderVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_AI_SETUP") != "1" {
		t.Skip("dedicated sequential terminal capture")
	}
	// A real shell on a profile with no saved account, as on first use.
	dir, home := t.TempDir(), t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	ws := workspace.NewWorkspace(dir, "", "test", setting.Settings{}, regressionLogger{})
	ws.SetBackendConfig(&session.Config{Agents: session.DefaultAgentsConfig()}, home)
	ws.SetAppDefaults(setting.App{Accounts: ws.AllAccounts()})
	ws.SetEventSender(noopEventSender{})
	app := NewApp(shellport.Local(ws), nil)
	t.Cleanup(func() {
		app.StopAsyncIO()
		ws.Close()
	})
	ws.LSPMgr.SetRuntime(noopLSPRuntime{})
	loadOnboarding(t, app)
	model := &aiSetupVisualModel{App: app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type aiSetupVisualModel struct{ *App }

func (*aiSetupVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *aiSetupVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		m.maybeShowAISetupReminder()
	}
	return m, cmd
}
