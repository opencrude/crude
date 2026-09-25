package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/history"
	"crude/sessiondb"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/activitylist"
	filepickerscreen "crude/tui/screens/filepicker"
	findinfiles "crude/tui/screens/findinfiles"
	helpscreen "crude/tui/screens/help"
	"crude/tui/screens/launcher"
	historyscreen "crude/tui/screens/localhistory"
	memorybrowser "crude/tui/screens/memorybrowser"
	sessionpicker "crude/tui/screens/sessionpicker"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellfooter"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shellterm"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/screens/termdock"
	settingsui "crude/tui/settings"
	"crude/tui/surface"
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/ui/popup"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/workspace"
)

var OnHistorySave func([]string)

type Mode = workspace.Mode

const (
	ModeChat        = workspace.ModeChat
	ModeTextOverlay = workspace.ModeTextOverlay
	ModeFilePicker  = workspace.ModeFilePicker
)

type Logger = workspace.Logger
type App struct {
	*workspace.Workspace

	// TUI-owned presentation state. Workspace retains only domain data and
	// services; these controls and render projections are owned by the host.
	Theme                     *theme.Theme
	Input                     input.Model
	Feedback                  *feedbackSink
	workspacePanelLastVersion uint64
	workspacePanelLastSecond  int64
	workspaceSessions         workspaceSessionCache
	Win                       *window.Window
	BufferMgr                 *editor.BufferManager
	Tree                      *TreeState
	FindReplace               *popup.FindReplaceState
	LastContextMenu           *ui.ContextMenuState
	ContextMenuOpenedAt       time.Time
	TreePrompt                *ui.TextPromptState
	TreeDeletePending         string
	TreeDeleteChoice          int
	GitDiscardPending         *workspace.GitChange
	FilePicker                *filepickerscreen.Screen
	DiagPopupID               string

	// Composer draft persistence (hydration). The active-session pointer is
	// compared every frame; on change the composer clears immediately and the
	// incoming session's persisted draft loads asynchronously.
	composerHydratedFor    *workspace.Session
	composerSavedDraft     string
	composerSaving         bool
	composerDirtySinceSave bool
	// Failed BTW uploads belong to their session until its composer is active.
	failedBtwDrafts        map[*workspace.Session][]string
	DirtyPromptPath        string
	DirtyPromptChoice      int
	accountRecoveryChoice  int
	accountRecoverySession bool
	settingsPrompt         *settingsPromptState
	agentPrompt            *agentPromptState
	agentPromptQueue       []agentPromptRequest
	// pendingTabClose holds the tabs still to close after a bulk close
	// ("Close others" / "Close to the right") stopped at a dirty buffer and
	// opened the unsaved-changes prompt. Cancel drops it; a confirmed close
	// resumes the walk.
	pendingTabClose     []string
	ChatAreaHeight      int
	LastInteractionTime time.Time
	SpinFrame           int
	History             []string // recall slice mirrored from Prompts
	Prompts             *history.Store
	promptHistoryPath   string
	LastSettings        ttypes.Settings

	// Display state (owned by viewmodel — tracks what's visible/active)
	EditorState    *EditorState
	OverlayState   *OverlayState
	LSPPopup       *LSPPopupState
	editorSettings ttypes.EditorSettings
	editorSaves    map[string]*editorSaveState
	editorChanges  *editorChangesView

	eventBus *workspace.EventBus
	width    int
	height   int

	// Compositing engine
	compositor         *compositor.Compositor
	windowManager      *wm.WindowManager
	renderCtx          *wm.RenderContext
	cachedOutput       string // last ANSI frame reused while high-rate presentation is coalesced
	frameGate          frameGate
	chatReflowContentW int
	chatReflowInnerW   int

	// Root component tree (Phase 13.9): ONE ui.Host mounts the whole shell
	// (header/status/tabs/sidebar/chat/input/editor/launcher/deckMount/termDock)
	// and paints into ONE full-screen surface. Per-screen Hosts and layer
	// surfaces are deleted; the per-session PanelDeck Hosts survive inside
	// shellRoot's deckMount adapter. Terminals use a workspace termDock slot,
	// not the session PanelDeck.
	mainSurf *surface.Surface // full-screen shell surface (replaces all layer surfaces)
	shell    *shellRoot
	rootHost *ui.Host
	// Rectangles computed by computeContentLayout each frame; they drive
	// shell slot geometry AND viewmodel policy (mouse region classification
	// for editor/divider/status and focus-stack updates).
	sessionDeckRect ui.Rect
	inputRect       ui.Rect
	chatRect        ui.Rect
	editorRect      ui.Rect
	termDockRect    ui.Rect

	// frameOverlay holds unsaved shell-frame paddings from the live settings
	// preview (FramePreviewMsg). It sits on top of the persisted theme frame
	// so the inset tracks each keystroke; CancelMsg clears it back to the
	// saved values. Nil = no live override for that side.
	frameOverlay *ui.Insets

	actionRegistry     *ui.ActionRegistry
	overlayHost        *ui.OverlayHost
	subPaneProjections *subPaneProjectionRegistry
	sessionProjections *sessionProjectionRegistry

	// App-scoped fields (not on workspace.Workspace)
	headerScreen           *shellheader.Screen
	footerScreen           *shellfooter.Screen
	tabScreen              *shelltabs.Screen
	sidebarScreen          *shelltree.Screen
	activityBar            *activitybar.Screen
	activityList           *activitylist.Screen
	sourceControl          *sourcecontrol.Screen
	activityView           activitybar.View
	termScreens            map[string]*shellterm.Screen
	termDock               *termdock.Screen
	termDockHeight         int
	termDockFocused        bool
	suppressTermClose      string
	inputScreen            *shellinput.Screen
	chatScreen             *shellchat.Screen
	agentChatScroll        map[string]agentMainChatScrollSnapshot
	chatScrollAgent        string
	chatScrollNeedsRestore bool
	editorScreen           *shelleditor.Screen
	launcherScreen         *launcher.Screen
	historyScreen          *historyscreen.Screen
	sessionPicker          *sessionpicker.Screen
	helpScreen             *helpscreen.Screen
	commandPaletteModel    *commandPaletteModel
	findInFiles            *findinfiles.Screen
	findInFilesRequest     uint64
	findInFilesGen         int
	memoryBrowser          *memorybrowser.Screen
	providerPresets        []ttypes.ProviderPreset
	asyncIO                atomic.Pointer[appAsyncIOState]
	asyncIOStopped         atomic.Bool
	launcherLoadGen        atomic.Uint64 // generation of the in-flight async launcher load
	gitRefreshGen          atomic.Uint64
	editorGitGen           atomic.Uint64
	editorGitStale         atomic.Bool
	editorGitEditAt        time.Time // most recent text change awaiting projection refresh
	editorGitSince         time.Time // start of the current unrefreshed burst (hold cap)
	editorGitCancel        context.CancelFunc
	sourceControlTimeout   time.Duration
	treeGitRefreshAt       time.Time
	// Header Git reconciliation (git_snapshot.go): watcher events arm a
	// debounced, coalesced silent snapshot read so the header identity follows
	// the repository without the user opening the Source Control panel.
	gitSnapshotEventAt time.Time       // most recent unreconciled watcher event
	gitSnapshotBurstAt time.Time       // start of the current unreconciled burst
	gitSnapshotAt      time.Time       // when the last snapshot read settled
	gitReadsInFlight   int             // snapshot reads (explicit or silent) awaiting a result
	workspaceSwitchGen atomic.Uint64   // invalidates project-bound async callbacks
	lspRequestCtx      context.Context // canceled whenever the workspace generation changes
	lspRequestCancel   context.CancelFunc
	reveals            map[string]revealAnim // in-flight popup reveal animations (overlay ID → anim)

	OnQuit        func()
	OnExitMessage func()

	Quit           bool
	lastCancelTime time.Time
}

