package viewmodel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core/port"
	"crude/tui/editor"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
	"crude/workspace/fileio"
)

const fileLinkSource = "package demo\n\t界 target\n// سلام دنیا | שלום עולם\n"
const fileLinkDraft = "سلام دنیا | KEEP MY DRAFT"

func fileLinkApp(t *testing.T, width, height int) (*App, string) {
	t.Helper()
	app := newTranscriptAttachmentApp(t)
	app.width, app.height = width, height
	root := t.TempDir()
	wsOf(app).RootPath = root
	app.Tree.Visible = false
	app.BufferMgr = editor.NewBufferManager()
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(fileLinkSource), 0600); err != nil {
		t.Fatal(err)
	}
	app.appendActiveMessage(types.ChatMessage{InternalID: "files", Role: types.RoleAssistant,
		Content: "Open `src/main.go:2:4`.\n\nسلام دنیا، این متن فارسی است.\n\nשלום עולם, mixed English 123."})
	app.Input.SetInput(fileLinkDraft)
	app.setFocusInput()
	return app, path
}

func paintedFileLink(app *App) (int, int, bool) { return findPaintedLink(app, "src/main.go:2:4") }
func findPaintedLink(app *App, target string) (x, y int, ok bool) {
	app.paintShellFrame()
	buf := app.mainSurf.Buffer()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
		for x := app.chatRect.X; x < app.chatRect.X+app.chatRect.W; x++ {
			if link := buf.Get(x, y).Link; link.File() && link.Target() == target {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

func verifyFileLinkLanding(app *App, path string) error {
	ed := app.EditorState.Editor
	if ed == nil || ed.FilePath() != path || ed.Value() != fileLinkSource || ed.CursorLine() != 1 || ed.CursorCol() != 7 {
		return fmt.Errorf("file link did not land on the original source at 2:4")
	}
	if app.Input.Value() != fileLinkDraft {
		return fmt.Errorf("file navigation changed the composer draft")
	}
	return nil
}

func TestChatFileLinksOpenFromPaintedTextAndPreserveDraft(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, path := fileLinkApp(t, size[0], size[1])
			x, y, ok := paintedFileLink(app)
			if !ok {
				t.Fatal("no file link painted")
			}
			t.Logf("FILE_LINK_POINT size=%dx%d point=%d,%d", size[0], size[1], x, y)
			footer := rowText(app, app.height-1)
			if strings.Contains(footer, "history") || strings.Contains(footer, "find in chat") || !strings.Contains(footer, "new line") ||
				size[0] >= 120 && !strings.Contains(footer, "attach file") {
				t.Fatalf("wrong composer guidance: %q", footer)
			}
			_, press := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			if app.EditorState.ActiveView != "" {
				t.Fatal("press opened the file before release")
			}
			_, release := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			runAppCommand(t, app, press)
			runAppCommand(t, app, release)
			if err := verifyFileLinkLanding(app, path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestChatFileOpenKeepsTheCanonicalUnsavedDocumentAndFencesLateResults(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(fileLinkSource), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	ed := liveTestOpen(t, app, path)
	ed.SetCursorPosition(0, 0)
	ed.InsertRune('!')
	settleLive(t, app)
	runAppCommand(t, app, app.openChatFile("main.go#L2C4"))
	if app.EditorState.Editor.Value() != "!"+fileLinkSource || !app.EditorState.Editor.Modified() || app.EditorState.Editor.CursorLine() != 1 || app.EditorState.Editor.CursorCol() != 7 {
		t.Fatal("a file link replaced unsaved canonical text or lost its location")
	}
	late := app.openChatFile("main.go:1")
	msg := late().(paneFilesOpenedMsg)
	app.workspaceSwitchGen.Add(1)
	runAppCommand(t, app, app.applyPaneFilesOpened(msg))
	if app.EditorState.Editor.CursorLine() != 1 {
		t.Fatal("an old workspace open moved the current caret")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != fileLinkSource {
		t.Fatal("opening a file link wrote to disk", err)
	}
}

// Native capture uses ordinary mouse input. F4 only observes, F9 exits.
func TestChatFileLinksVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_FILE_LINKS") != "1" {
		t.Skip("requires scripts/tuishot.sh")
	}
	app, path := fileLinkApp(t, 160, 40)
	model := &fileLinkVisualModel{App: app, path: path}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if model.failure != nil || model.phase != 2 {
		t.Fatalf("file-link native journey: phase=%d err=%v", model.phase, model.failure)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != fileLinkSource {
		t.Fatal("native link navigation changed the source", err)
	}
}

type fileLinkVisualModel struct {
	*App
	path    string
	phase   int
	failure error
}

type fileLinkEvidenceMsg struct {
	err  error
	quit bool
}

func (m *fileLinkVisualModel) Init() tea.Cmd {
	// This synthetic App does not use NewApp's settings bootstrap. Publish the
	// same policy and host mode before native paint, especially in VTE.
	m.applyTextDirectionSettings(m.ws.LoadAppSettings())
	return tickCmd()
}

func (m *fileLinkVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if result, ok := msg.(fileLinkEvidenceMsg); ok {
		if result.err != nil {
			m.failure = result.err
		}
		if m.failure != nil || result.quit {
			return m, tea.Quit
		}
		return m, nil
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF4:
			var evidence string
			if m.phase == 0 {
				x, y, ok := paintedFileLink(m.App)
				if !ok {
					m.failure = fmt.Errorf("no file link in the native frame")
				} else {
					m.phase++
					evidence = fmt.Sprintf("FILE_LINK_POINT %d,%d\nFILE_LINK_READY phase=1\n", x, y)
				}
			} else {
				m.failure = verifyFileLinkLanding(m.App, m.path)
				if m.failure == nil {
					m.phase++
					evidence = "FILE_LINK_READY phase=2\n"
				}
			}
			if m.failure != nil {
				evidence = "FILE_LINK_FAILED " + m.failure.Error() + "\n"
			}
			return m, func() tea.Msg { return fileLinkEvidenceMsg{err: m.evidence(evidence)} }
		case tea.KeyF9:
			return m, func() tea.Msg { return fileLinkEvidenceMsg{err: m.evidence("FILE_LINK_SUCCESS\n"), quit: true} }
		}
	}
	_, command := m.App.Update(msg)
	return m, command
}

func (m *fileLinkVisualModel) evidence(value string) error {
	file, err := os.OpenFile(os.Getenv("CRUDE_FILE_LINK_RESULT"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// chatLinkState is what a reference that is not an exact match must leave
// alone until the user chooses a file in Go to File.
type chatLinkState struct {
	tabs   int
	text   string
	scroll int
}

func captureChatLinkState(app *App) chatLinkState {
	state := chatLinkState{tabs: len(app.Win.GroupTabs(app.Win.ActiveGroup()))}
	if app.EditorState != nil && app.EditorState.Editor != nil {
		state.text = app.EditorState.Editor.Value()
	}
	if app.chatScreen != nil {
		state.scroll = app.chatScreen.ScrollModel().Offset()
	}
	return state
}

// A short reference that names exactly one file opens it at the cited place.
func TestChatFileShortReferenceOpensItsOnlyExactMatch(t *testing.T) {
	app, path := fileLinkApp(t, 160, 40)
	runAppCommand(t, app, app.openChatFile("main.go:2:4"))
	if err := verifyFileLinkLanding(app, path); err != nil {
		t.Fatal(err)
	}
	if app.findInFiles != nil {
		t.Fatal("an exact match opened Go to File")
	}
}

// Two files share the cited name and another editor has unsaved edits. The
// link opens Go to File with the reference; nothing else changes until the
// user chooses, and the chosen file opens at the cited line.
func TestChatFileAmbiguousReferenceOpensGoToFileWithoutChangingTheWorkspace(t *testing.T) {
	app, path := fileLinkApp(t, 160, 40)
	root := wsOf(app).RootPath
	for _, dir := range []string{"core/client", "core/port/remote"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "files.go"), []byte("package "+filepath.Base(dir)+"\n\nfunc one() {}\nfunc two() {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runAppCommand(t, app, app.openSourceLocation(path, 1, 1))
	dirty := app.EditorState.Editor
	dirty.InsertRune('!')
	app.Win.SwitchTo(0)
	app.handleTabSwitch()
	app.appendActiveMessage(types.ChatMessage{Role: types.RoleAssistant, InternalID: "ambiguous", Content: "Open `files.go:3`."})
	app.paintShellFrame()
	before := captureChatLinkState(app)
	send := func(msg tea.Msg) { _, cmd := app.Update(msg); runAppCommand(t, app, cmd) }
	click := func() {
		x, y, ok := findPaintedLink(app, "files.go:3")
		if !ok {
			t.Fatal("partial reference not painted")
		}
		send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		send(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	}
	click()
	search := app.findInFiles
	if search == nil || search.Mode() != findinfiles.ModeFiles || search.Query() != "files.go" {
		t.Fatalf("an ambiguous reference did not open Go to File with it: %+v", search)
	}
	if after := captureChatLinkState(app); after != before {
		t.Fatalf("Go to File changed the workspace before a choice: %+v -> %+v", before, after)
	}
	var choices []fileio.ProjectSearchMatch
	for _, match := range search.Results() {
		if !match.NameOnly {
			t.Fatalf("Go to File returned a content match: %+v", match)
		}
		if strings.HasSuffix(match.RelativePath, "files.go") {
			choices = append(choices, match)
		}
	}
	if len(choices) != 2 {
		t.Fatalf("Go to File did not offer both files: %+v", search.Results())
	}
	if after := captureChatLinkState(app); after != before {
		t.Fatalf("searching changed the workspace: %+v -> %+v", before, after)
	}
	hover := func(i int) {
		t.Helper()
		app.View()
		fw := app.overlayHost.Window(ui.OverlayFindInFiles)
		pos, ok := fw.Content.(*ui.WindowFrameWidget).Host().ScreenRect(findinfiles.ListID)
		if !ok {
			t.Fatal("no result list")
		}
		x, y := fw.Position()
		send(tea.MouseMotionMsg{X: x + pos.X + 2, Y: y + pos.Y + i, Button: tea.MouseNone})
		preview := app.findInFiles.SyntaxEditor()
		if preview == nil || !strings.Contains(preview.Value(), "package "+filepath.Base(filepath.Dir(choices[i].Path))) {
			t.Fatalf("hover %d did not show its source", i)
		}
		if captureChatLinkState(app) != before || dirty.Value() != "!"+fileLinkSource {
			t.Fatal("preview changed editor, tabs or chat scroll")
		}
	}
	hover(0)
	hover(1)
	send(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.findInFiles != nil || captureChatLinkState(app) != before {
		t.Fatal("Escape did not restore chat")
	}
	click()
	hover(1)
	send(tea.KeyPressMsg{Code: tea.KeyEnter})
	ed := app.EditorState.Editor
	if ed == nil || filepath.ToSlash(ed.FilePath()) != filepath.ToSlash(filepath.Join(root, choices[1].RelativePath)) || ed.CursorLine() != 2 {
		t.Fatal("the chosen file did not open at the cited line")
	}
	if app.Input.Value() != fileLinkDraft {
		t.Fatal("choosing a file changed the composer draft")
	}
}

// A reference to a missing file creates no document, even briefly: the opener
// refuses it and Go to File offers to find the name.
func TestChatFileMissingReferenceCreatesNoDocument(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(fileLinkSource), 0600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	tabs := len(app.Win.GroupTabs(app.Win.ActiveGroup()))
	editorBefore := app.EditorState.Editor
	runAppCommand(t, app, app.openChatFile("internal/missing.go:3"))
	if search := app.findInFiles; search == nil || search.Mode() != findinfiles.ModeFiles || search.Query() != "internal/missing.go" {
		t.Fatal("a missing reference did not offer Go to File")
	}
	if len(app.Win.GroupTabs(app.Win.ActiveGroup())) != tabs || app.EditorState.Editor != editorBefore {
		t.Fatal("a missing reference opened an editor")
	}
	missingImage := "missing.png"
	runAppCommand(t, app, app.openChatFile(missingImage))
	if app.findInFiles == nil || app.findInFiles.Query() != missingImage || len(app.Win.GroupTabs(app.Win.ActiveGroup())) != tabs {
		t.Fatal("a missing image did not use the same file chooser")
	}
	// The checked opener is the last fence: a file deleted after it was
	// chosen is reported, and the core never creates a document.
	runAppCommand(t, app, app.openSourceLocationChecked(filepath.Join(root, "gone.go"), 1, 1, true))
	if len(app.Win.GroupTabs(app.Win.ActiveGroup())) != tabs || app.EditorState.Editor != editorBefore {
		t.Fatal("the checked opener created a document for a missing file")
	}
	if _, err := os.Stat(filepath.Join(root, "gone.go")); !os.IsNotExist(err) {
		t.Fatal("a missing reference created a file", err)
	}
}

// A web link in chat asks before Crude starts the browser; Escape keeps
// everything as it was. Terminal link handling (Shift+click) is not needed.
func TestChatWebLinkAsksBeforeOpening(t *testing.T) {
	app, _ := fileLinkApp(t, 160, 40)
	const target = "https://example.com/docs/a/very/long/path/that/a/table/would/shorten?with=query"
	app.appendActiveMessage(types.ChatMessage{InternalID: "web", Role: types.RoleAssistant, Content: "Read [the guide](" + target + ")."})
	app.paintShellFrame()
	buf := app.mainSurf.Buffer()
	x, y, found := 0, 0, false
	for row := app.chatRect.Y; row < app.chatRect.Y+app.chatRect.H && !found; row++ {
		for col := app.chatRect.X; col < app.chatRect.X+app.chatRect.W; col++ {
			if link := buf.Get(col, row).Link; link != nil && !link.File() && link.Target() == target {
				x, y, found = col, row, true
				break
			}
		}
	}
	if !found {
		t.Fatal("no web link painted")
	}
	_, press := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	_, release := app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	runAppCommand(t, app, press)
	runAppCommand(t, app, release)
	if app.webLink == nil || app.webLink.target != target || !app.overlayHost.IsOpen(webLinkOverlay) {
		t.Fatal("a web link click did not ask before opening")
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.webLink != nil || app.overlayHost.IsOpen(webLinkOverlay) {
		t.Fatal("Escape did not dismiss the link prompt")
	}
	if app.Input.Value() != fileLinkDraft {
		t.Fatal("the link prompt changed the composer draft")
	}
}

type fileLinkCheckoutPort struct {
	shellport.Workspace
	checkout  string
	requested string
}

func (p *fileLinkCheckoutPort) CatalogExecutionFiles(ctx context.Context, id string, hidden bool) (workspace.BrowserCatalog, error) {
	p.requested = id
	if id != "producer" {
		return workspace.BrowserCatalog{}, fmt.Errorf("no such execution")
	}
	return workspace.ScanBrowserPaths(ctx, workspace.BrowserPathRequest{Root: p.checkout, IncludeHidden: hidden})
}

func TestPaintedReferenceUsesProducingCheckout(t *testing.T) {
	app, _ := fileLinkApp(t, 160, 40)
	root := t.TempDir()
	p := &fileLinkCheckoutPort{Workspace: app.ws, checkout: root}
	app.ws = p
	path := filepath.Join(root, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("checkout-only\nsecond\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app.appendActiveMessage(types.ChatMessage{Role: types.RoleAssistant, InternalID: "scoped", ExecutionID: "producer", Content: "Read `main.go:2`."})
	x, y, ok := findPaintedLink(app, "main.go:2")
	if !ok {
		t.Fatal("no painted source reference")
	}
	for _, msg := range []tea.Msg{tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}, tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}} {
		_, cmd := app.Update(msg)
		runAppCommand(t, app, cmd)
	}
	ed := app.EditorState.Editor
	if p.requested != "producer" || ed == nil || ed.FilePath() != path || ed.Value() != "checkout-only\nsecond\n" || ed.CursorLine() != 1 {
		t.Fatal("link did not open original checkout at its cited location")
	}
}

func TestWebLinkConfirmationBindsExactDestinationAndShellLifetime(t *testing.T) {
	for _, key := range []string{"SSH_TTY", "SSH_CLIENT", "SSH_CONNECTION"} {
		t.Setenv(key, "")
	}
	app, _ := fileLinkApp(t, 80, 24)
	target := "http://example.org/path/" + strings.Repeat("unchanged/", 60) + "?a=b&c=d"
	var started []string
	launch := func(_ context.Context, url string) error { started = append(started, url); return nil }
	app.confirmWebLink(target, launch)
	if app.webLink == nil {
		t.Fatal("no confirmation")
	}
	_, cancel := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cancel)
	if app.webLink != nil || len(started) != 0 {
		t.Fatal("Enter was not safe by default")
	}
	app.confirmWebLink(target, launch)
	prompt := app.webLink
	cmd := prompt.activate(0)
	if cmd == nil || len(started) != 0 || prompt.activate(0) != nil {
		t.Fatal("accept did not give one deferred launch")
	}
	runAppCommand(t, app, cmd)
	if len(started) != 1 || started[0] != target {
		t.Fatal("the confirmed exact address was not passed to the browser")
	}
	app.confirmWebLink(target, launch)
	late := app.webLink.activate(0)
	app.workspaceSwitchGen.Add(1)
	runAppCommand(t, app, late)
	if len(started) != 1 {
		t.Fatal("a stale workspace launched a browser")
	}
	t.Setenv("SSH_TTY", "remote")
	app.confirmWebLink(target, launch)
	if app.webLink == nil {
		t.Fatal("SSH did not offer a copy")
	}
	w := app.overlayHost.Window(webLinkOverlay).Content.(*ui.ComponentWidget)
	if ui.ConfirmButtonCount(w) != 2 {
		t.Fatal("SSH offered a browser launch")
	}
}
