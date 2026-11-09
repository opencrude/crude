package viewmodel

import (
	"context"
	"crude/textencoding"
	"os"
	"path/filepath"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/document"
	"crude/tui/ui"
	"crude/types"
)

// The capture driver supplies the double click, typing, Ctrl+S and file name.
// F9 observes the result and exits; it does not create or save the document.
func TestUntitledVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_UNTITLED") != "1" {
		t.Skip("requires a dedicated capture terminal")
	}
	root := t.TempDir()
	existing := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(existing, []byte("Existing file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	name := "notes.txt"
	overwrite := os.Getenv("CRUDE_VISUAL_UNTITLED_OVERWRITE") == "1"
	if overwrite {
		name = "replace.txt"
		if err := os.WriteFile(filepath.Join(root, name), []byte("Old content\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
	liveTestOpen(t, shell.app, existing)
	shell.app.applyEditorSettings(types.EditorSettings{AutoSave: types.AutoSaveAfterDelay, AutoSaveDelay: 250})
	model := &untitledVisualModel{App: shell.app}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	model.SetProgram(program)
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
	data, err := os.ReadFile(filepath.Join(root, name))
	text, encoding, decodeErr := textencoding.DecodeText(data)
	wantEncoding := textencoding.UTF8
	if os.Getenv("CRUDE_VISUAL_UNTITLED_ENCODING") == "1" {
		wantEncoding = textencoding.UTF16LE
	}
	if err != nil || decodeErr != nil || encoding != wantEncoding || string(text) != "new draft" || !model.sawUntitled || !model.sawSave ||
		model.savedPath != filepath.Join(root, name) || overwrite && (!model.sawReview || !model.sawCancel) {
		t.Fatalf("native save journey incomplete: bytes=%q error=%v untitled=%v window=%v review=%v cancel=%v saved=%q",
			data, err, model.sawUntitled, model.sawSave, model.sawReview, model.sawCancel, model.savedPath)
	}
	if os.Getenv("CRUDE_VISUAL_UNTITLED_COMPARE") == "1" && !model.sawComparison {
		t.Fatal("native Compare action did not open the existing comparison tab")
	}
}

type untitledVisualModel struct {
	*App
	sawUntitled, sawSave, sawReview, sawCancel bool
	sawComparison                              bool
	savedPath                                  string
}

func (m *untitledVisualModel) Init() tea.Cmd { return tickCmd() }
func (m *untitledVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	if ed := m.EditorState.Editor; ed != nil && ed.IsOpen() && document.IsUntitledID(ed.DocumentState().DocumentID) {
		if ed.FilePath() == "" {
			m.sawUntitled = true
		} else {
			m.savedPath = ed.FilePath()
		}
	}
	m.sawSave = m.sawSave || m.overlayHost.IsOpen(ui.OverlayFilePicker)
	m.sawComparison = m.sawComparison || m.activeLocalDiff() != nil
	if m.overlayHost.IsOpen(untitledOverwriteOverlay) {
		m.sawReview = true
	} else if m.sawReview && m.untitledSave != nil && m.untitledSave.editor.FilePath() == "" {
		m.sawCancel = true
	}
	return m, cmd
}
