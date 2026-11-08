package viewmodel

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"

	"crude/tui/filetree"
	"crude/tui/ui"
)

func writeImageFixture(t *testing.T, root, name string) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 6), B: 80, A: 255})
		}
	}
	path := filepath.Join(root, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func openImageFromTree(t *testing.T, app *App, path string) {
	t.Helper()
	_, command := app.Update(filetree.FileSelectedMsg{Path: path})
	runAppCommand(t, app, command)
}

func TestImageFileTreeJourneyUsesReadOnlyPaneAndZoom(t *testing.T) {
	app, ws := newRealShellApp(t)
	path := writeImageFixture(t, ws.RootPath, "picture.png")
	app.Input.SetInput("keep this draft")
	app.terminalImages = terminalImageState{supported: true, cellWidth: 9, cellHeight: 20}

	openImageFromTree(t, app, path)
	tab := app.Win.ActiveTab()
	if !isImageTab(tab) || tab.Resource.ID != path || app.activeImageView() == nil {
		t.Fatalf("tree did not open a real image tab: %+v", tab)
	}
	if app.EditorState.ActiveView != "" || app.BufferMgr.Get(path) != nil || app.EditorState.Tabs[path] != nil {
		t.Fatal("image was opened as a writable text document")
	}
	hints := app.compactContextHelpSegments()
	if len(hints) == 0 || hints[0].Key != "+/-" || hints[0].Label != "zoom" {
		t.Fatalf("image focus displayed unrelated chat hints: %+v", hints)
	}
	_, hints = app.contextualHelpSegments()
	if len(hints) == 0 || hints[0].Key != "+/-" || hints[0].Label != "zoom" {
		t.Fatalf("image focus displayed unrelated expanded hints: %+v", hints)
	}
	view := app.View()
	if len(view.Images) != 1 || !strings.ContainsRune(view.RenderContent(), kitty.Placeholder) {
		t.Fatalf("the image pane did not paint Kitty graphics: placements=%d", len(view.Images))
	}
	before := view.Images[0]
	_, command := app.Update(tea.KeyPressMsg{Code: '+', Text: "+"})
	runAppCommand(t, app, command)
	app.presentImmediateFrame()
	zoomed := app.View()
	if len(zoomed.Images) != 1 || zoomed.Images[0].Image != before.Image || zoomed.Images[0].Columns <= before.Columns {
		t.Fatalf("zoom did not enlarge the original image: before=%+v after=%+v", before, zoomed.Images)
	}
	_, command = app.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	runAppCommand(t, app, command)
	app.presentImmediateFrame()
	if got := app.View().Images; len(got) != 1 || got[0].Columns != before.Columns {
		t.Fatalf("fit did not restore the image geometry: %+v", got)
	}
	_, command = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, command)
	app.presentImmediateFrame()
	if app.Win.TabByTitle(imageTabTitle(path)) != nil || len(app.View().Images) != 0 || app.Input.Value() != "keep this draft" {
		t.Fatal("closing the image left graphics or changed the composer")
	}
}

func TestImageFileControlTooltipInDockedAndFloatingPanes(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 160, 44)
	path := writeImageFixture(t, ws.RootPath, "hover.png")
	openImageFromTree(t, app, path)
	tab := app.Win.ActiveTab().ID

	for _, floating := range []bool{false, true} {
		if floating {
			app.openTabInWindow(tab)
		}
		app.paintShellFrame()
		screen := app.imageView(tab)
		if screen == nil {
			t.Fatal("image pane was not mounted")
		}
		id := ui.NewID(screen.ID(), "zoom-in")
		rect, ok := app.screenRectOf(id)
		if !ok || rect.Empty() {
			t.Fatalf("floating=%v: zoom control is not mounted", floating)
		}
		_, wait := app.Update(tea.MouseMotionMsg{X: rect.X, Y: rect.Y})
		runAppCommand(t, app, wait)
		if !app.overlayHost.IsOpen(ui.OverlayTooltip) || !strings.Contains(tooltipText(app), "Zoom in (+)") {
			t.Fatalf("floating=%v: zoom tooltip missing: %q", floating, tooltipText(app))
		}
		app.Update(tea.MouseMotionMsg{X: 0, Y: 0})
	}
}

