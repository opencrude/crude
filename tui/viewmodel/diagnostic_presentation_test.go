package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
	"crude/workspace"
)

func newDiagnosticPresentationShell(t *testing.T) (remoteJourneyShell, *diagnosticFixtureService) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("scsc"), 0600); err != nil {
		t.Fatal(err)
	}
	core := remoteJourneyRuntime(t, "http://127.0.0.1:9", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.Enabled, settings.LSP.AutoDetect = false, false
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, core, root)
	app := shell.app
	ed := liveTestOpen(t, app, path)
	ed.SetCursorPosition(0, 0)
	app.setFocusEditor()
	service := &diagnosticFixtureService{
		LSPService: app.ws.LSPMgr(), facts: make(map[string]types.InsightDiagnostics),
		generation: ed.DocumentSnapshot().AnalysisGeneration,
	}
	previous := app.ws
	app.ws = &diagnosticFixtureWorkspace{Workspace: previous, service: service, root: root}
	t.Cleanup(func() { app.ws = previous })
	return shell, service
}

func diagnosticPresentationApp(t *testing.T, cols, rows int, diags []editor.LSPDiagnostic) (*App, int, int) {
	t.Helper()
	shell, service := newDiagnosticPresentationShell(t)
	app := shell.app
	app.Update(tea.WindowSizeMsg{Width: cols, Height: rows})
	ed := app.EditorState.Editor
	facts := editorDiagnosticFacts(ed, diags)
	facts.ID = "diagnostic-presentation"
	facts.Boundary.Producer.Provider = "pyright-langserver"
	service.facts[ed.FilePath()], service.version = facts, 1
	app.drainLSPDiagnostics()
	app.paintShellFrame()
	rect, mounted := app.screenRectOf(app.editorScreen.ID())
	if !mounted {
		t.Fatal("editor is not mounted")
	}
	x, y := ed.CursorScreenPosition(rect.X, rect.Y)
	return app, x, y
}

func diagnosticPresentationBuffer(app *App, fw *wm.FloatingWindow) *surface.CellBuffer {
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	app.View()
	w, h := fw.Size()
	s := surface.NewSurface("diagnostic-presentation", w, h, app.mainSurf.Buffer().Get(0, 0))
	fw.Content.Render(s, app.renderCtx)
	return s.Buffer()
}

func diagnosticPresentationPaint(app *App, fw *wm.FloatingWindow) string {
	buf := diagnosticPresentationBuffer(app, fw)
	w, h := fw.Size()
	var out strings.Builder
	for y := range h {
		out.WriteString(shellchat.PlainRow(buf, y, w))
		out.WriteByte('\n')
	}
	return out.String()
}

func TestDiagnosticPresentationShortMessagesFitAtEverySeverity(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}, {40, 20}} {
		for _, tc := range []struct {
			severity int
			title    string
			message  string
			icon     rune
			role     ui.SemanticColor
		}{
			{editor.DiagError, "Error", `"scsc" is not defined`, '✕', ui.ColorError},
			{editor.DiagWarning, "Warning", "Expression value is unused", '⚠', ui.ColorWarning},
			{editor.DiagInfo, "Information", "Use a named constant", 'i', ui.ColorAccent2},
			{editor.DiagHint, "Hint", "Remove the unused import", '·', ui.ColorMuted},
		} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], tc.title), func(t *testing.T) {
				app, x, y := diagnosticPresentationApp(t, size[0], size[1], []editor.LSPDiagnostic{{
					Line: 0, Col: 0, EndCol: 4, Severity: tc.severity,
					Message: tc.message, Source: "Pyright", Code: "reportUndefinedVariable",
				}})
				_, cmd := app.Update(tea.MouseMotionMsg{X: x, Y: y})
				runAppCommand(t, app, cmd)
				fw := app.overlayHost.Window(ui.OverlayDiagHover)
				if fw == nil {
					t.Fatal("hover did not open")
				}
				paint := diagnosticPresentationPaint(app, fw)
				if !strings.Contains(paint, tc.title) || !strings.Contains(paint, string(tc.icon)+" "+tc.message) {
					t.Fatalf("first painted card hides severity or problem:\n%s", paint)
				}
				w, h := fw.Size()
				buf := diagnosticPresentationBuffer(app, fw)
				found := false
				ink := ui.NewTokenSet(app.Theme).Color(tc.role)
				for y := range h {
					for x := range w {
						c := buf.Get(x, y)
						found = found || c.Char == tc.icon && c.FG.Equal(ink) && c.Attr&cell.AttrReverse == 0
					}
				}
				if !found {
					t.Fatalf("message has no %s-colored severity icon", tc.title)
				}
				if h > 5 || w > size[0]-4 {
					t.Fatalf("one-line problem has an oversized card: %dx%d", w, h)
				}
				area := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextReader).TextArea()
				if area.Scroll().MaxOffset() != 0 ||
					!strings.Contains(diagnosticVisualLetters(paint), "PyrightreportUndefinedVariable") {
					t.Fatalf("short message and its rule must be readable without scrolling:\n%s", paint)
				}
			})
		}
	}
}

