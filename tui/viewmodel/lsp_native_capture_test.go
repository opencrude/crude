package viewmodel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/types"
	"crude/workspace"
)

// Native, opt-in capture driver for scripts/tuishot.ps1. The capture supplies
// ordinary keys; F4 only observes the resulting UI. The provider is installed
// gopls and all work stays in a disposable root and test home.
//
// references: Alt+; F4 End Enter F4 F9
// actions: CodeActionsChord (or editor Code actions menu) F4 Home Enter F4
// Escape F4 F9. CRUDE_CAPTURE_LSP_RESULT retains assertions and frames outside
// the terminal's alternate screen for the capture workflow to verify.
func TestLSPInteractionNativeCapture(t *testing.T) {
	kind := os.Getenv("CRUDE_CAPTURE_LSP")
	if kind != "references" && kind != "actions" {
		t.Skip("native terminal capture only")
	}
	server, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatal("installed gopls is required", err)
	}
	root := t.TempDir()
	before := "package journey\n\nfunc Answer() int { return 42 }\n"
	if kind == "actions" {
		before = "package journey\n\nimport \"fmt\"\n\nvar value = 42\n"
	}
	for name, text := range map[string]string{
		"go.mod":  "module journey\n\ngo 1.22\n",
		"main.go": before,
		"use.go":  "package journey\n\nvar value = Answer()\n",
	} {
		if kind == "actions" && name == "use.go" {
			continue
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoComplete, settings.LSP.AutoDetect = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: server, RootMarkers: []string{"go.mod"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, filepath.Join(root, "main.go"))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		_, current := ed.CurrentInsightSource()
		return current && shell.app.ws.LSPMgr().ServerReady()
	})
	if kind == "actions" {
		ed.SetCursorPosition(2, 8)
	} else {
		ed.SetCursorPosition(2, 6)
	}
	shell.app.setFocusEditor()
	shell.app.Input.SetInput("keep this private draft")
	model := &nativeLSPCapture{App: shell.app, kind: kind}
	if path := os.Getenv("CRUDE_CAPTURE_LSP_RESULT"); path != "" {
		t.Cleanup(func() {
			result := fmt.Sprintf("passed=%t kind=%s phases=%d failure=%v\n%s",
				!t.Failed(), kind, model.phase, model.failure, model.frames.String())
			if err := os.WriteFile(path, []byte(result), 0o600); err != nil {
				t.Error(err)
			}
		})
	}
	program := tea.NewProgram(model)
	ctx, cancel := context.WithCancel(t.Context())
	var pump sync.WaitGroup
	pump.Add(1)
	go func() {
		defer pump.Done()
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	_, runErr := program.Run()
	cancel()
	pump.Wait()
	if runErr != nil || model.failure != nil {
		t.Fatal(runErr, model.failure)
	}
	want := 2
	if kind == "actions" {
		want = 3
	}
	if model.phase != want || shell.app.Input.Value() != "keep this private draft" {
		t.Fatalf("capture did not complete its real interaction: phase=%d want=%d", model.phase, want)
	}
	disk, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || string(disk) != before {
		t.Fatal("navigation or Cancel changed source", err)
	}
	t.Logf("NATIVE_LSP_CAPTURE_SUCCESS kind=%s phases=%d\n%s", kind, model.phase, model.frames.String())
}

type nativeLSPCapture struct {
	*App
	kind    string
	phase   int
	target  types.LSPNavigationTarget
	failure error
	frames  strings.Builder
}

func (m *nativeLSPCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		if key.Code == tea.KeyF9 {
			return m, tea.Quit
		}
		if key.Code == tea.KeyF4 {
			m.failure = m.observe()
			if m.failure != nil {
				return m, tea.Quit
			}
			m.phase++
			return m, nil
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *nativeLSPCapture) observe() error {
	paint := ansi.Strip(m.App.View().RenderContent())
	fmt.Fprintf(&m.frames, "PHASE %d\n%s\n", m.phase, paint)
	if m.phase == 0 {
		if m.kind == "references" {
			s := m.navigation
			if s == nil || !s.shown || s.result == nil || len(s.result.Targets) != 2 || !s.screen.InList() {
				return fmt.Errorf("real references list was not shown")
			}
			m.target = s.result.Targets[len(s.result.Targets)-1]
		} else {
			s := m.codeActions
			if s == nil || len(s.set.Items) == 0 || s.set.Items[0].Kind != "source.organizeImports" || !s.set.Items[0].CanReview {
				return fmt.Errorf("real organize-imports action was not offered first")
			}
		}
		for _, word := range []string{"Failure (literal)", "targetSelectionRange", "Source / provider", "Set digest:"} {
			if strings.Contains(paint, word) {
				return fmt.Errorf("chooser shows debug text %q", word)
			}
		}
		return nil
	}
	if m.kind == "references" {
		ed := m.EditorState.Editor
		line, col := ed.LSPCursorPosition()
		if m.navigation != nil || ed.FilePath() != m.target.Path || line != m.target.Selection.Start.Line ||
			col != m.target.Selection.Start.Character || m.focusTarget() != FocusEditor {
			return fmt.Errorf("choosing the reference did not land directly in the code")
		}
		return nil
	}
	if m.phase == 1 {
		p := m.LSPPopup.EditPreview
		want := fmt.Sprintf("%x", sha256.Sum256([]byte("package journey\n\nvar value = 42\n")))
		if m.codeActions != nil || p == nil || len(p.request.preview.Files) != 1 ||
			p.request.preview.Files[0].AfterSHA256 != want || !strings.Contains(paint, "Apply changes") ||
			!strings.Contains(paint, "- import \"fmt\"") {
			return fmt.Errorf("choosing the action did not show the actual import-removal review")
		}
		for _, word := range []string{"SHA-256", "canonical", "UTF-16", "(literal)", "Action title"} {
			if strings.Contains(paint, word) {
				return fmt.Errorf("change review shows internal text %q", word)
			}
		}
		return nil
	}
	if m.LSPPopup.EditPreview != nil || m.codeActions != nil {
		return fmt.Errorf("Cancel did not close the action review")
	}
	return nil
}
