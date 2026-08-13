package viewmodel

import (
	"testing"
	"time"

	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shellstatus"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/screens/termdock"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/workspace"
)

// newTestApp builds an App with the full shell tree mounted (all shell
// screens under one rootHost painted into mainSurf) plus the workspace
// stubs. No compositor/renderCtx are created — tests that call View()
// should use newFullTestApp instead.
func newTestApp(t *testing.T) *App {
	t.Helper()
	th := theme.DefaultDark()
	wmgr := wm.NewWindowManager()
	im := input.NewModel(th)
	sb := shellstatus.New(th)

	headerScreen := shellheader.New(th)
	tabScreen := shelltabs.New(th)
	inputScreen := shellinput.New(th)
	chatScreen := shellchat.New(th)
	editorScreen := shelleditor.New(th)
	sidebarScreen := shelltree.New(th, nil)
	activityBar := activitybar.New(th)
	activityList := activitylist.New(th)
	sourceControl := sourcecontrol.New(th)
	launcherScreen := launcher.New(th)
	launcherScreen.SetData(".", nil)
	termDock := termdock.New(th)

	deck := newDeckMount(nil)
	shell := newShellRoot(headerScreen, sb, tabScreen, activityBar, sidebarScreen, chatScreen, inputScreen, editorScreen, launcherScreen, deck, termDock)
	rootHost := ui.NewHost(th, shell)

	app := &App{
		Workspace: &workspace.Workspace{
			AgentMgr:      workspace.NewAgentManager(),
			PaneMgr:       workspace.NewPaneManager(),
			Git:           workspace.NewGitService("."),
			TermMgr:       workspace.NewTerminalManager("."),
			FocusStack:    workspace.NewFocusStack(),
			StartTime:     time.Now(),
			ActiveSession: &workspace.Session{},
			LSPMgr:        workspace.NewLSPManager(nil),
		},
		Theme:              th,
		Input:              im,
		StatusBar:          sb,
		Tree:               NewTreeState(nil, 30),
		SpinFrame:          0,
		OverlayState:       NewOverlayState(),
		LSPPopup:           NewLSPPopupState(),
		EditorState:        NewEditorState(),
		actionRegistry:     ui.DefaultRegistry(),
		windowManager:      wmgr,
		overlayHost:        ui.NewOverlayHost(wmgr),
		subPaneProjections: newSubPaneProjectionRegistry(th),
		sessionProjections: newSessionProjectionRegistry(th),
		headerScreen:       headerScreen,
		tabScreen:          tabScreen,
		sidebarScreen:      sidebarScreen,
		activityBar:        activityBar,
		activityList:       activityList,
		sourceControl:      sourceControl,
		termScreens:        make(map[string]*shellterm.Screen),
		termDock:           termDock,
		termDockHeight:     12,
		inputScreen:        inputScreen,
		chatScreen:         chatScreen,
		editorScreen:       editorScreen,
		launcherScreen:     launcherScreen,
		shell:              shell,
		rootHost:           rootHost,
		mainSurf:           surface.NewSurface("main-shell", 80, 24, cell.TransparentBlank()),
		width:              80,
		height:             24,
	}
	deck.app = app
	app.Win = window.New(th)
	launcherScreen.SetFocusHandler(rootHost.Focus)
	sourceControl.SetFocusHandler(rootHost.Focus)
	return app
}

// newFullTestApp is newTestApp plus a compositor/renderCtx so View() works.
func newFullTestApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.compositor = compositor.NewCompositor(app.width, app.height)
	app.renderCtx = wm.NewRenderContext(app.Theme)
	return app
}

// newEditorPaintApp builds a compositor-backed App with a real file tree so
// the DocumentEditor shell can be painted on the first View().
func newEditorPaintApp(t *testing.T, root string) *App {
	t.Helper()
	th := theme.DefaultDark()
	w, h := 100, 30
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width = w
	app.height = h
	app.Theme = th
	app.BufferMgr = editor.NewBufferManager()

	treeModel := filetree.NewFileTreeModel(root)
	app.Tree = NewTreeState(treeModel, 24)
	app.Tree.Visible = true
	app.sidebarScreen.SetModel(treeModel)

	app.ChatAreaHeight = h - 6
	app.EditorState = NewEditorState()
	app.EditorState.Editor = editor.New(th)
	return app
}

// paintShellFrame runs the full per-frame shell configuration and paint for
// tests: geometry, slots, projections, and the root-host render into mainSurf.
func (m *App) paintShellFrame() {
	m.configureShell()
	m.renderShellToSurface()
	m.syncRootFocus()
}

// shellSlotActive reports whether the slot is currently mounted in the tree.
func (m *App) shellSlotActive(id shellSlotID) bool {
	if m.shell == nil {
		return false
	}
	return m.shell.slots[id].active
}

// rootHandleFocus sets the root-host focus and dispatches one message,
// returning the EventResult (mirrors dispatchKeyPress's per-target routing).
func (m *App) rootHandleFocus(focus ui.ComponentID, msg any) ui.EventResult {
	if m.rootHost == nil {
		return ui.EventResult{}
	}
	m.rootHost.Focus(focus)
	return m.rootHost.Handle(msg)
}
