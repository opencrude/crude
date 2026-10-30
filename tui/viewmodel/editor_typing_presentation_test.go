package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
	"crude/tui/surface"
	"crude/types"
	"crude/workspace"
)

func TestConnectedEditorTypingKeepsUnchangedPresentation(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			server := &immediateTreeDiagRuntime{diags: []workspace.LSPDiagnostic{{Line: 4, Col: 3, EndLine: 4, EndCol: 12, Severity: editor.DiagError, Message: "retained diagnostic"}}}
			runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
				w.LSPMgr.SetRuntime(server)
				settings := w.AppDefaults()
				settings.LSP = types.DefaultLSPSettings()
				// This presentation journey checks the unchanged sidebar, not
				// source-current tree counters (covered by the three-shell test).
				settings.LSP.DiagInTree = false
				w.SetAppDefaults(settings)
				grantSemanticFixture(t, w)
			})
			root := t.TempDir()
			path := filepath.Join(root, "typing.go")
			source := "package main\nvar value = 1\n" + strings.Repeat("// unchanged 界 documentation\n", 2000)
			if err := os.WriteFile(path, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			one := newRemoteJourneyShell(t, runtime, root)
			app := one.app
			ed := liveTestOpen(t, app, path)
			// The baseline must have actually received and painted native shared
			// syntax. A local SyntaxCommand no longer prepares canonical source;
			// starting from a plain pending frame would mistake its first real
			// shared publication for typing-induced flicker.
			remoteJourneyUntil(t, []remoteJourneyShell{one}, func() bool {
				state, _ := ed.InsightStatus()
				return state == types.InsightCurrent && len(ed.Diagnostics()) == 1
			})
			runAppCommand(t, app, app.drainEditorSyntaxCommands())
			_, cmd := app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			runAppCommand(t, app, cmd)
			ed.SetCursorPosition(1, len("var value"))
			app.forceFrame()
			first := app.View()
			before := app.mainSurf.Buffer().Clone()
			frames := []tea.View{first}
			for _, r := range "xyz" {
				start := time.Now()
				_, cmd := app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
				frames = append(frames, app.View())
				assertTypingCellsStable(t, app, before, ed)
				t.Logf("key-to-view %c: %s", r, time.Since(start))
				// Complete the real edit acknowledgement and syntax command only
				// after exercising the intermediate frame a user can actually see.
				runAppCommand(t, app, cmd)
				frames = append(frames, app.View())
				assertTypingCellsStable(t, app, before, ed)
			}
			if !strings.Contains(ed.Value(), "var valuexyz = 1") {
				t.Fatalf("typing lost source: %.60q", ed.Value())
			}
			assertTerminalTypingOutput(t, size, frames)
		})
	}
}

func assertTypingCellsStable(t *testing.T, app *App, before *surface.CellBuffer, ed *editor.Editor) {
	t.Helper()
	r := app.editorRect
	after := app.mainSurf.Buffer()
	for y := r.Y; y < r.Y+r.H-2; y++ {
		if y == r.Y+1 {
			continue
		} // exact edited source line
		for x := r.X + ed.GutterWidth(); x < r.X+r.W-2; x++ {
			a, b := before.Get(x, y), after.Get(x, y)
			if !a.Equal(b) {
				t.Fatalf("typing changed unrelated source cell %d,%d: %+v -> %+v", x, y, a, b)
			}
		}
	}
	// The source edit must never blank or restyle the file tree/background.
	for y := 3; y < app.height-2; y++ {
		for x := 1; x < r.X-1; x++ {
			if a, b := before.Get(x, y), after.Get(x, y); !a.Equal(b) {
				t.Fatalf("typing repainted unrelated sidebar cell %d,%d", x, y)
			}
		}
	}
}

type typingFrameMsg struct{ index int }
type typingFrameReplay struct {
	frames []tea.View
	index  int
}

func (m *typingFrameReplay) Init() tea.Cmd { return nil }
func (m *typingFrameReplay) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if frame, ok := msg.(typingFrameMsg); ok {
		m.index = frame.index
	}
	return m, nil
}
func (m *typingFrameReplay) View() tea.View {
	v := m.frames[m.index]
	// Exact frame marker in the same physical write as its cell delta.
	v.WindowTitle = fmt.Sprintf("typing-frame-%d", m.index)
	return v
}

type typingOutput struct {
	mu     sync.Mutex
	chunks chan string
}

func (o *typingOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.chunks <- string(p)
	return len(p), nil
}
func assertTerminalTypingOutput(t *testing.T, size [2]int, frames []tea.View) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out := &typingOutput{chunks: make(chan string, 128)}
	model := &typingFrameReplay{frames: frames}
	p := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(out), tea.WithWindowSize(size[0], size[1]), tea.WithEnvironment([]string{"TERM=xterm-256color"}), tea.WithWidthMethod(ansi.GraphemeWidth), tea.WithoutSignals())
	done := make(chan error, 1)
	go func() { _, err := p.Run(); done <- err }()
	defer func() {
		p.Quit()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for i := range frames {
		if i > 0 {
			p.Send(typingFrameMsg{index: i})
		}
		var output strings.Builder
		for !strings.Contains(output.String(), fmt.Sprintf("typing-frame-%d", i)) {
			select {
			case chunk := <-out.chunks:
				output.WriteString(chunk)
			case <-ctx.Done():
				t.Fatal("terminal did not present frame", i)
			}
		}
		if i == 0 {
			continue
		}
		text := output.String()
		if strings.Contains(text, "\x1b[2J") || strings.Contains(text, "\x1b[3J") || strings.Contains(text, "\x1b[?1049") {
			t.Fatalf("typing frame %d cleared/reset terminal", i)
		}
		// A character and its small chrome changes must not retransmit the
		// whole source pane. This observes the real Bubble Tea/UV output, not
		// the full-frame string deliberately supplied to its diff renderer.
		if glyphs := len([]rune(ansi.Strip(text))); glyphs > 200 {
			t.Fatalf("typing frame %d rewrote %d terminal glyphs", i, glyphs)
		}
		t.Logf("terminal frame %d: %d bytes", i, len(text))
	}
}