func TestImageFileDesktopRevealRejectsRetargetedTab(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 120, 30)
	path := writeImageFixture(t, ws.RootPath, "before.png")
	openImageFromTree(t, app, path)
	tab := app.Win.ActiveTab().ID
	calls := recordDesktopLauncher(t, app)
	paneOpenTabMenu(t, app, tab)
	action := menuItem(t, app.LastContextMenu, desktopRevealLabel()).Action
	app.retargetImageTabs(path, filepath.Join(ws.RootPath, "after.png"), false)
	action()
	asyncIOStateFor(app).detachedCommands.Wait()
	requireNoDesktopLaunch(t, calls)
}

func TestImageFileSplitHasIndependentPlacementsAndFallback(t *testing.T) {
	app, ws := newRealShellApp(t)
	path := writeImageFixture(t, ws.RootPath, "two.png")
	app.terminalImages = terminalImageState{supported: true, cellWidth: 9, cellHeight: 20}
	openImageFromTree(t, app, path)
	app.presentImmediateFrame()
	app.View()
	first := app.Win.ActiveTab().ID
	app.splitEditor(ui.Horizontal, false)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	app.presentImmediateFrame()
	view := app.View()
	if len(view.Images) != 2 || view.Images[0].Image == view.Images[1].Image {
		t.Fatalf("split images need independent virtual placements: %+v", view.Images)
	}
	if app.imageView(first) == nil || app.activeImageView() == nil {
		t.Fatal("split lost a pane's image view")
	}
	app.terminalImages.supported = false
	app.refreshReferenceGraphics()
	app.presentImmediateFrame()
	view = app.View()
	if len(view.Images) != 0 || !strings.ContainsRune(view.RenderContent(), '▀') {
		t.Fatal("unsupported terminal did not render the image fallback")
	}
}

func TestImageFileWindowPublishesAndReleasesItsPlacement(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 160, 44)
	path := writeImageFixture(t, ws.RootPath, "window.png")
	app.terminalImages = terminalImageState{supported: true, cellWidth: 9, cellHeight: 20}
	runAppCommand(t, app, app.openFileInWindow(path))
	app.presentImmediateFrame()
	view := app.View()
	tab := app.Win.ActiveTab()
	if !isImageTab(tab) || !app.Win.IsFloating(tab.Group) || len(view.Images) != 1 {
		t.Fatal("image did not open with a placement in its own window")
	}
	id, group := tab.ID, tab.Group
	runAppCommand(t, app, app.closePopupWindowByID(string(paneWindowID(group))))
	app.presentImmediateFrame()
	if app.Win.TabByID(id) != nil || len(app.View().Images) != 0 {
		t.Fatal("closing the image window retained its tab or terminal placement")
	}
}

func TestImageFileWheelZoomThroughShell(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 160, 40)
	path := writeImageFixture(t, ws.RootPath, "wheel.png")
	app.terminalImages = terminalImageState{supported: true, cellWidth: 8, cellHeight: 20}
	openImageFromTree(t, app, path)
	tab := app.Win.ActiveTab().ID
	for _, floating := range []bool{false, true} {
		if floating {
			app.openTabInWindow(tab)
		}
		app.presentImmediateFrame()
		before := app.View().Images[0]
		screen := app.imageView(tab)
		bounds, ok := app.screenRectOf(screen.ID())
		if !ok {
			t.Fatal("image screen is not mounted")
		}
		x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
		_, cmd := app.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp})
		runAppCommand(t, app, cmd)
		app.presentImmediateFrame()
		after := app.View().Images
		if len(after) != 1 || after[0].Columns <= before.Columns {
			t.Fatalf("floating=%v: wheel did not zoom at %d,%d (hit=%q, before=%+v after=%+v)", floating, x, y, app.mainHost().HitTest(x, y), before, after)
		}
		_, cmd = app.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
		runAppCommand(t, app, cmd)
		app.presentImmediateFrame()
		if after := app.View().Images; len(after) != 1 || after[0].Columns != before.Columns {
			t.Fatalf("floating=%v: wheel down did not restore the scale", floating)
		}
	}
}