func NewApp(ws *workspace.Workspace, presets []ttypes.ProviderPreset) *App {
	th := theme.FromPreset(themePresetFromSettings(ws.LoadAppSettings()))
	applyWorkspaceFrame(&th, ws)
	im := input.NewModel(th)
	headerScreen := shellheader.New(th)
	footerScreen := shellfooter.New(th, ui.DefaultRegistry())
	tabScreen := shelltabs.New(th)
	inputScreen := shellinput.New(th)
	chatScreen := shellchat.New(th)
	editorScreen := shelleditor.New(th)
	rendering.SetInvalidUTF8Logger(func(tag, msg string, pairs ...any) {
		if ws.Logger != nil {
			ws.Logger.Warn(tag, msg, pairs...)
		}
	})
	treeModel := filetree.NewFileTreeModel(ws.RootPath)
	tree := NewTreeState(treeModel, th.Tree.Config.DefaultWidth)
	sidebarScreen := shelltree.New(th, treeModel)
	activityBar := activitybar.New(th)
	activityList := activitylist.New(th)
	sourceControl := sourcecontrol.New(th)
	termDock := termdock.New(th)
	bufferMgr := editor.NewBufferManager()
	comp := compositor.NewCompositor(80, 24)
	wmgr := wm.NewWindowManager()
	rctx := wm.NewRenderContext(th)
	eb := workspace.NewEventBus()
	ws.SetEventBus(eb)
	ws.LSPMgr.SetRuntime(newEditorLSPRuntime())

	// Start on the unified welcome launcher (actions + workspaces + sessions).
	// The workspace listing loads asynchronously (refreshLauncherAsync from
	// Init) so the first frame never blocks on the workspace DB scan.
	launcherScreen := launcher.New(th)
	launcherScreen.SetData(ws.RootPath, nil)

	// The root tree: one Host mounts the whole shell and paints into one
	// full-screen surface. The compositor renders this single surface under
	// the floating windows (see View()).
	deck := newDeckMount(nil)
	shell := newShellRoot(headerScreen, footerScreen, tabScreen, activityBar, sidebarScreen, chatScreen, inputScreen, editorScreen, launcherScreen, deck, termDock)
	rootHost := ui.NewHost(th, shell)
	launcherScreen.SetFocusHandler(rootHost.Focus)
	sourceControl.SetFocusHandler(rootHost.Focus)

	edState := NewEditorState()
	edState.Editor = editor.New(th)

	registry := ui.DefaultRegistry()
	im.SetActionRegistry(input.ActionRegistryAdapter{
		HintForFn:      func(id string) string { return registry.HintFor(ui.ActionID(id)) },
		HintLabelForFn: func(id string) string { return registry.HintLabelFor(ui.ActionID(id)) },
	})
	overlay := ui.NewOverlayHost(wmgr)
	// The overlay stack scopes the shell Host's focus: opening an overlay
	// captures the current shell focus and closing it restores the exact
	// prior focus path (Phase 5 focus scopes).
	overlay.SetFocusHost(rootHost)
	subPaneProjections := newSubPaneProjectionRegistry(th)
	sessionProjections := newSessionProjectionRegistry(th)

	mainSurf := surface.NewSurface("main-shell", 80, 24, cell.TransparentBlank())
	comp.Root().AddChild(mainSurf)

	app := &App{
		Workspace:          ws,
		Theme:              th,
		Input:              im,
		Win:                window.New(th),
		BufferMgr:          bufferMgr,
		Tree:               tree,
		FilePicker:         filepickerscreen.New(th),
		LastSettings:       ws.InitialSettings,
		EditorState:        edState,
		OverlayState:       NewOverlayState(),
		LSPPopup:           NewLSPPopupState(),
		editorSettings:     ws.LoadAppSettings().Editor,
		editorSaves:        make(map[string]*editorSaveState),
		headerScreen:       headerScreen,
		footerScreen:       footerScreen,
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
		mainSurf:           mainSurf,
		providerPresets:    presets,
		compositor:         comp,
		windowManager:      wmgr,
		eventBus:           eb,
		renderCtx:          rctx,
		lastCancelTime:     time.Now(),
		actionRegistry:     registry,
		overlayHost:        overlay,
		subPaneProjections: subPaneProjections,
		sessionProjections: sessionProjections,
		agentChatScroll:    make(map[string]agentMainChatScrollSnapshot),
	}
	deck.app = app
	app.Feedback = newFeedbackSink(app)
	app.initWorkspacePanels()
	if app.sidebarScreen != nil {
		app.sidebarScreen.SetPathSearchHandler(app.beginTreePathSearch)
	}

	app.registerOverlayHandlers()

	return app
}