func TestDiagnosticPresentationOverlappingMessagesVisibleTogether(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			app, x, y := diagnosticPresentationApp(t, 80, 24, []editor.LSPDiagnostic{
				{Line: 0, EndCol: 4, Severity: editor.DiagError, Source: "Pyright",
					Code: "reportUndefinedVariable", Message: `"scsc" is not defined`},
				{Line: 0, EndCol: 4, Severity: editor.DiagWarning, Source: "Pyright",
					Code: "reportUnusedExpression", Message: "Expression value is unused"},
			})
			id := ui.OverlayDiagHover
			if explicit {
				id = ui.OverlayDiagContext
				ed := app.EditorState.Editor
				factID, _ := ed.DiagnosticCursor()
				app.showDiagnosticAt(ed, factID, 0, 0)
			} else {
				_, cmd := app.Update(tea.MouseMotionMsg{X: x, Y: y})
				runAppCommand(t, app, cmd)
			}
			fw := app.overlayHost.Window(id)
			if fw == nil {
				t.Fatal("diagnostics did not open")
			}
			paint := diagnosticPresentationPaint(app, fw)
			for _, want := range []string{`✕ Error: "scsc" is not defined`, "⚠ Warning: Expression value is unused"} {
				if !strings.Contains(paint, want) {
					t.Fatalf("first paint must show both problems without scrolling, missing %q:\n%s", want, paint)
				}
			}
			if _, h := fw.Size(); h > 6 {
				t.Fatalf("two short problems use %d rows", h)
			}
			if area := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextReader).TextArea(); area.Scroll().MaxOffset() != 0 {
				t.Fatal("two short problems require scrolling")
			}
		})
	}
}

func TestDiagnosticPresentationGrowsThenScrollsToCompleteMessage(t *testing.T) {
	for _, lines := range []int{12, 40} {
		t.Run(fmt.Sprint(lines), func(t *testing.T) {
			var message strings.Builder
			for i := 1; i < lines; i++ {
				fmt.Fprintf(&message, "Argument %d has an incompatible type.\n", i)
			}
			message.WriteString("Use a string for the final argument.")
			app, x, y := diagnosticPresentationApp(t, 80, 24, []editor.LSPDiagnostic{{
				Line: 0, EndCol: 4, Severity: editor.DiagError, Message: message.String(),
			}})
			_, cmd := app.Update(tea.MouseMotionMsg{X: x, Y: y})
			runAppCommand(t, app, cmd)
			fw := app.overlayHost.Window(ui.OverlayDiagHover)
			if fw == nil {
				t.Fatal("hover did not open")
			}
			paint := diagnosticPresentationPaint(app, fw)
			frame := fw.Content.(*ui.WindowFrameWidget)
			reader := frame.Body().(*ui.TextReader)
			area := reader.TextArea()
			if lines == 12 {
				if _, h := fw.Size(); h < 14 || area.Scroll().MaxOffset() != 0 ||
					!strings.Contains(paint, "Use a string for the final argument.") {
					t.Fatalf("message that fits should grow without scrolling:\n%s", paint)
				}
				return
			}
			bar, ok := frame.Host().ScreenRect(ui.NewID(reader.ID(), "scrollbar"))
			if !ok || bar.W != 1 || area.Scroll().MaxOffset() == 0 {
				t.Fatal("overflow has no usable scrollbar")
			}
			px, py := fw.Position()
			content := fw.ContentBounds()
			bx, by := px+content.X+bar.X, py+content.Y+bar.Y
			for _, msg := range []tea.Msg{
				tea.MouseMotionMsg{X: bx, Y: by},
				tea.MouseClickMsg{X: bx, Y: by, Button: tea.MouseLeft},
				tea.MouseMotionMsg{X: bx, Y: by + bar.H - 1, Button: tea.MouseLeft},
				tea.MouseReleaseMsg{X: bx, Y: by + bar.H - 1, Button: tea.MouseLeft},
			} {
				_, cmd := app.Update(msg)
				runAppCommand(t, app, cmd)
			}
			paint = diagnosticPresentationPaint(app, fw)
			if !strings.Contains(paint, "Use a string for the final argument.") {
				t.Fatalf("dragging the scrollbar did not expose the complete final line:\n%s", paint)
			}
			if area.Value() != "✕ "+message.String() || app.EditorState.Editor.Value() != "scsc" {
				t.Fatal("reading changed the message or source")
			}
		})
	}
}