func TestImagePlacementPublicationIsStableAndAllocationFree(t *testing.T) {
	app, ws := newRealShellApp(t)
	app.captureTerminalImages()
	if got := testing.AllocsPerRun(100, app.captureTerminalImages); got != 0 {
		t.Fatalf("publishing a frame without images allocated %.1f times", got)
	}

	path := writeImageFixture(t, ws.RootPath, "placements.png")
	app.terminalImages = terminalImageState{supported: true, cellWidth: 9, cellHeight: 20}
	openImageFromTree(t, app, path)
	app.splitEditor(ui.Horizontal, false)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	app.presentImmediateFrame()
	app.View()
	app.captureTerminalImages()
	if len(app.imagePlacements) != 2 {
		t.Fatalf("split published %d images, want 2", len(app.imagePlacements))
	}
	first, second := app.imagePlacements[0], app.imagePlacements[1]
	for i := 0; i < 100; i++ {
		app.captureTerminalImages()
		if len(app.imagePlacements) != 2 || app.imagePlacements[0] != first || app.imagePlacements[1] != second {
			t.Fatal("unchanged split images changed publication order")
		}
	}
	if got := testing.AllocsPerRun(100, app.captureTerminalImages); got != 0 {
		t.Fatalf("publishing unchanged split images allocated %.1f times", got)
	}
}

func TestImageFileOpenFencesCloseAndWorkspaceChange(t *testing.T) {
	app, ws := newRealShellApp(t)
	path := writeImageFixture(t, ws.RootPath, "fence.png")
	group := app.Win.ActiveGroup()
	prepared := app.openPaneFiles([]string{path}, group)().(paneFilesOpenedMsg)
	app.workspaceSwitchGen.Add(1)
	app.applyPaneFilesOpened(prepared)
	if app.Win.TabByTitle(imageTabTitle(path)) != nil {
		t.Fatal("an old workspace's image appeared after switching")
	}
	openImageFromTree(t, app, path)
	tab := app.Win.TabByTitle(imageTabTitle(path))
	if tab == nil {
		t.Fatal("image was not opened")
	}
	prepared = app.openPaneFiles([]string{path}, group)().(paneFilesOpenedMsg)
	app.closeImageTab(tab.ID)
	app.applyPaneFilesOpened(prepared)
	if app.Win.TabByTitle(imageTabTitle(path)) != nil {
		t.Fatal("a late open resurrected a closed image")
	}
}

func TestImageFileRelativeReopenDoesNotRestoreAClosedTab(t *testing.T) {
	app, ws := newRealShellApp(t)
	path := writeImageFixture(t, ws.RootPath, "relative.png")
	openImageFromTree(t, app, path)
	tab := app.Win.ActiveTab()
	group := app.Win.ActiveGroup()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relative, err := filepath.Rel(cwd, path)
	if err != nil {
		t.Skipf("cannot form a relative path on this filesystem: %v", err)
	}
	prepared := app.openPaneFiles([]string{relative}, group)().(paneFilesOpenedMsg)
	if len(prepared.files) != 1 || prepared.files[0].imageTab != tab.ID {
		t.Fatal("relative reopen did not retain the existing image tab")
	}
	app.closeImageTab(tab.ID)
	app.applyPaneFilesOpened(prepared)
	if app.Win.TabByTitle(imageTabTitle(path)) != nil {
		t.Fatal("a late relative reopen resurrected the closed image")
	}
}

func TestImageFileConnectedCoreReadsProjectBytes(t *testing.T) {
	root := t.TempDir()
	path := writeImageFixture(t, root, "remote.png")
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:9")
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	app.terminalImages = terminalImageState{supported: true, cellWidth: 9, cellHeight: 20}
	openImageFromTree(t, app, path)
	app.presentImmediateFrame()
	view := app.View()
	if !isImageTab(app.Win.ActiveTab()) || len(view.Images) != 1 || !strings.ContainsRune(view.RenderContent(), kitty.Placeholder) {
		t.Fatal("connected file tree did not render the image")
	}
	if app.EditorState.Tabs[path] != nil || app.BufferMgr.Get(path) != nil {
		t.Fatal("connected image was sent through the text document service")
	}
}

func TestImageFileConnectedExternalAndDeniedProjectLink(t *testing.T) {
	root := t.TempDir()
	external := writeImageFixture(t, t.TempDir(), "external.png")
	link := filepath.Join(root, "linked.png")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("cannot create symlink on this host: %v", err)
	}
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:9")
	shell := newRemoteJourneyShell(t, runtime, root)
	app := shell.app
	openImageFromTree(t, app, link)
	if app.Win.TabByTitle(imageTabTitle(link)) != nil {
		t.Fatal("a rejected project link was retried as an external read")
	}
	openImageFromTree(t, app, external)
	if tab := app.Win.ActiveTab(); !isImageTab(tab) || tab.Resource.ID != external {
		t.Fatalf("explicit external image did not open through the connected core: %+v", tab)
	}
}