// SwitchProject performs the workspace domain switch, then rebuilds the TUI
// projections that are rooted in the old project directory. Workspace does
// not construct file-tree renderers or editor controls.
func (m *App) SwitchProject(projectDir string) error {
	if sameWorkspaceRoot(m.RootPath, projectDir) {
		return nil
	}
	for _, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor != nil {
			if saving := m.editorSaves[tab.Editor.BufferID()]; saving != nil && saving.inFlight {
				return fmt.Errorf("file save is still in progress; retry the workspace switch after it finishes")
			}
			if tab.Editor.Modified() {
				m.showDirtyClosePrompt(tab.Path)
				return fmt.Errorf("save or discard changes in %s before switching workspaces", tab.Editor.BaseTitle())
			}
		}
	}
	state := m.beginWorkspaceSwitch()
	if err := m.Workspace.SwitchProject(projectDir); err != nil {
		state.restore(m)
		return err
	}
	// Workspace.SwitchProject has loaded the target workspace overrides. Apply
	// its effective palette before constructing any target-owned projections.
	m.applyThemePreset(themePresetFromSettings(m.LoadAppSettings()))
	m.finishWorkspaceSwitch(state)
	m.editorSaves = make(map[string]*editorSaveState)
	m.applyEditorSettings(m.LoadAppSettings().Editor)
	return nil
}

