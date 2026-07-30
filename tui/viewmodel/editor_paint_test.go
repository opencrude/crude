package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellstatus"
	"crude/tui/screens/shelltree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/widget"
	"crude/tui/wm"
)

// newEditorPaintApp builds a compositor-backed App that can paint the
// DocumentEditor shell on the first View() after openEditorTab.
func newEditorPaintApp(t *testing.T, root string) *App {
	t.Helper()
	th := theme.DefaultDark()
	w, h := 100, 30
	wmgr := wm.NewWindowManager()
	comp := compositor.NewCompositor(w, h)

	treeModel := filetree.NewFileTreeModel(root)
	tree := NewTreeState(treeModel, 24)
	tree.Visible = true

	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width = w
	app.height = h
	app.Theme = th
	app.compositor = comp
	app.windowManager = wmgr
	app.overlayHost = ui.NewOverlayHost(wmgr)
	app.renderCtx = widget.NewRenderContext(th)
	app.Tree = tree
	app.BufferMgr = editor.NewBufferManager()

	app.headerScreen = shellheader.New(th)
	app.headerHost = ui.NewHost(th, app.headerScreen)
	app.StatusBar = shellstatus.New(th)
	app.statusHost = ui.NewHost(th, app.StatusBar)
	app.sidebarScreen = shelltree.New(th, treeModel)
	app.sidebarHost = ui.NewHost(th, app.sidebarScreen)
	app.launcherScreen = launcher.New(th)
	app.launcherScreen.SetData(root, nil)
	app.launcherHost = ui.NewHost(th, app.launcherScreen)
	app.editorScreen = shelleditor.New(th)
	app.editorHost = ui.NewHost(th, app.editorScreen)

	app.mainHeaderSurf = surface.NewSurface("main-header", w, 2, cell.TransparentBlank())
	app.mainStatusbarSurf = surface.NewSurface("main-statusbar", w, 2, cell.TransparentBlank())
	app.mainSidebarSurf = surface.NewSurface("main-sidebar", tree.Width+1, h-4, cell.TransparentBlank())
	app.mainLauncherSurf = surface.NewSurface("main-launcher", w-tree.Width-1, h-4, cell.TransparentBlank())
	app.mainContentSurf = surface.NewSurface("main-content", w, h, cell.TransparentBlank())
	app.mainEditorSurf = surface.NewSurface("main-editor", w, h, cell.TransparentBlank())
	app.mainChatSurf = surface.NewSurface("main-chat", w, h, cell.TransparentBlank())
	app.mainInputSurf = surface.NewSurface("main-input", w, 4, cell.TransparentBlank())
	app.mainTabBarSurf = surface.NewSurface("main-tabbar", w, 2, cell.TransparentBlank())
	app.mainSessionDeckSurf = surface.NewSurface("session-deck", w, 1, cell.TransparentBlank())
	app.ChatAreaHeight = h - 6
	app.EditorState = NewEditorState()
	app.EditorState.Editor = editor.New(th)
	return app
}

func countNonBlankGlyphs(buf *surface.CellBuffer) int {
	if buf == nil {
		return 0
	}
	n := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 && c.Char != ' ' {
				n++
			}
		}
	}
	return n
}

func bufferContainsPlain(buf *surface.CellBuffer, needle string) bool {
	if buf == nil || needle == "" {
		return false
	}
	for y := 0; y < buf.Height(); y++ {
		var b strings.Builder
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			if c.Width > 0 && c.Char != 0 {
				b.WriteRune(c.Char)
			}
		}
		if strings.Contains(b.String(), needle) {
			return true
		}
	}
	return false
}

