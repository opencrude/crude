package viewmodel

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/widget"
	"crude/tui/widgets"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/workspace"
)

var OnHistorySave func([]string)

type Mode = workspace.Mode

const (
	ModeChat        = workspace.ModeChat
	ModeConfirm     = workspace.ModeConfirm
	ModeQuestion    = workspace.ModeQuestion
	ModeYolo        = workspace.ModeYolo
	ModeTextOverlay = workspace.ModeTextOverlay
	ModeFilePicker  = workspace.ModeFilePicker
)

type Logger = workspace.Logger
type App struct {
	*workspace.Workspace

	// TUI-owned presentation state. Workspace retains only domain data and
	// services; these controls and render projections are owned by the host.
	Theme               *theme.Theme
	Input               input.Model
	StatusBar           *widgets.StatusBar
	Win                 *window.Window
	Overlay             *window.Window
	BufferMgr           *editor.BufferManager
	Tree                *TreeState
	FindReplace         *widgets.FindReplaceState
	LastContextMenu     *widgets.ContextMenuState
	ContextMenuOpenedAt time.Time
	TreePrompt          *widgets.TextPromptState
	TreeDeletePending   string
	TreeDeleteChoice    int
	FilePicker          widgets.FilePicker
	DiagPopupID         string
	DirtyPromptPath     string
	DirtyPromptChoice   int
	ChatAreaHeight      int
	LastInteractionTime time.Time
	SpinFrame           int
	History             []string
	LastSettings        ttypes.Settings
	FormHint            string

	// Display state (owned by viewmodel — tracks what's visible/active)
	EditorState  *EditorState
	OverlayState *OverlayState
	LSPPopup     *LSPPopupState

	eventBus *workspace.EventBus
	width    int
	height   int

	// Compositing engine
	compositor        *compositor.Compositor
	windowManager     *wm.WindowManager
	renderCtx         *widget.RenderContext
	cachedOutput      string // last rendered ANSI output for View() throttling during drag
	contentDirty      bool   // true when the main view (editor/chat/launcher) needs re-rendering — separate from chrome (tree/tabs/popups) so hover changes don't trigger expensive chroma/glamour re-renders
	mainContentCached string // last rendered main-view ANSI string — reused when only chrome changed (hover on tree, tabs, popups, etc.)

	// Layer surfaces for differential rendering. Cheap layers (sidebar,
	// header, status bar) are always re-rendered every frame. The main
	// content surface uses contentDirty to skip expensive chroma/glamour
	// when only the editor content changed.
	mainContentSurf     *surface.Surface // editor/chat/launcher content
	mainSidebarSurf     *surface.Surface // file tree sidebar + divider
	mainStatusbarSurf   *surface.Surface // status bar separator + content
	mainHeaderSurf      *surface.Surface // header line + separator
	mainSessionDeckSurf *surface.Surface // native per-session dock below messages
	sessionDeckRect     ui.Rect

	actionRegistry     *ui.ActionRegistry
	overlayHost        *ui.OverlayHost
	subPaneProjections *subPaneProjectionRegistry
	sessionProjections *sessionProjectionRegistry

	// App-scoped fields (not on workspace.Workspace)
	launcher            *LauncherState
	commandPaletteModel *commandPaletteModel
	findInFiles         *widgets.FindInFiles
	findInFilesRequest  uint64
	findInFilesGen      int
	providerPresets     []ttypes.ProviderPreset

	OnQuit        func()
	OnExitMessage func()

	Quit           bool
	lastCancelTime time.Time
}

type (
	tickMsg               = ttypes.TickMsg
	errorMsg              = ttypes.ErrorMsg
	statusMsg             = ttypes.StatusMsg
	agentMsg              = ttypes.AgentMsg
	openSettingsMsg       = ttypes.OpenSettingsMsg
	fetchModelsMsg        = ttypes.FetchModelsMsg
	saveSettingsMsg       = ttypes.SaveSettingsMsg
	updateModelMsg        = ttypes.UpdateModelMsg
	selectionTickMsg      = ttypes.SelectionTickMsg
	settingsSaveResultMsg = ttypes.SettingsSaveResultMsg
)

