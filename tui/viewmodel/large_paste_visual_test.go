package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/internal/clipboard"
	"crude/tui/ui"
	"crude/types/setting"
	"crude/workspace"
)

func TestLargePasteVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_LARGE_PASTE") != "1" {
		t.Skip("requires the repository's dedicated terminal capture workflow")
	}
	app := newFullTestApp(t)
	// Let the real trust prompt persist the user's restricted-mode choice.
	root := t.TempDir()
	db, err := workspace.OpenWorkspaceDB(root)
	if err != nil {
		t.Fatal(err)
	}
	w := wsOf(app)
	w.RootPath, w.WorkspaceDB = root, db
	setAttachmentDestination(t, app, setting.AttachmentOpenCrude)
	wsOf(app).Logger = stubLogger{}
	app.AddOrchestrator("paste-owner")
	app.ActiveSession = workspace.NewSession("paste-owner", t.TempDir(), nil, stubLogger{})
	app.maybeHydrateComposerDraft()
	app.Input.SetInput("Please investigate the attached diagnostic.")
	content := strings.Repeat("A complete diagnostic line retained in the attachment, not the prompt body.\n", 900) + "EXACT-DIAGNOSTIC-TAIL"
	restore := installAsyncClipboardBackendForTest(app, clipboardAsyncBackend{
		read: func(format clipboard.Format) []byte {
			if format == clipboard.FmtText {
				return []byte(content)
			}
			return nil
		},
		writeText: func(string) error { return nil },
	})
	defer restore()
	model := &largePasteVisualModel{App: app, content: content, t: t}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	state := asyncIOStateFor(app)
	state.mu.Lock()
	state.detachedMessageSink = program.Send
	state.mu.Unlock()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if refs := app.RefStore().ListReferences(); len(refs) != 1 || app.EditorState.Editor == nil || app.EditorState.Editor.FilePath() != refs[0].FilePath || app.EditorState.Editor.Value() != content || app.overlayHost.IsOpen(ui.OverlayTextOverlay) {
		t.Fatal("visual journey did not retain exactly one pasted attachment")
	}
}

type largePasteVisualModel struct {
	t     *testing.T
	ready bool
	*App
	content string
}

func (m *largePasteVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *largePasteVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			_, command := m.App.Update(tea.PasteMsg{Content: m.content})
			return m, command
		case tea.KeyF3:
			_, command := m.App.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
			return m, command
		case tea.KeyF4:
			for _, ref := range m.RefStore().ListReferences() {
				return m, m.openAttachment(ref.ID, false)
			}
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(msg)
	markCaptureReady(m.t, m.App, &m.ready)
	return m, command
}