// TestOpenFile_FirstFramePaintsEditorGlyphs asserts the very first View()
// after opening a file paints real editor content — no blink / second frame.
func TestOpenFile_FirstFramePaintsEditorGlyphs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unique_marker_alpha.go")
	body := "package unique_marker_alpha\n\nfunc AlphaMarker() {}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newEditorPaintApp(t, dir)

	// Seed a launcher-style main-content cache so the pre-fix path would
	// take useCached=true and leave editorRect empty (the black-frame bug).
	app.EditorState.ActiveView = ""
	_ = app.View()
	if app.mainContentCached == "" {
		t.Fatal("setup: expected launcher main content cache")
	}
	app.contentDirty = false
	app.editorRect = ui.Rect{}

	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	// Do NOT run blink / second frame — only the immediate View after open.
	_ = app.View()

	if app.editorRect.Empty() {
		t.Fatal("first frame: editorRect must be laid out")
	}
	if !app.mainEditorSurf.Visible() {
		t.Fatal("first frame: mainEditorSurf must be visible")
	}
	buf := app.mainEditorSurf.Buffer()
	glyphs := countNonBlankGlyphs(buf)
	if glyphs < 10 {
		t.Fatalf("first frame: expected editor glyphs, got %d non-blank cells", glyphs)
	}
	if !bufferContainsPlain(buf, "unique_marker_alpha") && !bufferContainsPlain(buf, "AlphaMarker") {
		t.Fatal("first frame: editor surface missing opened file content")
	}
}

// TestOpenFile_DefensiveGuardPaintsWithoutContentDirty verifies View() still
// paints when contentDirty was forgotten, as long as editorRect is empty /
// cache kind mismatches.
func TestOpenFile_DefensiveGuardPaintsWithoutContentDirty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guard_file.go")
	if err := os.WriteFile(path, []byte("package guard_file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, dir)
	_ = app.View() // launcher cache
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	// Simulate the bug: dirty mark lost, rect unset, stale launcher cache.
	app.contentDirty = false
	app.editorRect = ui.Rect{}
	app.mainContentCacheKind = mainViewLauncher
	app.mainContentCacheEditorPath = ""

	_ = app.View()
	if countNonBlankGlyphs(app.mainEditorSurf.Buffer()) < 5 {
		t.Fatal("defensive guard: first View must still paint editor glyphs")
	}
	if !bufferContainsPlain(app.mainEditorSurf.Buffer(), "guard_file") {
		t.Fatal("defensive guard: missing guard_file content")
	}
}

// TestEditorTabSwitch_FirstFramePaintsNewFile asserts switching tabs paints
// the newly selected file immediately — not the previous file until blink.
func TestEditorTabSwitch_FirstFramePaintsNewFile(t *testing.T) {
	dir := t.TempDir()
	pathA := filepath.Join(dir, "file_aaa.go")
	pathB := filepath.Join(dir, "file_bbb.go")
	if err := os.WriteFile(pathA, []byte("package file_aaa\n\nfunc TripleA() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, []byte("package file_bbb\n\nfunc TripleB() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := newEditorPaintApp(t, dir)
	if _, err := app.openEditorTab(pathA); err != nil {
		t.Fatal(err)
	}
	_ = app.View()
	if !bufferContainsPlain(app.mainEditorSurf.Buffer(), "file_aaa") {
		t.Fatal("setup: tab A should paint file_aaa")
	}

	if _, err := app.openEditorTab(pathB); err != nil {
		t.Fatal(err)
	}
	// Immediate first frame after switch — no blink.
	_ = app.View()
	buf := app.mainEditorSurf.Buffer()
	if !bufferContainsPlain(buf, "file_bbb") && !bufferContainsPlain(buf, "TripleB") {
		t.Fatal("tab switch first frame must paint file B content")
	}
	if bufferContainsPlain(buf, "TripleA") {
		t.Fatal("tab switch first frame must not keep file A content")
	}

	// Switch back to A via tab bar / handleTabSwitch.
	if tab := app.Win.TabByTitle(app.editorTabTitle(pathA)); tab == nil {
		t.Fatal("missing tab A")
	} else {
		app.Win.SwitchTo(app.Win.IndexOf(tab.ID))
		app.handleTabSwitch()
	}
	_ = app.View()
	buf = app.mainEditorSurf.Buffer()
	if !bufferContainsPlain(buf, "file_aaa") && !bufferContainsPlain(buf, "TripleA") {
		t.Fatal("switch back first frame must paint file A")
	}
	if bufferContainsPlain(buf, "TripleB") {
		t.Fatal("switch back first frame must not keep file B content")
	}
}