func NewApp(ws *workspace.Workspace, presets []ttypes.ProviderPreset) *App {
	th := theme.DefaultDark()
	im := input.NewModel(th)
	sb := widgets.NewStatusBar(th)
	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) {
		if ws.Logger != nil {
			ws.Logger.Warn(tag, msg, pairs...)
		}
	})
	treeModel := filetree.NewFileTreeModel(ws.RootPath)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(th)
	tree := NewTreeState(treeModel, treeRenderer, th.Tree.Config.DefaultWidth)
	bufferMgr := editor.NewBufferManager()
	comp := compositor.NewCompositor(80, 24)
	wmgr := wm.NewWindowManager()
	rctx := widget.NewRenderContext(th)
	eb := workspace.NewEventBus()
	ws.SetEventBus(eb)
	ws.LSPMgr.SetRuntime(newEditorLSPRuntime())

	// Create persistent layer surfaces. The main content covers the whole
	// screen minus the header and status bar; sidebar, header, and status
	// bar are overlays that update independently.
	contentSurf := surface.NewSurface("main-content", 80, 24, cell.TransparentBlank())
	sidebarSurf := surface.NewSurface("main-sidebar", th.Tree.Config.DefaultWidth, 1, cell.TransparentBlank())
	statusbarSurf := surface.NewSurface("main-statusbar", 80, 1, cell.TransparentBlank())
	headerSurf := surface.NewSurface("main-header", 80, 2, cell.TransparentBlank())
	deckSurf := surface.NewSurface("session-deck", 80, 1, cell.TransparentBlank())
	comp.Root().AddChild(contentSurf)
	comp.Root().AddChild(sidebarSurf)
	comp.Root().AddChild(statusbarSurf)
	comp.Root().AddChild(headerSurf)
	comp.Root().AddChild(deckSurf)
	contentSurf.SetZOrder(0)
	deckSurf.SetZOrder(1)
	sidebarSurf.SetZOrder(2)
	statusbarSurf.SetZOrder(3)
	headerSurf.SetZOrder(4)

	edState := NewEditorState()
	edState.Editor = editor.New(th)

	registry := ui.DefaultRegistry()
	im.SetActionRegistry(input.ActionRegistryAdapter{
		HintForFn:      func(id string) string { return registry.HintFor(ui.ActionID(id)) },
		HintLabelForFn: func(id string) string { return registry.HintLabelFor(ui.ActionID(id)) },
	})
	overlay := ui.NewOverlayHost(wmgr)
	subPaneProjections := newSubPaneProjectionRegistry(th)
	sessionProjections := newSessionProjectionRegistry(th)

	// Start on the unified welcome launcher (actions + workspaces + sessions).
	launcher := NewLauncherState(ws.RootPath, ws.ListSessions())

	app := &App{
		Workspace:           ws,
		Theme:               th,
		Input:               im,
		StatusBar:           sb,
		Win:                 window.New(th),
		Overlay:             window.New(th),
		BufferMgr:           bufferMgr,
		Tree:                tree,
		FilePicker:          widgets.NewFilePicker(th),
		LastSettings:        ws.InitialSettings,
		EditorState:         edState,
		OverlayState:        NewOverlayState(),
		LSPPopup:            NewLSPPopupState(),
		launcher:            launcher,
		providerPresets:     presets,
		compositor:          comp,
		windowManager:       wmgr,
		eventBus:            eb,
		renderCtx:           rctx,
		lastCancelTime:      time.Now(),
		mainContentSurf:     contentSurf,
		mainSidebarSurf:     sidebarSurf,
		mainStatusbarSurf:   statusbarSurf,
		mainHeaderSurf:      headerSurf,
		mainSessionDeckSurf: deckSurf,
		actionRegistry:      registry,
		overlayHost:         overlay,
		subPaneProjections:  subPaneProjections,
		sessionProjections:  sessionProjections,
	}

	app.registerOverlayHandlers()

	return app
}

// SwitchProject performs the workspace domain switch, then rebuilds the TUI
// projections that are rooted in the old project directory. Workspace does
// not construct file-tree renderers or editor controls.
func (m *App) SwitchProject(projectDir string) error {
	if err := m.Workspace.SwitchProject(projectDir); err != nil {
		return err
	}
	treeModel := filetree.NewFileTreeModel(m.RootPath)
	treeRenderer := filetree.NewTreeRenderer(treeModel, treeModel.IconResolver())
	treeRenderer.ApplyTheme(m.Theme)
	m.Tree = NewTreeState(treeModel, treeRenderer, m.Theme.Tree.Config.DefaultWidth)
	m.BufferMgr = editor.NewBufferManager()
	m.EditorState = NewEditorState()
	m.EditorState.Editor = editor.New(m.Theme)
	m.LSPMgr.SetRuntime(newEditorLSPRuntime())
	m.launcher = NewLauncherState(m.RootPath, m.ListSessions())
	return nil
}

