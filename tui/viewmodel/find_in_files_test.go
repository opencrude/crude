package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/localhistory"
	"crude/tui/cell"
	"crude/tui/editor"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

func TestFindInFilesResultLandingBeforeFirstPaint(t *testing.T) {
	app := newFullTestApp(t)
	app.BufferMgr = editor.NewBufferManager()
	app.width, app.height = 120, 36
	path := filepath.Join(t.TempDir(), "small.txt")
	if err := os.WriteFile(path, []byte("first\n\t界 target\nlast"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := app.openFindInFilesResult(fileio.ProjectSearchMatch{Path: path, RelativePath: "small.txt", Line: 2, Column: 3, EndColumn: 9, LineText: "\t界 target"})
	if command == nil || len(app.EditorState.Tabs) != 0 {
		t.Fatal("result open read/published a file before the command")
	}
	app.applyPaneFilesOpened(command().(paneFilesOpenedMsg))
	ed := app.EditorState.Editor
	if ed.CursorLine() != 1 || ed.CursorCol() != 7 || ed.HasSelection() {
		t.Fatalf("landing line=%d col=%d selected=%v", ed.CursorLine(), ed.CursorCol(), ed.HasSelection())
	}
	surf := surface.NewSurface("result", 80, 15, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{})
	var row string
	for x := 0; x < 80; x++ {
		row += string(surf.Buffer().Get(x, 0).Char)
	}
	if !strings.Contains(row, "first") {
		t.Fatalf("short file scrolled before first paint: %q", row)
	}
}

func TestFindInFilesShortcutRoutesTypingAndPaste(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	wsOf(app).RootPath = t.TempDir()
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

func TestReplaceInFilesShortcutOpensSiblingOverlay(t *testing.T) {
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	wsOf(app).RootPath = t.TempDir()
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)

	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'h', Mod: tea.ModCtrl | tea.ModAlt})
	if app.findInFiles == nil || app.findInFiles.Mode() != findinfiles.ModeReplace {
		t.Fatal("Ctrl+Alt+H should open Replace in Files")
	}
	if app.windowManager.Window(replaceInFilesWindowID) == nil {
		t.Fatal("Replace in Files window missing")
	}
	if body, ok := app.windowManager.Window(replaceInFilesWindowID).Content.(*ui.WindowFrameWidget).Body().(*findinfiles.Screen); !ok || body != app.findInFiles {
		t.Fatal("Replace in Files should mount the native search body")
	}
}