// SetPromptHistory installs the global prompt-history store and seeds
// composer up/down recall from it.
func (m *App) SetPromptHistory(store *history.Store) {
	if store == nil {
		store = history.New()
	}
	m.Prompts = store
	m.promptHistoryPath = history.DefaultPath()
	m.syncPromptRecall()
}

// recordPrompt folds a submitted prompt into the global history and returns a
// command that persists the store to disk off the UI thread. The in-memory
// fold is synchronous so ↑ recalls the prompt immediately; the file write is
// deferred. Merged repeats still save — they change count/lastSeen/text.
func (m *App) recordPrompt(text string) tea.Cmd {
	if m.Prompts == nil {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}
	m.Prompts.Record(text, time.Now())
	m.syncPromptRecall()
	path := m.promptHistoryPath
	return func() tea.Msg {
		if err := m.Prompts.Save(path); err != nil && m.Logger != nil {
			m.Logger.Warn("HISTORY", "save_failed", "error", err.Error())
		}
		return nil
	}
}

// syncPromptRecall refreshes the composer's recall slice from the store.
func (m *App) syncPromptRecall() {
	m.History = m.Prompts.Recall()
	m.Input.SetHistory(m.History)
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

func (m *App) SetProgram(p *tea.Program) {
	if m.eventBus != nil {
		m.eventBus.SetProgram(p)
	}
}

func (m *App) Init() tea.Cmd {
	// Wire diagnostics before starting workspace LSP so fast servers cannot
	// populate LSPManager's cache without notifying the tree/status update path.
	m.wireLSPDiagnostics()
	if m.OverlayState.IsChat() {
		m.startLSPForWorkspace()
	} else {
		m.syncFileTreeDiagnosticsFromLSP()
	}

	// Hook file tree watcher events into the LSP server so it learns about
	// disk changes (git checkout, external edits, etc.). Workspace switches
	// use this exact helper too, keeping startup and replacement in lockstep.
	if m.Tree != nil {
		m.wireAndStartWorkspaceTreeWatcher(m.Tree.Model)
	}

	// Kick the async launcher workspace listing for the welcome screen so
	// the first frame paints immediately (no workspace DB scan on boot).
	if m.launcherScreen != nil {
		m.refreshLauncherAsync(m.RootPath)
	}

	var statusCmd tea.Cmd
	if m.Workspace != nil && m.Status != nil {
		center := m.Status
		statusCmd = func() tea.Msg {
			if err := center.Hydrate(); err != nil {
				return statusHydrateErrMsg{Err: err}
			}
			return workspace.StatusCenterHydratedMsg{}
		}
	}
	return tea.Batch(tickCmd(), statusCmd, m.refreshSourceControl())
}

// openTextOverlayWindow opens the text overlay as a floating window.
func (m *App) openTextOverlayWindow() {
	m.overlayHost.Close(ui.OverlayTextOverlay)
	p := m.activeSessionProjection()
	if p == nil || p.textOverlay == nil {
		return
	}

	title := p.textOverlay.Title()
	if title == "" {
		title = "Edit"
	}
	w, h := 72, 30
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "textoverlay-frame"),
		ui.FrameWindow,
		title,
		p.textOverlay,
		nil,
		true,
		w,
		h,
	)
	p.textOverlay.SetFocusHandler(frame.Focus)
	p.textOverlay.FocusInput()

	win := wm.NewFloatingWindow(string(ui.OverlayTextOverlay), title, frame,
		w, h,
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

	folderMode := m.FilePicker.Mode() == filepickerscreen.ModeFolder
	w, h := fitFloatingSize(m.width, m.height, 96, 28, 60, 18)
	title := "Attach Files"
	if folderMode {
		title = "New Workspace"
	}
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "filepicker-frame"),
		ui.FrameWindow,
		title,
		m.FilePicker,
		nil,
		true,
		w,
		h,
	)
	m.FilePicker.SetFocusHandler(frame.Focus)
	m.FilePicker.FocusFilter()
	win := wm.NewFloatingWindow(string(ui.OverlayFilePicker), title, frame,
		w, h,
		wm.WithDecoration(true),
		wm.WithMovable(true),
		wm.WithClosable(true),
		wm.WithResizable(true),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(minInt(60, w), minInt(14, h)),
		wm.WithMaxSize(maxInt(w, m.width-2), maxInt(h, m.height-2)),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.Open(ui.OverlayFilePicker, win, ui.ModalDialogPolicy(), func() {
		m.invalidatePathSearch(m.FilePicker)
		m.invalidateDirectoryAsyncLoad(m.FilePicker)
		m.FilePicker.Close()
		m.setMode(ModeChat)
	})
}