func (m *App) SetHistory(history []string) {
	m.History = history
	m.Input.SetHistory(history)
}

func modeName(m Mode) string {
	return workspace.ModeName(m)
}

func (m *App) SetReferences(refs []sessiondb.Reference) {
	if m.RefStore() == nil {
		return
	}
	for _, ref := range refs {
		m.RefStore().AddExisting(ref)
	}
	m.Input.SetReferenceStore(m.RefStore())
}

// popLongestLineWidth returns the display width of the widest
// line in the slice. Used to size confirm-style popups.
func popLongestLineWidth(lines []string) int {
	max := 0
	for _, l := range lines {
		w := rendering.VisibleWidth(l)
		if w > max {
			max = w
		}
	}
	return max
}

func (m *App) SetProgram(p *tea.Program) {
	if m.eventBus != nil {
		m.eventBus.SetProgram(p)
	}
}

func (m *App) Init() tea.Cmd {
	if m.OverlayState.IsChat() {
		m.startLSPForWorkspace()
	}
	// Wire the permanent diagnostics callback so project-wide diagnostics
	// (which arrive as soon as the LSP server connects) always update
	// the file tree, even before any file is opened in the editor.
	m.LSPMgr.SetOnDiagnostics(func(fp string, diags []editor.LSPDiagnostic) {
		if m.eventBus != nil {
			m.eventBus.Send(workspace.LSPDiagnosticsMsg{FilePath: fp, Diagnostics: diags})
		}
	})

	// Hook file tree watcher events into the LSP server so it learns about
	// disk changes (git checkout, external edits, etc.).
	if m.Tree.Model != nil {
		m.Tree.Model.SetOnFileChanged(func(filePath string, changeType int) {
			if m.eventBus != nil {
				m.eventBus.Send(workspace.FileWatcherMsg{FilePath: filePath, ChangeType: changeType})
			}
		})
		if err := m.Tree.Model.StartWatcher(); err != nil {
			if m.Logger != nil {
				m.Logger.Warn("FILETREE", "watcher_start_failed", "error", err.Error())
			}
		}
	}

	return tea.Batch(tickCmd())
}

// openTextOverlayWindow opens the text overlay as a floating window.
func (m *App) openTextOverlayWindow() {
	m.overlayHost.Close(ui.OverlayTextOverlay)
	p := m.activeSessionProjection()
	if p == nil {
		return
	}

	textBody := ui.NewANSIViewFunc(ui.NewID(ui.RootID, "textoverlay-body"), func(w, h int) string {
		lines := p.textOverlay.ViewInner(w, h, m.SpinFrame)
		return strings.Join(lines, "\n")
	})
	textWidget := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "textoverlay-frame"),
		ui.FrameWindow,
		"Edit",
		textBody,
		nil,
		true,
		72,
		30,
	)

	win := wm.NewFloatingWindow(string(ui.OverlayTextOverlay), "Edit", textWidget,
		72, 30,
		wm.WithMinSize(32, 8),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayTextOverlay, win, ui.DefaultOverlayPolicy(), func() {
		p.textOverlay.Close()
		m.setMode(ModeChat)
	})
}

// openFilePickerWindow opens the file picker as a floating window.
// WindowFrame owns the file picker's visual chrome; WM owns its interactions.
func (m *App) openFilePickerWindow() {
	m.overlayHost.Close(ui.OverlayFilePicker)

	w, h := fitFloatingSize(m.width, m.height, 68, 20, 42, 12)
	title := "Attach File"
	if m.FilePicker.Mode() == widgets.FilePickerModeFolder {
		title = "New Workspace"
	}
	body := ui.NewLegacyWidget(ui.NewID(ui.RootID, "filepicker-body"), &m.FilePicker)
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "filepicker-frame"),
		ui.FrameDialog,
		title,
		body,
		&m.FilePicker,
		true,
		w,
		h,
	)
	win := wm.NewFloatingWindow(string(ui.OverlayFilePicker), title, frame,
		w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithClosable(true),
		wm.WithResizable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(minInt(42, w), minInt(12, h)),
		wm.WithMaxSize(w, h),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayFilePicker, win, ui.ModalDialogPolicy(), func() {
		m.FilePicker.Close()
		m.setMode(ModeChat)
	})
}

