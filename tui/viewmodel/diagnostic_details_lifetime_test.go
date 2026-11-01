package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
)

func TestDiagnosticDetailExactPublicationLifecycle(t *testing.T) {
	for _, change := range []string{"edit", "equal-text-undo", "caret", "selection", "publication", "stale", "hidden", "reopen", "workspace", "blur", "outside"} {
		t.Run(change, func(t *testing.T) {
			app, fw := diagnosticIndependentMenu(t, 100, 32, "INDEPENDENT_RETIRED_DIAGNOSTIC")
			ed := app.EditorState.Editor
			switch change {
			case "edit":
				ed.InsertRune('x')
			case "equal-text-undo":
				ed.InsertRune('x')
				ed.Undo()
			case "caret":
				ed.SetCursorPosition(0, 0)
			case "selection":
				ed.SetSelection(0, 0, 0, 1)
			case "publication":
				facts := ed.DiagnosticFacts()
				facts.ID = "replacement-publication"
				ed.SetDiagnostics(facts)
			case "stale":
				facts := ed.DiagnosticFacts()
				facts.State = types.InsightStale
				ed.SetDiagnostics(facts)
			case "hidden":
				ed.SetDiagnosticsVisible(false)
			case "reopen":
				snapshot := ed.DocumentSnapshot()
				snapshot.AnalysisLifetime = "another-equal-text-open"
				if err := ed.InstallDocument(snapshot); err != nil {
					t.Fatal(err)
				}
			case "workspace":
				app.workspaceSwitchGen.Add(1)
			case "blur":
				app.Update(tea.BlurMsg{})
			case "outside":
				x, y := fw.Position()
				w, h := fw.Size()
				px, py := x, y+h+1
				if py >= app.height-2 {
					px, py = x+w+1, y
				}
				if px >= app.width {
					px, py = 1, 1
				}
				app.Update(tea.MouseClickMsg{X: px, Y: py, Button: tea.MouseLeft})
			}
			_, cmd := app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
			runAppCommand(t, app, cmd)
			if app.overlayHost.IsOpen(ui.OverlayDiagContext) {
				t.Fatal("diagnostic reader retained a departed exact publication/input owner", change)
			}
		})
	}
}

func TestDiagnosticDetailReadOnlySelectionAndFirstResize(t *testing.T) {
	app, fw := diagnosticIndependentMenu(t, 160, 40, strings.Repeat("Independent complete diagnostic message.\n", 30)+"LAST_ORIGINAL_DIAGNOSTIC")
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		t.Fatal("diagnostic details are not a native WindowFrame reader")
	}
	area, ok := frame.Body().(*ui.TextArea)
	if !ok {
		t.Fatal("diagnostic details are not selectable native text")
	}
	source, draft, text := app.EditorState.Editor.Value(), app.Input.Value(), area.Value()
	app.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	if !area.HasActiveSelection() {
		t.Fatal("diagnostic text did not support ordinary selection")
	}
	app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if area.Value() != text || app.EditorState.Editor.Value() != source || app.Input.Value() != draft {
		t.Fatal("typing into diagnostic reader changed original evidence or source")
	}
	for _, grid := range [][2]int{{80, 24}, {40, 20}, {160, 40}} {
		_, cmd := app.Update(tea.WindowSizeMsg{Width: grid[0], Height: grid[1]})
		runAppCommand(t, app, cmd)
		app.View()
		if app.overlayHost.Window(ui.OverlayDiagContext) != fw {
			t.Fatal("resize replaced/retired same-source diagnostic reader")
		}
		r, mounted := app.screenRectOf(app.editorScreen.ID())
		ed := app.EditorState.Editor
		cx, cy := ed.CursorScreenPosition(r.X, r.Y)
		x, y := fw.Position()
		w, h := fw.Size()
		if !mounted || x > cx || x+w <= cx || (y != cy+1 && y+h != cy) || x < 0 || y < 0 || x+w > grid[0] || y+h > grid[1] {
			t.Fatalf("first diagnostic resize lost its mounted source: %v caret=%d:%d popup=%d:%d %dx%d", grid, cx, cy, x, y, w, h)
		}
		if frame.Body() != area || !strings.Contains(area.Value(), "LAST_ORIGINAL_DIAGNOSTIC") {
			t.Fatal("resize rebuilt or clipped the original reader")
		}
	}
}

