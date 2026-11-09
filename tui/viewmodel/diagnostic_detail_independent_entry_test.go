package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"fmt"
	"strings"
	"testing"
	"time"
)

func diagnosticIndependentMenu(t *testing.T, cols, rows int, message string) (*App, *wm.FloatingWindow) {
	t.Helper()
	app, _ := signatureTestApp(t, cols)
	ed := app.EditorState.Editor
	snapshot := ed.DocumentSnapshot()
	snapshot.Text, snapshot.Saved = []byte("\t界🙂 problem"), []byte("\t界🙂 problem")
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	ed.SetCursorPosition(0, 10)
	ed.SetDiagnostics(editorDiagnosticFacts(ed, []editor.LSPDiagnostic{{Line: 0, Col: 0, EndLine: 0, EndCol: 12, Severity: editor.DiagError, Message: message}}))
	app.width, app.height = cols, rows
	app.paintShellFrame()
	r, ok := app.screenRectOf(app.editorScreen.ID())
	if !ok {
		t.Fatal("editor not mounted")
	}
	cx, cy := ed.CursorScreenPosition(r.X, r.Y)
	_, cmd := app.dispatchMouseRightClick(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseRight})
	runAppCommand(t, app, cmd)
	menu := app.LastContextMenu
	if menu == nil {
		t.Fatal("native editor right click did not open context menu")
	}
	chosen := -1
	for i := range menu.FlatItems {
		s, j, ok := menu.FlatItemAt(i)
		if ok && menu.Sections[s].Items[j].Label == "Show diagnostics" {
			chosen = i
			break
		}
	}
	if chosen < 0 {
		t.Fatal("context menu lacks the current diagnostic action")
	}
	for n := 0; menu.Cursor != chosen && n < len(menu.FlatItems); n++ {
		_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		runAppCommand(t, app, cmd)
	}
	if menu.Cursor != chosen {
		t.Fatal("native menu could not select diagnostic row")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	fw := app.overlayHost.Window(ui.OverlayDiagContext)
	if fw == nil {
		t.Fatal("native diagnostic action did not open details")
	}
	return app, fw
}

func TestDiagnosticDetailNativeMenuAnchorsAtItsMountedSource(t *testing.T) {
	for _, grid := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(grid), func(t *testing.T) {
			app, fw := diagnosticIndependentMenu(t, grid[0], grid[1], "DIAGNOSTIC_INDEPENDENT_LITERAL: wrong argument type")
			ed := app.EditorState.Editor
			r, _ := app.screenRectOf(app.editorScreen.ID())
			cx, cy := ed.CursorScreenPosition(r.X, r.Y)
			x, y := fw.Position()
			w, h := fw.Size()
			if x > cx || x+w <= cx || (y != cy+1 && y+h != cy) || x < 0 || y < 0 || x+w > grid[0] || y+h > grid[1] {
				t.Fatalf("native diagnostic details centered or covered the actual source instead of anchoring: caret=%d:%d popup=%d:%d %dx%d grid=%v", cx, cy, x, y, w, h, grid)
			}
			if ed.Value() != "\t界🙂 problem" || app.Input.Value() != "keep the private draft" || app.focusedPopup() != fw {
				t.Fatal("diagnostic inspection mutated source/draft or lost explicit reader focus")
			}
		})
	}
}

func TestDiagnosticDetailNativePagingReachesOriginalMessageTail(t *testing.T) {
	message := strings.Repeat("Independent diagnostic content spans many readable lines.\n", 30) + "DIAGNOSTIC_ORIGINAL_LAST_LINE"
	app, fw := diagnosticIndependentMenu(t, 80, 24, message)
	painted := func() string {
		app.advanceReveals(time.Now().Add(2 * revealDuration))
		w, h := fw.Size()
		s := surface.NewSurface("diagnostic-independent", w, h, app.mainSurf.Buffer().Get(0, 0))
		fw.Content.Render(s, app.renderCtx)
		var out strings.Builder
		for y := range h {
			out.WriteString(shellchat.PlainRow(s.Buffer(), y, w))
			out.WriteByte('\n')
		}
		return out.String()
	}
	found := strings.Contains(painted(), "DIAGNOSTIC_ORIGINAL_LAST_LINE")
	for i := 0; i < 12 && !found; i++ {
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
		runAppCommand(t, app, cmd)
		found = strings.Contains(painted(), "DIAGNOSTIC_ORIGINAL_LAST_LINE")
	}
	if !found {
		t.Fatal("native diagnostic reader cannot scroll to the original message tail")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if app.overlayHost.IsOpen(ui.OverlayDiagContext) || !app.EditorState.Editor.IsOpen() || app.EditorState.Editor.Value() != "\t界🙂 problem" {
		t.Fatal("diagnostic Escape changed the source")
	}
}