// openWorkspaceSettings opens the JetBrains-style workspace settings as a floating window.
func (m *App) openWorkspaceSettings() {
	m.overlayHost.Close(ui.OverlayWorkspaceSettings)

	appSets := m.LoadAppSettings()
	if len(appSets.Agents.Categories) == 0 {
		appSets.Agents = m.WorkspaceAgentDefaults()
	}
	if providers := m.AllProviderConfigs(); len(providers) > 0 {
		appSets.Providers = providers
	}
	if appSets.ActiveProvider == "" {
		appSets.ActiveProvider = m.LastSettings.ProviderID
	}
	if appSets.ActiveProvider == "" {
		appSets.ActiveProvider = "openrouter"
	}
	if pc := m.ProviderConfig(appSets.ActiveProvider); (pc.APIKey != "" || pc.ProviderURL != "") && appSets.Providers != nil {
		appSets.Providers[appSets.ActiveProvider] = pc
	}

	wsWidget := widgets.NewWorkspaceSettings(m.Theme, appSets, m.providerPresets)

	w, h := fitFloatingSize(m.width, m.height, 86, 22, 60, 18)
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "workspace-settings-frame"),
		ui.FrameWindow,
		"Settings",
		wsWidget.Component(),
		wsWidget,
		true,
		w,
		h,
	)
	wsWidget.SetFocusHandler(frame.Focus)
	wsWidget.FocusSearch()
	win := wm.NewFloatingWindow(string(ui.OverlayWorkspaceSettings), "Settings", frame,
		w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithResizable(true),
		wm.WithClosable(true),
		wm.WithFocusable(true),
		wm.WithModal(true),
		wm.WithMinSize(minInt(60, w), minInt(18, h)),
		wm.WithMaxSize(maxInt(w, m.width-2), maxInt(h, m.height-2)),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayWorkspaceSettings, win, ui.ModalDialogPolicy(), nil)
}

func fitFloatingSize(screenW, screenH, preferredW, preferredH, minW, minH int) (int, int) {
	maxW := screenW - 4
	maxH := screenH - 4
	if maxW < 10 {
		maxW = screenW
	}
	if maxH < 6 {
		maxH = screenH
	}
	w, h := preferredW, preferredH
	if w > maxW {
		w = maxW
	}
	if h > maxH {
		h = maxH
	}
	if w < minW && maxW >= minW {
		w = minW
	}
	if h < minH && maxH >= minH {
		h = minH
	}
	return w, h
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// handleQuit implements hierarchical quit: close tab first, quit if no tabs.
func (m *App) handleQuit() (tea.Model, tea.Cmd) {
	// First, close any focused floating window
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			m.overlayHost.Close(ui.OverlayID(fw.ID))
			return m, nil
		}
	}
	if projection := m.activeSessionDeck(); projection != nil && projection.deck.IsOpen() {
		if panel := projection.deck.Active(); panel != nil && panel.ID == sessionSettingsPanelID && projection.settings != nil {
			projection.settings.RestoreSnapshot()
		}
		projection.deck.CloseActive()
		projection.deckHost.MarkLayout()
		projection.deckFocused = projection.deck.IsOpen()
		return m, nil
	}

	// If there's an active overlay tab, close it first
	if m.Overlay.IsOpen() {
		m.Overlay.CloseAll()
		if m.OverlayState.IsOverlay() {
			m.OverlayState.Set(ModeChat)
			m.Input.ResetPrompt()
			m.Input.ClearInput()
		}
		return m, nil
	}

	// If there's an editor open, close it
	if m.EditorState.ActiveView != "" {
		m.closeEditorTab()
		return m, nil
	}

	// If there are window tabs, close the active one
	if m.Win.IsOpen() && m.Win.TabCount() > 0 {
		activeIdx := m.Win.ActiveIndex()
		if activeIdx >= 0 {
			tab := m.Win.Tabs()[activeIdx]
			action := m.Win.CloseTab(activeIdx)
			if action == window.CloseNone {
				return m, nil
			}
			// If the tab was an agent tab, clean up
			if m.isAgentTabTitle(tab.Title) {
				agentID := m.agentIDFromTabTitle(tab.Title)
				m.cleanupAgent(agentID)
			}
			// If no tabs left, show welcome launcher
			if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && !m.Win.IsOpen() {
				m.openLauncher()
				return m, nil
			}
		}
	}

	// No tabs left and no sessions — quit
	m.Quit = true
	return m, tea.Quit
}