func TestDiagnosticHoverIsPassiveLiteralAndReusesTheSamePublication(t *testing.T) {
	app, _ := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	ed.SetDiagnostics(editorDiagnosticFacts(ed, []editor.LSPDiagnostic{
		{Line: 0, Col: 0, EndLine: 0, EndCol: 12, Severity: editor.DiagHint, Message: "HINT_ORIGINAL"},
		{Line: 0, Col: 0, EndLine: 0, EndCol: 12, Severity: editor.DiagError, Message: "ERROR_ORIGINAL\x1b]52;c;not-clipboard\a"},
	}))
	app.paintShellFrame()
	r, _ := app.screenRectOf(app.editorScreen.ID())
	cx, cy := ed.CursorScreenPosition(r.X, r.Y)
	before, draft := ed.Value(), app.Input.Value()
	_, cmd := app.Update(tea.MouseMotionMsg{X: cx, Y: cy})
	runAppCommand(t, app, cmd)
	fw := app.overlayHost.Window(ui.OverlayDiagHover)
	if fw == nil {
		t.Fatal("ordinary editor pointer did not show current diagnostics")
	}
	if app.focusedPopup() != nil {
		t.Fatal("passive diagnostic hover captured keyboard focus")
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		t.Fatal("hover kept a separate legacy label-stack path")
	}
	area := frame.Body().(*ui.TextArea)
	text := area.Value()
	if strings.ContainsRune(text, '\x1b') || strings.ContainsRune(text, '\a') || !strings.Contains(text, "ERROR_ORIGINAL\\x1b]52;") || !strings.Contains(text, "HINT_ORIGINAL") {
		t.Fatalf("diagnostic message controls were interpreted or original text lost: %q", text)
	}
	for i := 0; i < 10; i++ {
		_, cmd = app.Update(tea.MouseMotionMsg{X: cx, Y: cy})
		runAppCommand(t, app, cmd)
	}
	if app.overlayHost.Window(ui.OverlayDiagHover) != fw || frame.Body() != area || area.Value() != text {
		t.Fatal("same-publication hover rebuilt its retained text")
	}
	if ed.Value() != before || app.Input.Value() != draft {
		t.Fatal("diagnostic hover changed editor/private draft")
	}
	facts := ed.DiagnosticFacts()
	facts.ID = "new-passive-publication"
	ed.SetDiagnostics(facts)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 32})
	if app.overlayHost.IsOpen(ui.OverlayDiagHover) {
		t.Fatal("passive diagnostic survived replacement publication")
	}
}