// openWorkspaceSettings opens the JetBrains-style workspace settings as a floating window.
func (m *App) openWorkspaceSettings() {
	m.openSettings(ttypes.SettingsScopeWorkspace)
}

func (m *App) openApplicationSettings() {
	m.openSettings(ttypes.SettingsScopeApplication)
}

func (m *App) openApplicationAccounts() {
	m.openSettingsTo(ttypes.SettingsScopeApplication, true)
}

func (m *App) openApplicationMainAgent() {
	m.openSettingsTo(ttypes.SettingsScopeApplication, false)
	if fw := m.windowManager.Window(string(ui.OverlayWorkspaceSettings)); fw != nil {
		if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok {
			if body, ok := frame.Body().(*settingsui.WorkspaceSettingsSurface); ok {
				body.ShowMainAgent()
				frame.Host().MarkLayout()
			}
		}
	}
}

func (m *App) openSettings(scope ttypes.SettingsScope) {
	m.openSettingsTo(scope, false)
}

func (m *App) openSettingsTo(scope ttypes.SettingsScope, accounts bool) {
	m.overlayHost.Close(ui.OverlaySettingsPrompt)
	m.overlayHost.Close(ui.OverlayWorkspaceSettings)

	appDefaults := m.AppDefaults()
	overrides := m.LoadWorkspaceOverrides()
	working := appDefaults
	title := "Application Settings"
	if scope == ttypes.SettingsScopeWorkspace {
		working = m.LoadAppSettings()
		title = "Workspace Settings"
	}
	if len(working.Agents.Categories) == 0 {
		working.Agents = m.WorkspaceAgentDefaults()
	}
	if providers := m.AllProviderConfigs(); len(providers) > 0 {
		if working.Providers == nil {
			working.Providers = map[string]ttypes.ProviderConfig{}
		}
		for id, cfg := range providers {
			if _, ok := working.Providers[id]; !ok {
				working.Providers[id] = cfg
			}
		}
	}
	if working.ActiveProvider == "" {
		working.ActiveProvider = m.LastSettings.ProviderID
	}
	if working.ActiveProvider == "" {
		working.ActiveProvider = "openrouter"
	}

	wsWidget := popup.NewWorkspaceSettingsScoped(m.Theme, working, m.providerPresets, scope, appDefaults, overrides)

	w, h := fitFloatingSize(m.width, m.height, minInt(124, m.width-8), minInt(28, m.height-4), 60, 18)
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "workspace-settings-frame"),
		ui.FrameWindow,
		title,
		wsWidget.Component(),
		wsWidget,
		true,
		w,
		h,
	)
	wsWidget.SetFocusHandler(frame.Focus)
	win := wm.NewFloatingWindow(string(ui.OverlayWorkspaceSettings), title, frame,
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
	m.overlayHost.Open(ui.OverlayWorkspaceSettings, win, ui.ModalDialogPolicy(), func() {
		if m.settingsPrompt != nil && m.settingsPrompt.parent == win {
			m.overlayHost.Close(ui.OverlaySettingsPrompt)
		}
	})
	frame.Host().MarkLayout()
	frame.Host().Layout()
	if accounts {
		if len(appDefaults.Accounts) == 0 {
			wsWidget.ShowAccountSetup()
		} else {
			wsWidget.ShowAccounts()
		}
		frame.Host().MarkLayout()
		frame.Host().Layout()
	} else {
		wsWidget.FocusSearch()
	}
	win.MarkContentDirty()
}