// toggleComposer opens or closes the composer floating window.
func (m *App) toggleComposer() (tea.Model, tea.Cmd) {
	if m.overlayHost.IsOpen(ui.OverlayComposer) {
		m.overlayHost.Close(ui.OverlayComposer)
		return m, nil
	}

	p := m.activeSessionProjection()
	if p == nil {
		return m, nil
	}
	p.composer.Open("")
	body := ui.NewANSIViewFunc(ui.NewID(ui.RootID, "composer-body"), func(w, h int) string {
		return p.composer.View(w, h)
	})
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "composer-frame"),
		ui.FrameWindow,
		"Composer",
		body,
		nil,
		true,
		72,
		24,
	)

	fw := wm.NewFloatingWindow(string(ui.OverlayComposer), "Composer", frame,
		72, 24,
		wm.WithMinSize(40, 10),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayComposer, fw, ui.DefaultOverlayPolicy(), func() {
		p.composer.Close()
	})
	return m, nil
}

// openOrchestratorSettings opens the settings to the current orchestrator's
// category settings, allowing per-orchestrator model/provider overrides.
func (m *App) openSessionSettings() {
	p := m.activeSessionProjection()
	if p == nil || p.settings == nil {
		return
	}
	p.settings.ConfigureProviders(m.providerPresets, m.AllProviderConfigs())
	p.settings.SaveSnapshot()
	m.openSessionSettingsPanel()
}

func (m *App) openOrchestratorSettings() {
	m.openSessionSettings()
}

// resetOrchestratorOverrides clears all runtime overrides for the current orchestrator.
func (m *App) resetOrchestratorOverrides() {
	id := m.AgentMgr.ActiveID()
	if id == "" {
		id = m.EditorState.ActiveView
		if id == "" {
			m.StatusBar.SetInfo("No active orchestrator to reset")
			return
		}
	}
	m.ResetOrchestratorOverrides(id)
	m.refreshSessionSettingsProjection(m.ActiveSession)
	m.StatusBar.SetInfo("Session overrides reset to workspace defaults")
}

// registerOverlayHandlers registers per-overlay content-click and hover
// handlers on the OverlayHost. Each handler delegates to the existing App
// method for that popup type, replacing the bespoke switch statements in
// popupmouse.go.
func (m *App) registerOverlayHandlers() {
	if m.overlayHost == nil {
		return
	}
	oh := m.overlayHost

	oh.RegisterContentHandler(ui.OverlayContextMenu, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickContextMenu(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayContextMenu, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverContextMenu(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayFindReplace, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickFindReplace(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayFindReplace, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverFindReplace(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayDirtyClose, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickDirtyPrompt(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayDirtyClose, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverDirtyPrompt(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayTreePrompt, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickTreePrompt(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayTreeDelete, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickTreeDeleteConfirm(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayTreeDelete, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverDirtyPrompt(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayFilePicker, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.forwardEventToWindowContent(fw, msg)
	})
	oh.RegisterHoverHandler(ui.OverlayFilePicker, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})
	oh.RegisterHoverHandler(ui.OverlayWorkspaceSettings, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})
	oh.RegisterHoverHandler(ui.OverlayFindInFiles, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})

	oh.RegisterContentHandler(ui.OverlayReferences, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickReferences(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayReferences, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverReferences(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayCompletion, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickCompletion(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayCompletion, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverCompletion(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayOutline, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickOutline(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayOutline, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverOutline(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayCommandPalette, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickCommandPalette(fw, localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayCommandPalette, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverCommandPalette(fw, localX, localY)
	})
}