func TestImageClientPathContainmentUsesCorePathSyntax(t *testing.T) {
	for _, tc := range []struct {
		root, candidate string
		within          bool
	}{
		{"/project", "/project/images/a.png", true},
		{"/project", "/project-other/a.png", false},
		{"/", "/other/a.png", true},
		{`C:\Work\Project`, `c:\work\project\image.png`, true},
		{"C:/Work/Project", "D:/Work/Project/image.png", false},
	} {
		if got := clientPathWithinRoot(tc.root, tc.candidate); got != tc.within {
			t.Errorf("clientPathWithinRoot(%q, %q) = %t", tc.root, tc.candidate, got)
		}
	}
}

func TestImageFileRestoresPaneAndFollowsRename(t *testing.T) {
	app, ws := newRealShellApp(t)
	path := writeImageFixture(t, ws.RootPath, "before.png")
	openImageFromTree(t, app, path)
	renamed := filepath.Join(ws.RootPath, "after.png")
	if err := app.relocateTreePath(path, renamed); err != nil {
		t.Fatal(err)
	}
	if tab := app.Win.ActiveTab(); !isImageTab(tab) || tab.Resource.ID != renamed || tab.DisplayTitle != "after.png" {
		t.Fatalf("rename did not retarget image tab: %+v", tab)
	}
	memory := app.captureViewMemory()
	if len(memory.Panes) != 1 || memory.Panes[0].Active != renamed {
		t.Fatalf("image missing from remembered panes: %+v", memory.Panes)
	}
	restored := newTestApp(t)
	restored.ws = &diagnosticFixtureWorkspace{Workspace: restored.ws, service: restored.ws.LSPMgr(), root: ws.RootPath}
	restored.restoreViewPanes(memory)
	commands := restored.editorCommands
	restored.editorCommands = nil
	for _, command := range commands {
		restored.applyPaneFilesOpened(command().(paneFilesOpenedMsg))
	}
	if tab := restored.Win.ActiveTab(); !isImageTab(tab) || tab.Resource.ID != renamed || restored.imageView(tab.ID) == nil {
		t.Fatalf("image tab was not restored: %+v", tab)
	}
}

func TestImageFileCorruptDataNeverOpensAnEditor(t *testing.T) {
	app, ws := newRealShellApp(t)
	path := filepath.Join(ws.RootPath, "broken.png")
	if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	app.Input.SetInput("keep this draft")
	openImageFromTree(t, app, path)
	if app.Win.TabByTitle(imageTabTitle(path)) != nil || app.EditorState.Tabs[path] != nil ||
		app.BufferMgr.Get(path) != nil || app.Input.Value() != "keep this draft" {
		t.Fatal("invalid image opened as a tab or changed the composer")
	}
}

// Opt-in real terminal journey for scripts/tuishot.sh: F2 opens the file through
// the tree's normal selection message; F9 verifies the mounted viewer and exits.
func TestImageFileVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_IMAGE_FILE") != "1" {
		t.Skip("requires the repository terminal capture workflow")
	}
	app, ws := newRealShellApp(t)
	path := writeImageFixture(t, ws.RootPath, "landscape.png")
	model := &imageFileVisualModel{App: app, t: t, path: path}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	restore := installDetachedMessageSinkForTest(app, program.Send)
	defer restore()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type imageFileVisualModel struct {
	*App
	t    *testing.T
	path string
}

func (m *imageFileVisualModel) Init() tea.Cmd {
	// newRealShellApp's synthetic size event starts a probe before a terminal
	// writer exists. Probe again through this real Program's writer.
	return tea.Batch(tickCmd(), m.probeTerminalImages())
}

func (m *imageFileVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			_, cmd := m.App.Update(filetree.FileSelectedMsg{Path: m.path})
			return m, cmd
		case tea.KeyF9:
			if !isImageTab(m.Win.ActiveTab()) || m.activeImageView() == nil {
				m.t.Error("the file image viewer was not mounted")
			}
			if m.focusedPopup() != nil {
				m.t.Error("a modal still covers the image viewer")
			}
			if os.Getenv("CRUDE_VISUAL_EXPECT_GRAPHICS") == "1" && len(m.View().Images) == 0 {
				m.t.Errorf("Kitty graphics were not negotiated: %+v", m.terminalImages)
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
