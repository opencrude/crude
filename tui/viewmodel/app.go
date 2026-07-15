package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/surface"
	ttypes "crude/tui/types"
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
	ModeSettings    = workspace.ModeSettings
	ModeConfirm     = workspace.ModeConfirm
	ModeQuestion    = workspace.ModeQuestion
	ModeYolo        = workspace.ModeYolo
	ModeTextOverlay = workspace.ModeTextOverlay
	ModeFilePicker  = workspace.ModeFilePicker
)

type Logger = workspace.Logger
type App struct {
	*workspace.Workspace

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
	mainContentSurf   *surface.Surface // editor/chat/launcher content
	mainSidebarSurf   *surface.Surface // file tree sidebar + divider
	mainStatusbarSurf *surface.Surface // status bar separator + content
	mainHeaderSurf    *surface.Surface // header line + separator

	// App-scoped fields (not on workspace.Workspace)
	launcher            *LauncherState
	commandPaletteModel *commandPaletteModel
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
	comp := compositor.NewCompositor(80, 24)
	wmgr := wm.NewWindowManager()
	rctx := widget.NewRenderContext(ws.Theme)
	eb := workspace.NewEventBus()

	// Create persistent layer surfaces. The main content covers the whole
	// screen minus the header and status bar; sidebar, header, and status
	// bar are overlays that update independently.
	contentSurf := surface.NewSurface("main-content", 80, 24, cell.TransparentBlank())
	sidebarSurf := surface.NewSurface("main-sidebar", ws.Theme.Tree.Config.DefaultWidth, 1, cell.TransparentBlank())
	statusbarSurf := surface.NewSurface("main-statusbar", 80, 1, cell.TransparentBlank())
	headerSurf := surface.NewSurface("main-header", 80, 2, cell.TransparentBlank())
	comp.Root().AddChild(contentSurf)
	comp.Root().AddChild(sidebarSurf)
	comp.Root().AddChild(statusbarSurf)
	comp.Root().AddChild(headerSurf)
	contentSurf.SetZOrder(0)
	sidebarSurf.SetZOrder(1)
	statusbarSurf.SetZOrder(2)
	headerSurf.SetZOrder(3)

	edState := NewEditorState()
	edState.Editor = editor.New(ws.Theme)

	// Start on the unified welcome launcher (actions + workspaces + sessions).
	launcher := NewLauncherState(ws.RootPath, ws.ListSessions())

	return &App{
		Workspace:         ws,
		EditorState:       edState,
		OverlayState:      NewOverlayState(),
		LSPPopup:          NewLSPPopupState(),
		launcher:          launcher,
		providerPresets:   presets,
		compositor:        comp,
		windowManager:     wmgr,
		eventBus:          eb,
		renderCtx:         rctx,
		lastCancelTime:    time.Now(),
		mainContentSurf:   contentSurf,
		mainSidebarSurf:   sidebarSurf,
		mainStatusbarSurf: statusbarSurf,
		mainHeaderSurf:    headerSurf,
	}
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
	m.windowManager.RemoveWindow("textoverlay")

	textViewFn := func() string {
		// Use the floating window's content area dimensions, not the full
		// screen dimensions. ViewInner renders content directly (no
		// RenderFloatingBox wrapper) since the FloatingWindow already
		// provides its own border and title bar.
		w, h := m.width, m.height
		if fw := m.windowManager.Window("textoverlay"); fw != nil {
			b := fw.ContentBounds()
			w, h = b.W, b.H
		}
		lines := m.ActiveSession.TextOverlay.ViewInner(w, h, m.SpinFrame)
		// Join lines into a single string
		result := ""
		for i, l := range lines {
			if i > 0 {
				result += "\n"
			}
			result += l
		}
		return result
	}

	textWidget := rendering.NewANSIAdapter(textViewFn)

	win := wm.NewFloatingWindow("textoverlay", "Edit", textWidget,
		72, 30,
		wm.WithMinSize(32, 8),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.windowManager.AddWindow(win)
}

// openFilePickerWindow opens the file picker as a floating window.
// The file picker now draws its own border/title (unified chrome),
// so the FloatingWindow is undecorated.
func (m *App) openFilePickerWindow() {
	m.windowManager.RemoveWindow("filepicker")

	w, h := fitFloatingSize(m.width, m.height, 68, 20, 42, 12)
	title := "Attach File"
	if m.FilePicker.Mode() == widgets.FilePickerModeFolder {
		title = "New Workspace"
	}
	win := wm.NewFloatingWindow("filepicker", title, &m.FilePicker,
		w, h,
		wm.WithDecoration(true),
		wm.WithBorderStyle(wm.BorderNone),
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
	m.windowManager.AddWindow(win)
}

// openWorkspaceSettings opens the JetBrains-style workspace settings as a floating window.
func (m *App) openWorkspaceSettings() {
	m.windowManager.RemoveWindow("workspace-settings")

	appSets := m.LoadAppSettings()
	if len(appSets.Agents.Categories) == 0 {
		appSets.Agents = m.WorkspaceAgentDefaults()
	}
	// Prefer live keys from the session config map.
	if providers := m.AllProviderConfigs(); len(providers) > 0 {
		appSets.Providers = providers
	}
	if appSets.ActiveProvider == "" {
		appSets.ActiveProvider = m.LastSettings.ProviderID
	}
	if appSets.ActiveProvider == "" {
		// Fall back to whatever ProviderConfig resolves for openrouter.
		appSets.ActiveProvider = "openrouter"
	}
	if pc := m.ProviderConfig(appSets.ActiveProvider); (pc.APIKey != "" || pc.ProviderURL != "") && appSets.Providers != nil {
		appSets.Providers[appSets.ActiveProvider] = pc
	}

	wsWidget := widgets.NewWorkspaceSettings(m.Theme, appSets, m.providerPresets)

	w, h := fitFloatingSize(m.width, m.height, 86, 22, 60, 18)
	win := wm.NewFloatingWindow("workspace-settings", "Settings", wsWidget,
		w, h,
		wm.WithDecoration(true),
		wm.WithBorderStyle(wm.BorderNone),
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
	m.windowManager.AddWindow(win)
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
			m.windowManager.RemoveWindow(fw.ID)
			return m, nil
		}
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
	if m.windowManager.Window("composer") != nil {
		m.windowManager.RemoveWindow("composer")
		return m, nil
	}

	m.ActiveSession.Composer.Open("")
	viewFn := func() string {
		w, h := m.width, m.height
		if fw := m.windowManager.Window("composer"); fw != nil {
			b := fw.ContentBounds()
			w, h = b.W, b.H
		}
		return m.ActiveSession.Composer.View(w, h)
	}
	ansiWidget := rendering.NewANSIAdapter(viewFn)

	fw := wm.NewFloatingWindow("composer", "Composer", ansiWidget,
		72, 24,
		wm.WithMinSize(40, 10),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.windowManager.AddWindow(fw)
	return m, nil
}

// openOrchestratorSettings opens the settings to the current orchestrator's
// category settings, allowing per-orchestrator model/provider overrides.
func (m *App) openSessionSettings() {
	if m.SessionSettings == nil {
		return
	}
	m.SessionSettings.ConfigureProviders(m.providerPresets, m.AllProviderConfigs())
	m.SessionSettings.SaveSnapshot()
	m.setMode(ModeSettings)
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
	m.StatusBar.SetInfo("Session overrides reset to workspace defaults")
}