func TestReplaceInFilesAppliesToDiskAndRefreshes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("alpha\nalpha beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	wsOf(app).RootPath = root
	wsOf(app).FileOps = workspace.NewFileOpsService(root, nil, nil)
	store, err := localhistory.Open(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	wsOf(app).LocalHistory = store
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.toggleReplaceInFiles()
	search := app.findInFiles
	search.SetQuery("alpha")
	search.SetReplacement("omega")
	search.ScheduleSearch()
	search.ApplySearchResults(search.Generation(), []fileio.ProjectSearchMatch{
		{Path: path, RelativePath: "main.go", Line: 1, Column: 0, EndColumn: 5, LineText: "alpha", TextMatches: []int{0, 1, 2, 3, 4}},
		{Path: path, RelativePath: "main.go", Line: 2, Column: 0, EndColumn: 5, LineText: "alpha beta", TextMatches: []int{0, 1, 2, 3, 4}},
	}, nil, 0)

	cmd := app.applyProjectReplace(findinfiles.ReplaceMsg{
		Screen: search, Generation: search.Generation(), All: true, Query: "alpha", Replacement: "omega",
	})
	if cmd == nil {
		t.Fatal("replace all should prepare a review off Update")
	}
	before, _ := os.ReadFile(path)
	if string(before) != "alpha\nalpha beta\n" {
		t.Fatal("replacement mutated during intent")
	}
	runAppCommand(t, app, cmd)
	if app.projectReplace == nil {
		t.Fatal("replacement review missing")
	}
	p := app.projectReplace
	runAppCommand(t, app, app.handleProjectReplaceAction(projectReplaceActionMsg{p, "apply"}))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "omega\nomega beta\n" {
		t.Fatalf("disk=%q", data)
	}
}

func TestFindInFilesSearchRoutesThroughWorkspaceEvent(t *testing.T) {
	root := t.TempDir()
	app := newFullTestApp(t)
	app.width = 120
	app.height = 36
	wsOf(app).RootPath = root
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
	wsOf(app).RootPath = t.TempDir()
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

	app.handleAppMessages(findinfiles.OpenMsg{Screen: stale, Match: fileio.ProjectSearchMatch{Path: filepath.Join(app.ws.RootPath(), "stale.go")}})
	if app.findInFiles != current || app.windowManager.Window(findInFilesWindowID) == nil {
		t.Fatal("stale open should not affect the current Find in Files screen")
	}
}

func TestClosingFindInFilesPreservesBlockingSessionPanel(t *testing.T) {
	app := newFullTestApp(t)
	selectDeckSession(t, app, app.ActiveSession, "agent")
	app.width = 120
	app.height = 36
	wsOf(app).RootPath = t.TempDir()
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	projection := app.activeSessionProjection()
	prompt := openTestAgentPrompt(t, app, projection, agentPromptRequest{question: "Continue?"})
	app.toggleFindInFiles()

	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.windowManager.Window(findInFilesWindowID) != nil || app.findInFiles != nil {
		t.Fatal("Esc should close and clean up Find in Files")
	}
	if !projection.deck.IsOpen() || projection.deck.Active().ID != prompt.panelID() || !app.sessionDeckBlocking() {
		t.Fatal("closing floating search changed the underlying blocking session panel")
	}
}

func TestBlockingSessionPanelWinsOverEditorTreeAndLauncher(t *testing.T) {
	app := newFullTestApp(t)
	selectDeckSession(t, app, app.ActiveSession, "agent")
	projection := app.activeSessionProjection()
	openTestAgentPrompt(t, app, projection, agentPromptRequest{question: "Continue?"})
	app.FocusStack.Push(FocusTree)
	app.EditorState.ActiveView = "/tmp/main.go"
	app.EditorState.Editor = editor.NewPreview(app.Theme, "/tmp/main.go", "package main\n", 1)
	if !app.sessionDeckCapturesInput() {
		t.Fatal("stale focus on an unmounted tree bypassed a blocking session panel")
	}

	app.EditorState.ActiveView = ""
	app.EditorState.Editor = nil
	app.FocusStack.Pop()
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
	wsOf(app).RootPath = root
	app.BufferMgr = editor.NewBufferManager()
	wsOf(app).LSPMgr = workspace.NewLSPManager(nil)
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.findInFiles = nil

	cmd := app.openFindInFilesResult(fileio.ProjectSearchMatch{
		Path: path, RelativePath: "main.go", Line: 3, Column: 5, LineText: "func target() {}",
	})
	if cmd == nil || app.EditorState.ActiveView != "" {
		t.Fatal("search activation must prepare file off Update")
	}
	app.applyPaneFilesOpened(cmd().(paneFilesOpenedMsg))
	if app.EditorState.ActiveView != path || app.EditorState.Editor == nil {
		t.Fatal("accepted search preparation should open editor tab")
	}
	if app.EditorState.Editor.CursorLine() != 2 || app.EditorState.Editor.CursorCol() != 5 {
		t.Fatalf("cursor=%d:%d", app.EditorState.Editor.CursorLine(), app.EditorState.Editor.CursorCol())
	}
}

func TestFindInFilesRejectsOldQueryActivationAndChangedEditorTarget(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "changed-result.txt")
	ed := app.EditorState.Editor
	line := ed.LineText(0)
	app.toggleFindInFiles()
	screen := app.findInFiles
	screen.SetQuery("old")
	screen.ScheduleSearch()
	activation := findinfiles.OpenMsg{Screen: screen, Generation: screen.Generation(), Match: fileio.ProjectSearchMatch{Path: paths[0], Line: 1, LineText: line}}
	screen.SetQuery("new")
	screen.ScheduleSearch()
	app.handleAppMessages(activation)
	if app.findInFiles != screen {
		t.Fatal("an old query activation closed the current search")
	}
	ed.ReplaceDocument("newer editor text\n")
	ed.SetCursorPosition(0, 3)
	command := app.openFindInFilesResult(fileio.ProjectSearchMatch{Path: paths[0], Line: 1, LineText: line, Column: 0, EndColumn: 4})
	app.applyPaneFilesOpened(command().(paneFilesOpenedMsg))
	if ed.Value() != "newer editor text\n" || ed.CursorCol() != 3 || !ed.Modified() {
		t.Fatal("stale search relocated or replaced newer live editor content")
	}
}

func TestFindInFilesLandsOnSurvivingUnsavedMatchWithoutReordering(t *testing.T) {
	app, ws := newRealShellApp(t)
	defer ws.Close()
	paneJourneySize(app, 160, 40)
	paths := openEditorTabs(t, app, "unsaved.go", "other.go")
	ed := app.EditorState.Tabs[paths[0]].Editor
	ed.ReplaceDocument("prefix target refined\n")
	before := app.Win.Tabs()
	command := app.openFindInFilesResult(fileio.ProjectSearchMatch{Path: paths[0], Line: 1, Column: 7, EndColumn: 13, LineText: "prefix target"})
	app.applyPaneFilesOpened(command().(paneFilesOpenedMsg))
	if ed.CursorCol() != 7 || ed.HasSelection() || !ed.Modified() || ed.Value() != "prefix target refined\n" {
		t.Fatal("surviving unsaved target was not safely highlighted")
	}
	for index, tab := range app.Win.Tabs() {
		if tab.ID != before[index].ID {
			t.Fatal("search activation reordered the existing tab")
		}
	}
}
