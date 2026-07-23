package viewmodel

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/editor"
	"crude/tui/screens/confirmation"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

func TestFindInFilesShortcutRoutesTypingAndPaste(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	app.RootPath = t.TempDir()
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)

	key := tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModAlt}
	app.dispatchKeyPress(key)
	if app.findInFiles == nil || app.windowManager.Window(findInFilesWindowID) == nil {
		t.Fatal("Ctrl+Alt+F should open Find in Files")
	}
	fw := app.windowManager.Window(findInFilesWindowID)
	if !fw.Movable() || !fw.Resizable() {
		t.Fatal("Find in Files should remain a draggable/resizable IDE surface")
	}
	if _, ok := fw.Content.(*ui.WindowFrameWidget); !ok {
		t.Fatal("Find in Files should use ui.WindowFrame chrome")
	}
	if body, ok := fw.Content.(*ui.WindowFrameWidget).Body().(*findinfiles.Screen); !ok || body != app.findInFiles {
		t.Fatal("Find in Files should mount its native component body")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'n', Text: "needle"})
	if got := app.findInFiles.Query(); got != "needle" {
		t.Fatalf("focused search query=%q", got)
	}
	app.dispatchPasteMsg(tea.PasteMsg{Content: " pasted"})
	if got := app.findInFiles.Query(); got != "needle pasted" {
		t.Fatalf("pasted search query=%q", got)
	}
}

func TestFindInFilesSearchRoutesThroughWorkspaceEvent(t *testing.T) {
	root := t.TempDir()
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	app.RootPath = root
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.toggleFindInFiles()

	search := app.findInFiles
	search.SetQuery("SearchTarget")
	search.SetSearchDebounce(0)
	debounce := search.ScheduleSearch()
	if debounce == nil {
		t.Fatal("search schedule should return a debounce tick")
	}
	debounced, ok := debounce().(ui.SearchDebouncedMsg)
	if !ok {
		t.Fatalf("debounce produced %T", debounce())
	}
	requestCmd := search.HandleSearchDebounced(debounced)
	if requestCmd == nil {
		t.Fatal("accepted debounce should produce a search request")
	}
	request, ok := requestCmd().(findinfiles.SearchRequestMsg)
	if !ok {
		t.Fatalf("search request produced %T", requestCmd())
	}
	cmds := app.handleAppMessages(request)
	if len(cmds) != 0 {
		t.Fatalf("workspace search should publish through event bus, got %d TUI commands", len(cmds))
	}
	requestID := app.findInFilesRequest
	if requestID == 0 {
		t.Fatal("viewmodel did not start workspace search")
	}
	app.handleAppMessages(workspace.ProjectSearchCompletedMsg{Result: workspace.ProjectSearchResult{
		RequestID: requestID,
		Matches: []fileio.ProjectSearchMatch{{
			Path:         filepath.Join(root, "target.go"),
			RelativePath: "target.go",
			Line:         2,
			LineText:     "func SearchTarget() {}",
			ContextStart: 1,
			Context:      "package sample\nfunc SearchTarget() {}",
		}},
	}})
	if got := app.findInFiles.Results(); len(got) != 1 || got[0].RelativePath != "target.go" {
		t.Fatalf("workspace search results=%+v", got)
	}
}

func TestFindInFilesIgnoresStaleOwnerMessages(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	app.RootPath = t.TempDir()
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.toggleFindInFiles()
	stale := app.findInFiles
	app.closeFindInFiles()
	app.toggleFindInFiles()
	current := app.findInFiles

	app.handleAppMessages(findinfiles.CloseMsg{Screen: stale})
	if app.findInFiles != current || app.windowManager.Window(findInFilesWindowID) == nil {
		t.Fatal("stale close should not affect the current Find in Files screen")
	}

	app.handleAppMessages(findinfiles.OpenMsg{Screen: stale, Match: fileio.ProjectSearchMatch{Path: filepath.Join(app.RootPath, "stale.go")}})
	if app.findInFiles != current || app.windowManager.Window(findInFilesWindowID) == nil {
		t.Fatal("stale open should not affect the current Find in Files screen")
	}
}

func TestClosingFindInFilesPreservesBlockingSessionPanel(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	app.RootPath = t.TempDir()
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	projection := app.activeSessionProjection()
	projection.confirmation.Reset(confirmation.Details{Question: "Continue?"})
	projection.confirmationAgentID = "agent"
	app.openSessionConfirmationPanel(projection)
	app.toggleFindInFiles()

	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.windowManager.Window(findInFilesWindowID) != nil || app.findInFiles != nil {
		t.Fatal("Esc should close and clean up Find in Files")
	}
	if !projection.deck.IsOpen() || projection.deck.Active().ID != sessionConfirmationPanelID || !app.sessionDeckBlocking() {
		t.Fatal("closing floating search changed the underlying blocking session panel")
	}
}

func TestBlockingSessionPanelWinsOverEditorTreeAndLauncher(t *testing.T) {
	app := newFullTestApp(t)
	projection := app.activeSessionProjection()
	projection.confirmation.Reset(confirmation.Details{Question: "Continue?"})
	app.openSessionConfirmationPanel(projection)
	app.FocusStack.Push(FocusTree)
	app.EditorState.ActiveView = "/tmp/main.go"
	app.EditorState.Editor = editor.NewPreview(app.Theme, "/tmp/main.go", "package main\n", 1)
	if !app.sessionDeckCapturesInput() {
		t.Fatal("blocking session panel did not capture input over editor/tree")
	}

	app.EditorState.ActiveView = ""
	app.EditorState.Editor = nil
	if !app.sessionDeckCapturesInput() {
		t.Fatal("blocking session panel did not capture input over launcher")
	}
}

func TestFindInFilesActivationOpensAndPositionsEditor(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nfunc target() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	app.RootPath = root
	app.BufferMgr = editor.NewBufferManager()
	app.LSPMgr = workspace.NewLSPManager(nil)
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.findInFiles = nil

	cmd := app.openFindInFilesResult(fileio.ProjectSearchMatch{
		Path: path, RelativePath: "main.go", Line: 3, Column: 5, LineText: "func target() {}",
	})
	if app.EditorState.ActiveView != path || app.EditorState.Editor == nil {
		t.Fatal("search activation should open editor tab immediately")
	}
	if app.EditorState.Editor.CursorLine() != 2 || app.EditorState.Editor.CursorCol() != 5 {
		t.Fatalf("cursor=%d:%d", app.EditorState.Editor.CursorLine(), app.EditorState.Editor.CursorCol())
	}
	if cmd == nil {
		t.Fatal("open should return asynchronous blink/LSP command")
	}
}