func (m *App) updateSettingsWindowTitle() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window(string(ui.OverlayWorkspaceSettings))
	if fw == nil {
		return
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		return
	}
	body, ok := frame.Body().(*settingsui.WorkspaceSettingsSurface)
	if !ok || frame.Frame() == nil {
		return
	}
	if title := body.WindowTitle(); title != frame.Frame().Title() {
		frame.Frame().SetTitle(title)
		frame.Host().MarkLayout()
		fw.MarkContentDirty()
	}
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
			if fw.ID == string(ui.OverlayWorkspaceSettings) {
				return m, m.closePopupWindowByID(fw.ID)
			}
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

	// If there's an editor open, close it
	if m.EditorState.ActiveView != "" {
		m.closeEditorTab()
		return m, nil
	}

	// If there are window tabs, close the active one. Closing a tab
	// consumes the quit: the key must never tear down the app while other
	// tabs (orchestrators, editors) remain open.
	if m.Win.IsOpen() && m.Win.TabCount() > 0 {
		activeIdx := m.Win.ActiveIndex()
		if activeIdx >= 0 {
			tab := m.Win.Tabs()[activeIdx]
			action := m.Win.CloseTab(activeIdx)
			if action == window.CloseNone {
				return m, nil
			}
			// Closing an agent tab minimizes it; the orchestrator and
			// its conversation survive for later restore.
			if m.isAgentTabTitle(tab.Title) {
				agentID := m.agentIDFromTabTitle(tab.Title)
				m.minimizeAgent(agentID)
			}
			// If no tabs left, show welcome launcher
			if m.AgentMgr.VisibleCount() == 0 && m.EditorState.ActiveView == "" && !m.Win.IsOpen() {
				m.openLauncher()
			}
			// A tab was closed (or the launcher fallback shown): the quit
			// is consumed. Quitting is only reachable via the fall-through
			// below when there were no tabs to close at all.
			return m, nil
		}
	}

	// No tabs left and no sessions — quit
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil {
			continue
		}
		if state := m.editorSaves[tab.Editor.BufferID()]; state != nil && state.inFlight {
			if m.Feedback != nil {
				m.Feedback.NotifyInfo("Waiting for the file save to finish")
			}
			return m, nil
		}
		if tab.Editor.Modified() {
			m.showDirtyClosePrompt(tab.Path)
			return m, nil
		}
	}
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
	if p == nil || p.composer == nil {
		return m, nil
	}
	p.composer.Open("")
	frame := ui.NewWindowFrameWidget(
		m.Theme,
		ui.NewID(ui.RootID, "composer-frame"),
		ui.FrameWindow,
		"Composer",
		p.composer,
		nil,
		true,
		72,
		24,
	)
	p.composer.SetFocusHandler(frame.Focus)
	p.composer.FocusInput()

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

func (m *App) openSessionSettings() {
	p := m.activeSessionProjection()
	if p == nil || p.settings == nil {
		return
	}
	p.settings.ConfigureProviders(m.providerPresets, m.AllProviderConfigs())
	p.settings.ConfigureAccounts(m.AllAccounts())
	p.settings.SaveSnapshot()
	m.openSessionSettingsPanel()
}