func TestDiagnosticHoverNativeWheelAndExplicitReadingDoNotFallThrough(t *testing.T) {
	app, _ := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	message := strings.Repeat("Original hover evidence.\n", 30) + "HOVER_ORIGINAL_TAIL"
	ed.SetDiagnostics(editorDiagnosticFacts(ed, []editor.LSPDiagnostic{{Line: 0, Col: 0, EndLine: 0, EndCol: 12, Severity: editor.DiagError, Message: message}}))
	app.paintShellFrame()
	r, _ := app.screenRectOf(app.editorScreen.ID())
	cx, cy := ed.CursorScreenPosition(r.X, r.Y)
	_, cmd := app.Update(tea.MouseMotionMsg{X: cx, Y: cy})
	runAppCommand(t, app, cmd)
	fw := app.overlayHost.Window(ui.OverlayDiagHover)
	if fw == nil {
		t.Fatal("ordinary hover did not open")
	}
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	app.View()
	frame := fw.Content.(*ui.WindowFrameWidget)
	area := frame.Body().(*ui.TextArea)
	x, y := fw.Position()
	source, draft := ed.Value(), app.Input.Value()
	// A real wheel gesture first transfers the pointer from the source into
	// the passive reader. Repeated motion must retain that exact publication.
	for i := 0; i < 2; i++ {
		_, cmd = app.Update(tea.MouseMotionMsg{X: x + 5, Y: y + 3, Button: tea.MouseNone})
		runAppCommand(t, app, cmd)
		app.View()
		if app.overlayHost.Window(ui.OverlayDiagHover) != fw {
			t.Fatal("native pointer transfer retired the diagnostic body before wheel")
		}
	}
	for i := 0; i < 2; i++ {
		_, cmd = app.Update(tea.MouseWheelMsg{X: x + 5, Y: y + 3, Button: tea.MouseWheelDown})
		runAppCommand(t, app, cmd)
	}
	if area.ScrollOffset() == 0 || app.focusedPopup() != nil {
		t.Fatal("passive diagnostic reader ignored native wheel or stole focus")
	}
	_, cmd = app.Update(tea.MouseClickMsg{X: x + 5, Y: y + 3, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	_, cmd = app.Update(tea.MouseReleaseMsg{X: x + 5, Y: y + 3, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	if app.overlayHost.Window(ui.OverlayDiagHover) != fw || app.focusedPopup() != fw {
		t.Fatal("explicit diagnostic hover click did not retain its actual native reader")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	runAppCommand(t, app, cmd)
	if area.ScrollOffset() < 20 {
		t.Fatalf("explicit diagnostic hover reading did not reach the original tail: scroll=%d focused=%s body=%+v reveal=%g",
			area.ScrollOffset(), frame.Host().Focused(), area.Bounds(), frame.Frame().Reveal())
	}
	app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if ed.Value() != source || app.Input.Value() != draft || !strings.Contains(area.Value(), "HOVER_ORIGINAL_TAIL") {
		t.Fatal("hover reading leaked edits or lost original evidence")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if app.overlayHost.IsOpen(ui.OverlayDiagHover) {
		t.Fatal("hover reader Escape did not retire")
	}
}

func TestDiagnosticDetailNativeMenuUsesClickedSourceWithoutConsumingSelection(t *testing.T) {
	app, _ := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	snapshot := ed.DocumentSnapshot()
	snapshot.Text, snapshot.Saved = []byte("outside\n\t界🙂 problem"), []byte("outside\n\t界🙂 problem")
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	ed.SetCursorPosition(1, 10)
	ed.SetDiagnostics(editorDiagnosticFacts(ed, []editor.LSPDiagnostic{{Line: 1, Col: 0, EndLine: 1, EndCol: 12, Severity: editor.DiagError, Source: "independent-source", Code: "independent-code", Message: "CLICKED_SOURCE_DIAGNOSTIC"}}))
	app.paintShellFrame()
	r, _ := app.screenRectOf(app.editorScreen.ID())
	cx, cy := ed.CursorScreenPosition(r.X, r.Y)
	ed.SetSelection(0, 0, 0, 3)
	before := ed.SelectedText()
	_, cmd := app.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseRight})
	runAppCommand(t, app, cmd)
	menu := app.LastContextMenu
	if menu == nil {
		t.Fatal("right click did not mount menu")
	}
	target := -1
	for i := range menu.FlatItems {
		s, j, ok := menu.FlatItemAt(i)
		if ok && strings.HasPrefix(menu.Sections[s].Items[j].Label, "Show 1 diagnostic") {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("native menu inspected retained selection instead of clicked source")
	}
	for n := 0; menu.Cursor != target && n < len(menu.FlatItems); n++ {
		_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		runAppCommand(t, app, cmd)
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	fw := app.overlayHost.Window(ui.OverlayDiagContext)
	if fw == nil {
		t.Fatal("clicked source details not opened")
	}
	x, y := fw.Position()
	w, h := fw.Size()
	if x > cx || x+w <= cx || (y != cy+1 && y+h != cy) {
		t.Fatalf("clicked diagnostic anchored at retained caret instead: clicked=%d:%d popup=%d:%d %dx%d", cx, cy, x, y, w, h)
	}
	text := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextArea).Value()
	for _, v := range []string{"CLICKED_SOURCE_DIAGNOSTIC", "Source: independent-source", "Code: independent-code", "Error 1 of 1", "Fact "} {
		if !strings.Contains(text, v) {
			t.Fatal("complete original diagnostic metadata missing", v, text)
		}
	}
	if !ed.HasSelection() || ed.SelectedText() != before || app.Input.Value() != "keep the private draft" {
		t.Fatal("diagnostic right click consumed retained selection/draft")
	}
}

func TestDiagnosticDetailRejectsAReplacedPublicationWhileNativeMenuRemainsOpen(t *testing.T) {
	app, _ := signatureTestApp(t, 100)
	ed := app.EditorState.Editor
	ed.SetDiagnostics(editorDiagnosticFacts(ed, []editor.LSPDiagnostic{{Line: 0, Col: 0, EndLine: 0, EndCol: 12, Severity: editor.DiagError, Message: "OLD_ORIGINAL"}}))
	app.paintShellFrame()
	r, _ := app.screenRectOf(app.editorScreen.ID())
	cx, cy := ed.CursorScreenPosition(r.X, r.Y)
	_, cmd := app.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseRight})
	runAppCommand(t, app, cmd)
	menu := app.LastContextMenu
	target := -1
	for i := range menu.FlatItems {
		s, j, ok := menu.FlatItemAt(i)
		if ok && strings.HasPrefix(menu.Sections[s].Items[j].Label, "Show 1 diagnostic") {
			target = i
			break
		}
	}
	if target < 0 {
		t.Fatal("native menu diagnostic row missing")
	}
	for n := 0; menu.Cursor != target && n < len(menu.FlatItems); n++ {
		_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		runAppCommand(t, app, cmd)
	}
	facts := ed.DiagnosticFacts()
	facts.ID = "same-source-replacement"
	facts.Diagnostics[0].Message = "REPLACEMENT_ORIGINAL"
	ed.SetDiagnostics(facts)
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	if app.overlayHost.IsOpen(ui.OverlayDiagContext) {
		t.Fatal("old native menu action borrowed a replacement diagnostic publication")
	}
}

func TestDiagnosticDetailsRefuseDisplayBudgetWithoutTruncatingAValidAnswer(t *testing.T) {
	app, fw := diagnosticIndependentMenu(t, 80, 24, strings.Repeat("Original diagnostic.", 4000)+"NO_CLIPPED_TAIL")
	area := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextArea)
	if !strings.Contains(area.Value(), "64 KiB reader budget") || strings.Contains(area.Value(), "Original diagnostic.") {
		t.Fatal("oversized diagnostic presented a clipped apparently-complete answer")
	}
	if !strings.HasSuffix(app.EditorState.Editor.DiagnosticFacts().Diagnostics[0].Message, "NO_CLIPPED_TAIL") {
		t.Fatal("display refusal destroyed original retained diagnostic evidence")
	}
}

func TestDiagnosticDetailEscapeDismissesTextSelectionBeforeTheReader(t *testing.T) {
	app, fw := diagnosticIndependentMenu(t, 80, 24, "ORIGINAL_DIAGNOSTIC_SELECTED_TEXT")
	area := fw.Content.(*ui.WindowFrameWidget).Body().(*ui.TextArea)
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	runAppCommand(t, app, cmd)
	if !area.HasActiveSelection() {
		t.Fatal("reader did not establish native text selection")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	runAppCommand(t, app, cmd)
	if app.overlayHost.Window(ui.OverlayDiagContext) != fw || area.HasActiveSelection() {
		t.Fatal("diagnostic Escape bypassed the shared text-selection dismissal before closing")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	runAppCommand(t, app, cmd)
	if app.overlayHost.IsOpen(ui.OverlayDiagContext) || !app.EditorState.Editor.IsOpen() {
		t.Fatal("second diagnostic Escape did not close only the reader")
	}
}