// resetOrchestratorOverrides clears all runtime overrides for the current orchestrator.
func (m *App) resetOrchestratorOverrides() {
	id := m.AgentMgr.ActiveID()
	if id == "" {
		id = m.EditorState.ActiveView
		if id == "" {
			m.Feedback.NotifyInfo("No active orchestrator to reset")
			return
		}
	}
	m.ResetOrchestratorOverrides(id)
	m.refreshSessionSettingsProjection(m.ActiveSession)
	m.Feedback.NotifyInfo("Session overrides reset to workspace defaults")
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

	// Every NON-MODAL, non-typing overlay open starts a slide-in reveal on its
	// WindowFrame (driven by the per-frame tick). Popovers, palettes and
	// menus get the subtle open animation; modals (dirty-close, tree-delete,
	// file picker, settings) appear instantly for snappy confirmation UX.
	// Typing-driven popups (completion, references, outline) are excluded:
	// they re-open on every keystroke, so an animation would feel laggy.
	// The reveal is a no-op for windows without a WindowFrame body.
	oh.SetOnOpen(func(id ui.OverlayID, fw *wm.FloatingWindow) {
		switch id {
		case ui.OverlayCompletion, ui.OverlayReferences, ui.OverlayOutline:
			return
		}
		if entry := oh.Get(id); entry != nil && entry.Policy.Modal {
			return
		}
		m.beginReveal(fw, string(id))
	})

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
	oh.RegisterContentHandler(ui.OverlayAccountRecovery, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		choice := confirmFrameButtonAt(fw, localX, localY)
		if choice >= 0 {
			return m.activateAccountRecovery(choice)
		}
		return nil
	})
	oh.RegisterHoverHandler(ui.OverlayAccountRecovery, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		choice := confirmFrameButtonAt(fw, localX, localY)
		if choice >= 0 && choice != m.accountRecoveryChoice {
			m.accountRecoveryChoice = choice
			if cw, ok := fw.Content.(*ui.ComponentWidget); ok {
				ui.SetConfirmHover(cw, choice)
			}
		}
	})
	oh.RegisterContentHandler(ui.OverlaySettingsPrompt, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		if choice := confirmFrameButtonAt(fw, localX, localY); choice >= 0 {
			return m.activateSettingsPrompt(choice)
		}
		return nil
	})
	oh.RegisterHoverHandler(ui.OverlaySettingsPrompt, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		if cw, ok := fw.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(cw, confirmFrameButtonAt(fw, localX, localY))
		}
	})
	oh.RegisterContentHandler(ui.OverlayAgentPrompt, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		if m.agentPrompt != nil && m.agentPrompt.request.kind == agentPromptConfirmation {
			choice := confirmFrameButtonAt(fw, localX, localY)
			if choice >= 0 {
				m.resolveAgentConfirmationChoice(choice)
			}
			return nil
		}
		return m.forwardEventToWindowContent(fw, msg)
	})

	oh.RegisterContentHandler(ui.OverlayTreePrompt, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickTreePrompt(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayTreeDelete, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.clickTreeDeleteConfirm(localX, localY)
	})
	oh.RegisterHoverHandler(ui.OverlayTreeDelete, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		m.hoverTreeDeleteConfirm(localX, localY)
	})

	oh.RegisterContentHandler(ui.OverlayFilePicker, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.forwardEventToWindowContent(fw, msg)
	})
	oh.RegisterHoverHandler(ui.OverlayFilePicker, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})
	oh.RegisterContentHandler(ui.OverlaySessionPicker, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.forwardEventToWindowContent(fw, msg)
	})
	oh.RegisterHoverHandler(ui.OverlaySessionPicker, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})
	oh.RegisterContentHandler(ui.OverlayHelp, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.forwardEventToWindowContent(fw, msg)
	})
	oh.RegisterHoverHandler(ui.OverlayHelp, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})
	oh.RegisterContentHandler(ui.OverlayComposer, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.forwardEventToWindowContent(fw, msg)
	})
	oh.RegisterHoverHandler(ui.OverlayComposer, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
		x, y := fw.Position()
		m.forwardEventToWindowContent(fw, tea.MouseMotionMsg{X: x + localX, Y: y + localY, Button: button})
	})
	oh.RegisterContentHandler(ui.OverlayTextOverlay, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, msg tea.Msg) tea.Cmd {
		return m.forwardEventToWindowContent(fw, msg)
	})
	oh.RegisterHoverHandler(ui.OverlayTextOverlay, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
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
	oh.RegisterHoverHandler(ui.OverlayReplaceInFiles, func(host *ui.OverlayHost, fw *wm.FloatingWindow, localX, localY int, button tea.MouseButton) {
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
