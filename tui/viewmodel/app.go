package viewmodel

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/clipboard"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/cell"
	"crude/tui/compositor"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	ttypes "crude/tui/types"
	"crude/tui/widget"
	"crude/tui/widgets"
	"crude/tui/window"
	"crude/tui/wm"
	"crude/tui/workspace"
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
	width     int
	height    int

	// Compositing engine
	compositor    *compositor.Compositor
	windowManager *wm.WindowManager
	renderCtx     *widget.RenderContext
	cachedOutput       string // last rendered ANSI output for View() throttling during drag
	dirty              bool   // true when view content has changed and needs re-rendering
	contentDirty       bool   // true when the main view (editor/chat/launcher) needs re-rendering — separate from chrome (tree/tabs/popups) so hover changes don't trigger expensive chroma/glamour re-renders
	mainContentCached  string // last rendered main-view ANSI string — reused when only chrome changed (hover on tree, tabs, popups, etc.)

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

	OnQuit        func()
	OnExitMessage func()
	OnYoloCmd     func(bool)
	OnUndoCmd     func(string)

	Quit           bool
	lastCancelTime time.Time
}

func (m *App) AddOrchestrator(orchID string, inputCh chan<- provider.UserInput, btwCh chan<- provider.UserInput, cancelCh chan<- struct{}, blockResolveCh chan string) {
	m.launcher = nil

	ml := widgets.NewMessageList(m.Theme)
	ml.SetModelName(m.ModelName)
	ml.SetUsername(m.Username)
	ml.SetSubPaneContentFn(m.renderSubPaneContent)

	sp := m.PaneMgr.GetOrCreate(orchID, workspace.PaneAgent, orchID, m.Theme)
	sp.MsgList = ml
	sp.MsgList.SetModelName(m.ModelName)
	sp.MsgList.SetUsername(m.Username)

	agent := workspace.NewAgentState(orchID, ml, m.Logger)
	m.AgentMgr.AddOrchestrator(orchID, inputCh, btwCh, cancelCh, blockResolveCh, agent)
	m.syncAgentWindowTabs()
}

func (m *App) RemoveOrchestrator(orchID string) {
	m.cleanupAgent(orchID)
}

// cleanupAgent removes all state for a given agent orchestrator or sub-agent.
func (m *App) cleanupAgent(agentID string) {
	m.AgentMgr.Remove(agentID)
	m.PaneMgr.Remove(agentID)
	if m.StatusBar != nil {
		m.StatusBar.ClearAgentState(agentID)
	}
}

func (m *App) activeMsgList() *widgets.MessageList {
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil && n.State.MsgList != nil {
		return n.State.MsgList
	}
	return nil
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

func NewApp(modelName string, username string, initial ttypes.Settings, onSave func(ttypes.Settings) error, fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(string) ttypes.ProviderConfig, lg Logger, sessions []sessiondb.SessionMeta, sessionID, sessionDir string, db *sql.DB) *App {
	ws := workspace.NewWorkspace("", modelName, username, initial, onSave, getProviderConfigFn, lg, sessions, sessionID, sessionDir, db)

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

	app := &App{
		Workspace:          ws,
		EditorState:        edState,
		OverlayState:       NewOverlayState(),
		LSPPopup:           NewLSPPopupState(),
		launcher:           NewLauncherState(sessions),
		compositor:         comp,
		windowManager:      wmgr,
		eventBus:           eb,
		renderCtx:          rctx,
		lastCancelTime:     time.Now(),
		mainContentSurf:    contentSurf,
		mainSidebarSurf:    sidebarSurf,
		mainStatusbarSurf:  statusbarSurf,
		mainHeaderSurf:     headerSurf,
	}

	return app
}

// resizeLayerSurfaces resizes and repositions the persistent layer surfaces.
// When the sidebar is visible, the main content surface is shifted right by
// sidebarW and narrowed so it does not render underneath the sidebar. The
// sidebar, header, and status bar are overlays that update independently.
func (m *App) resizeLayerSurfaces() {
	if m.mainContentSurf == nil {
		return
	}

	sidebarW := 0
	if m.Tree.Visible {
		sidebarW = m.Tree.Width + 1 // tree content + divider column
	}

	// Status bar is 2 rows: a blank separator row and the status content.
	statusH := 2
	if m.mainStatusbarSurf == nil {
		m.mainStatusbarSurf = surface.NewSurface("main-statusbar", m.width, statusH, cell.TransparentBlank())
		m.compositor.Root().AddChild(m.mainStatusbarSurf)
		m.mainStatusbarSurf.SetZOrder(2)
	} else {
		m.mainStatusbarSurf.Resize(m.width, statusH)
	}
	m.mainStatusbarSurf.SetPosition(0, m.height-statusH)

	// Content and sidebar stop above the status bar.
	endContent := m.height - statusH
	contentX := sidebarW
	contentW := m.width - sidebarW
	if contentW < 1 {
		contentW = 1
	}
	contentH := endContent - 0
	if contentH < 1 {
		contentH = 1
	}
	m.mainContentSurf.Resize(contentW, contentH)
	m.mainContentSurf.SetPosition(contentX, 0)

	sidebarH := contentH - m.Tree.YOffset
	if sidebarH < 1 {
		sidebarH = 1
	}
	if m.mainSidebarSurf == nil {
		m.mainSidebarSurf = surface.NewSurface("main-sidebar", sidebarW, sidebarH, cell.TransparentBlank())
		m.compositor.Root().AddChild(m.mainSidebarSurf)
		m.mainSidebarSurf.SetZOrder(1)
	} else if sidebarW > 0 {
		m.mainSidebarSurf.Resize(sidebarW, sidebarH)
	}
	m.mainSidebarSurf.SetPosition(0, m.Tree.YOffset)

	if m.mainHeaderSurf == nil {
		m.mainHeaderSurf = surface.NewSurface("main-header", m.width, 2, cell.TransparentBlank())
		m.compositor.Root().AddChild(m.mainHeaderSurf)
		m.mainHeaderSurf.SetZOrder(3)
	} else {
		m.mainHeaderSurf.Resize(m.width, 2)
	}
	// Header surface starts at Y=1 to leave room for the padding row
	// (row 0) which appears at the very top of the app.
	m.mainHeaderSurf.SetPosition(0, 1)
}

// renderSidebarToSurface draws the file tree (plus its right-hand divider)
// into the sidebar layer surface. Uses RenderToLines — the same path as the
// original buildMainView — so scrollbar and LSP diagnostic indicators work.
func (m *App) renderSidebarToSurface() {
	if m.mainSidebarSurf == nil || m.Tree.Renderer == nil {
		return
	}

	sidebarH := m.mainSidebarSurf.Height()
	// Fill with theme background so the surface is fully opaque
	// (the default cell is transparent, which would let the back
	// buffer show through and create visual gaps).
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainSidebarSurf.Buffer().Fill(bgCell)

	// Update tree state before rendering so diagnostics, scrollbar
	// and hover use the current viewport size.
	m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
	m.Tree.Renderer.SetSize(m.Tree.Width, sidebarH)
	m.Tree.Renderer.SetScrollbarContentHeight(sidebarH)

	treeLines := m.Tree.Renderer.RenderToLines(m.Tree.Width, sidebarH)
	dividerStyle := lipgloss.NewStyle().Foreground(m.Theme.Tree.Colors.BorderFg)
	divider := dividerStyle.Render("│")

	var full strings.Builder
	for i, line := range treeLines {
		if i > 0 {
			full.WriteByte('\n')
		}
		full.WriteString(line)
		full.WriteString(divider)
	}
	rendering.RenderToSurface(m.mainSidebarSurf, full.String())
}

// renderStatusbarToSurface draws the status bar (2 rows: blank separator +
// status content) into its own layer surface.
func (m *App) renderStatusbarToSurface() {
	if m.mainStatusbarSurf == nil {
		return
	}
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainStatusbarSurf.Buffer().Fill(bgCell)

	// Row 0: separator line (─).
	// Row 1: status content with the standard left/right padding.
	pad := strings.Repeat(" ", m.Theme.Spacing.PadLR)
	statusView := m.StatusBar.View()
	visW := rendering.VisibleWidth(statusView)
	rightPad := m.width - 2*m.Theme.Spacing.PadLR - visW
	if rightPad < 0 {
		rightPad = 0
	}
	full := widgets.RenderSep(m.width, m.Theme.Colors.Border) + "\n" + pad + statusView + strings.Repeat(" ", rightPad) + pad
	rendering.RenderToSurface(m.mainStatusbarSurf, full)
}

// renderHeaderToSurface draws the header line (crude + model + uptime + agent
// label) and the separator below it into the header layer surface.
func (m *App) renderHeaderToSurface() {
	if m.mainHeaderSurf == nil {
		return
	}
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainHeaderSurf.Buffer().Fill(bgCell)

	pad := strings.Repeat(" ", m.Theme.Spacing.PadLR)

	headerLine := widgets.RenderHeader(m.Theme, m.ModelName, m.OverlayState.IsYolo(), m.StartTime, m.width)
	if node := m.AgentMgr.ActiveNode(); node != nil {
		agentLabel := node.ID
		if node.Label != "" {
			agentLabel = node.Label
		}
		sp := m.PaneMgr.Get(node.ID)
		catLabel := ""
		if sp != nil {
			catLabel = string(sp.Type)
		}
		viewLabel := agentLabel
		if catLabel != "" {
			viewLabel = catLabel + ":" + agentLabel
		}
		accentStyle := lipgloss.NewStyle().Foreground(m.Theme.Colors.Accent).Bold(true)
		headerLine += " " + accentStyle.Render("⟨"+viewLabel+"⟩")
	}

	// Row 0: header line
	// Row 1: separator
	full := pad + headerLine + pad + "\n" + widgets.RenderSep(m.width, m.Theme.Colors.Border)
	rendering.RenderToSurface(m.mainHeaderSurf, full)
}

func (m *App) renderSubPaneContent(paneID string, width, maxH, spinFrame int) []string {
	sp := m.PaneMgr.Get(paneID)
	if sp == nil {
		return []string{"", "● unknown pane"}
	}
	if n := m.AgentMgr.Get(paneID); n != nil && n.State != nil {
		sp.MsgList.SetStreaming(n.State.Streaming)
		sp.MsgList.SetActiveTyping(n.State.ComputeActiveTyping())
	}
	result := sp.Render(width, maxH, spinFrame)
	return result.Lines
}

func (m *App) completeSubAgent(agentID string) {
	n := m.AgentMgr.Get(agentID)
	if n == nil || n.State == nil {
		return
	}
	if n.State.Completed {
		return
	}

	parentID := ""
	if n.Parent != nil {
		parentID = n.Parent.ID
	}
	parentNode := m.AgentMgr.Get(parentID)
	var parentML *widgets.MessageList
	if parentNode != nil && parentNode.State != nil {
		parentML = parentNode.State.MsgList
	}

	title := m.agentTabTitle(agentID)
	if tab := m.Win.TabByTitle(title); tab != nil {
		wasActive := m.Win.IndexOf(tab.ID) == m.Win.ActiveIndex()
		m.Win.CloseTabByID(tab.ID)
		if wasActive && m.AgentMgr.ActiveID() == agentID {
			m.AgentMgr.ClearActive()
			if parentML != nil {
				parentML.InvalidateAllMessages()
			}
		}
	}

	if sp := m.PaneMgr.Get(agentID); sp != nil && !sp.Done {
		sp.Collapse()
		if parentML != nil {
			parentML.InvalidateSubPane(agentID)
		}
	}

	n.State.Completed = true
	n.State.Streaming = false
	n.State.Thinking = false
	if n.State.Status == "" || n.State.Status == "running" {
		n.State.Status = "completed"
	}

	m.StatusBar.ClearAgentState(agentID)

	if !m.Win.IsOpen() {
		m.setMode(ModeChat)
	} else {
		m.syncOverlayMode()
	}
}

func (m *App) anyActive() bool {
	if m.AgentMgr.AnyActive() {
		return true
	}
	for _, sp := range m.PaneMgr.Panes() {
		if !sp.Done {
			return true
		}
	}
	return false
}

func (m *App) SetProviderPresets(presets []ttypes.ProviderPreset) {
	m.ProviderPresets = presets
	m.Settings.SetPresets(presets)
}

func (m *App) SetHistory(history []string) {
	m.History = history
	m.Input.SetHistory(history)
}

func (m *App) setMode(newMode Mode) {
	oldMode := m.OverlayState.Current()
	if m.Logger != nil {
		m.Logger.Debug("UI", "mode_switch", "from", modeName(oldMode), "to", modeName(newMode))
	}
	m.OverlayState.Set(newMode)
	if ml := m.activeMsgList(); ml != nil {
		ml.ClearSelection()
		ml.InvalidateAllMessages()
	}
	switch newMode {
	case ModeSettings:
		m.Input.ClearInput()
		m.Overlay.OpenTab("Settings", "", true)
	case ModeConfirm:
		m.Overlay.OpenTab("Confirm", "", true)
	case ModeQuestion:
		m.Overlay.OpenTab("Question", "", true)
	case ModeYolo:
		m.Overlay.OpenTab("YOLO", "", true)
	case ModeTextOverlay:
		m.openTextOverlayWindow()
	case ModeFilePicker:
		m.openFilePickerWindow()
	case ModeChat:
		m.FormHint = ""
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("textoverlay")
			m.windowManager.RemoveWindow("filepicker")
		}
	}
}

func modeName(m Mode) string {
	return workspace.ModeName(m)
}

func (m *App) SetDeleteSessionFn(fn func(string)) {
	m.OnDeleteSession = fn
}

func (m *App) SetListSessionsFn(fn func() []sessiondb.SessionMeta) {
	m.OnListSessions = fn
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

func (m *App) activeMessageList() *widgets.MessageList {
	activeML := m.activeMsgList()
	if activeML != nil {
		return activeML
	}
	// Fallback: use any available agent's MsgList
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil && n.State.MsgList != nil {
			return n.State.MsgList
		}
	}
	return nil
}

func (m *App) agentForView() *workspace.AgentState {
	if n := m.AgentMgr.ActiveNode(); n != nil && n.State != nil {
		return n.State
	}
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil {
			return n.State
		}
	}
	return nil
}

func (m *App) activeAgentID() string {
	return m.AgentMgr.ActiveID()
}

func (m *App) switchToAgentView(agentID string) {
	prev := m.AgentMgr.ActiveID()
	if agentID == "" {
		m.AgentMgr.ClearActive()
	} else if n := m.AgentMgr.Get(agentID); n != nil && n.State != nil && !n.State.Completed {
		m.AgentMgr.SetActive(agentID)
	}

	curr := m.AgentMgr.ActiveID()
	if prev != curr {
		if prev != "" {
			if n := m.AgentMgr.Get(prev); n != nil && n.State != nil && n.State.MsgList != nil {
				n.State.MsgList.InvalidateAllMessages()
			}
		}
		if curr != "" {
			if n := m.AgentMgr.Get(curr); n != nil && n.State != nil && n.State.MsgList != nil {
				n.State.MsgList.InvalidateAllMessages()
			}
		}
	}

	m.syncAgentWindowTabs()
}

// agentTabTitle returns the window tab title for an agent.
// Uses the full agentID to guarantee uniqueness (no truncation).
// Human-readable display names are set via DisplayTitle on the tab.
func (m *App) agentTabTitle(agentID string) string {
	if agentID == "" {
		return "agent:main"
	}
	return "agent:" + agentID
}

// isAgentTabTitle checks if a window tab title is an agent tab.
func (m *App) isAgentTabTitle(title string) bool {
	return len(title) > 6 && title[:6] == "agent:"
}

// agentIDFromTabTitle extracts the agent ID from a window tab title.
// Since tab titles now use the full agentID, parsing is direct.
func (m *App) agentIDFromTabTitle(title string) string {
	if !m.isAgentTabTitle(title) {
		return ""
	}
	return title[6:]
}

// syncAgentWindowTabs creates/removes window tabs to match the current agent set.
// Every agent gets its own tab.
// Tabs are always shown when at least one agent or editor is open.
func (m *App) syncAgentWindowTabs() {
	// Don't switch away from editor tab
	if m.EditorState.ActiveView != "" {
		m.purgeStaleAgentTabs()
		return
	}

	// If no agents exist, purge agent tabs and return
	if m.AgentMgr.Count() == 0 {
		m.purgeAgentTabs()
		m.EditorState.TabSnapshotCache = ""
		return
	}

	// Determine active agent ID
	activeID := m.AgentMgr.ActiveID()

	// Create tabs for sub-agents that have panes
	for _, id := range m.PaneMgr.PaneOrder() {
		n := m.AgentMgr.Get(id)
		if n == nil || (n.State != nil && n.State.Completed) {
			continue
		}
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTab(title, "", true)
		}
		if tab := m.Win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.Win.SetTabIconByIndex(m.Win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := id
			if n.Label != "" {
				displayLabel = n.Label
			}
			if tab.DisplayTitle != displayLabel {
				m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(tab.ID), displayLabel)
			}
		}
	}

	// Create tabs for orchestrator agents (top-level agents without parent IDs)
	for _, n := range m.AgentMgr.AllNodes() {
		if n.State != nil && n.State.Completed {
			continue
		}
		if n.Parent != nil {
			continue
		}
		id := n.ID
		title := m.agentTabTitle(id)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTab(title, "", true)
		}
		if tab := m.Win.TabByTitle(title); tab != nil {
			newIcon := m.agentTabIcon(id)
			if tab.Icon != newIcon {
				m.Win.SetTabIconByIndex(m.Win.IndexOf(tab.ID), newIcon)
			}
			displayLabel := id
			if n.Label != "" {
				displayLabel = n.Label
			}
			if tab.DisplayTitle != displayLabel {
				m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(tab.ID), displayLabel)
			}
		}
	}

	// Activate the correct tab
	if activeID != "" {
		activeTitle := m.agentTabTitle(activeID)
		if tab := m.Win.TabByTitle(activeTitle); tab != nil {
			targetIdx := m.Win.IndexOf(tab.ID)
			if m.Win.ActiveIndex() != targetIdx {
				m.Win.SwitchTo(targetIdx)
			}
		}
	}

	m.purgeStaleAgentTabs()
}

func (m *App) agentTabSnapshot() string {
	var b strings.Builder
	for _, id := range m.PaneMgr.PaneOrder() {
		n := m.AgentMgr.Get(id)
		if n == nil || n.State == nil || n.State.Completed {
			continue
		}
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(n.State.Status)
		b.WriteByte('|')
		if n.Label != "" {
			b.WriteString(n.Label)
		}
		b.WriteByte('|')
	}
	return b.String()
}

func (m *App) ensureAgentTabActive() {
	activeID := m.AgentMgr.ActiveID()
	if activeID == "" {
		return
	}
	activeTitle := m.agentTabTitle(activeID)
	if tab := m.Win.TabByTitle(activeTitle); tab != nil {
		targetIdx := m.Win.IndexOf(tab.ID)
		if targetIdx >= 0 && m.Win.ActiveIndex() != targetIdx {
			m.Win.SwitchTo(targetIdx)
		}
	}
}

// agentTabIcon returns the status icon for an agent's window tab.
func (m *App) agentTabIcon(id string) string {
	if id == "" {
		return "■"
	}
	n := m.AgentMgr.Get(id)
	if n == nil || n.State == nil || n.State.Status == "" {
		return "●"
	}
	status := n.State.Status
	switch {
	case status == "running":
		return "●"
	case status == "completed":
		return "✓"
	case len(status) > 6 && status[:6] == "failed":
		return "✗"
	case status == "cancelled":
		return "!"
	default:
		return "■"
	}
}

// purgeAgentTabs closes all agent window tabs in a single pass.
func (m *App) purgeAgentTabs() {
	for i := m.Win.TabCount() - 1; i >= 0; i-- {
		tabs := m.Win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			m.Win.CloseTabByID(tabs[i].ID)
		}
	}
}

// purgeStaleAgentTabs closes agent tabs for agents that no longer exist
// or have completed.
func (m *App) purgeStaleAgentTabs() {
	for i := m.Win.TabCount() - 1; i >= 0; i-- {
		tabs := m.Win.Tabs()
		if i >= len(tabs) {
			continue
		}
		if m.isAgentTabTitle(tabs[i].Title) {
			agentID := m.agentIDFromTabTitle(tabs[i].Title)
			n := m.AgentMgr.Get(agentID)
			if n == nil || (n.State != nil && n.State.Completed) {
				m.Win.CloseTabByID(tabs[i].ID)
			}
		}
	}
}

// closeAllOverlayTabs closes all overlay tabs and returns to chat mode.
func (m *App) closeAllOverlayTabs() {
	m.Overlay.CloseAll()
	if m.OverlayState.IsOverlay() {
		m.OverlayState.Set(ModeChat)
		m.FormHint = ""
		m.Input.ResetPrompt()
		m.Input.ClearInput()
	}
}

func (m *App) syncOverlayMode() {
	if !m.Overlay.IsOpen() {
		if m.OverlayState.IsOverlay() {
			m.OverlayState.Set(ModeChat)
			m.FormHint = ""
			m.Input.ResetPrompt()
			m.Input.ClearInput()
		}
		return
	}
	activeTab := m.Overlay.ActiveTab()
	if activeTab == nil {
		if m.OverlayState.IsOverlay() {
			m.OverlayState.Set(ModeChat)
			m.FormHint = ""
			m.Input.ResetPrompt()
			m.Input.ClearInput()
		}
		return
	}
	switch {
	case activeTab.Title == "Settings":
		m.OverlayState.Set(ModeSettings)
	case activeTab.Title == "Confirm":
		m.OverlayState.Set(ModeConfirm)
	case activeTab.Title == "YOLO":
		m.OverlayState.Set(ModeYolo)
	case activeTab.Title == "Question":
		m.OverlayState.Set(ModeQuestion)
	default:
		m.OverlayState.Set(ModeChat)
		m.FormHint = ""
	}
}

func (m *App) editorTabTitle(path string) string {
	return "editor:" + path
}

func (m *App) isEditorTabTitle(title string) bool {
	return len(title) > 7 && title[:7] == "editor:"
}

func (m *App) editorPathFromTabTitle(title string) string {
	if !m.isEditorTabTitle(title) {
		return ""
	}
	return title[7:]
}

func (m *App) openEditorTab(path string) (tea.Cmd, error) {
	tabTitle := m.editorTabTitle(path)

	// If the tab is already open, just activate it. Crucially, do NOT
	// reload the file — switching tabs must preserve cursor, scroll,
	// undo, and unsaved edits.
	if tab, ok := m.EditorState.Tabs[path]; ok && tab.Editor.IsOpen() {
		if m.Win.TabByTitle(tabTitle) == nil {
			// Tab exists in our map but the window tab was lost
			// (e.g. window tabs were reset). Re-create it.
			m.Win.OpenTab(tabTitle, "✎", true)
		}
		if winTab := m.Win.TabByTitle(tabTitle); winTab != nil {
			m.Win.SwitchTo(m.Win.IndexOf(winTab.ID))
			m.handleTabSwitch()
		}
		return nil, nil
	}

	// Get or create the shared buffer via BufferManager. Multiple tabs
	// viewing the same file share one Buffer (PieceTable + dirty flag +
	// diagnostics). The Editor wraps this buffer with view-specific state
	// (cursor, scroll, selection, undo history).
	buf, err := m.BufferMgr.OpenFile(path)
	if err != nil {
		return nil, err
	}

	// Create a fresh Editor for this tab and open the shared buffer.
	// Per-tab view state (cursor, scroll, undo, selection, chroma cache,
	// search) lives inside the Editor. Switching to another tab and back
	// gives back the same Editor instance with all state intact.
	ed := editor.New(m.Theme)
	ed.OpenBuffer(buf)

	// Wire content changes to LSP manager.
	ed.SetOnContentChanged(func(fp, text string) {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.LSPMgr.UpdateFile(lspCtx, fp, text)
	})
	// Apply format-on-save preference from user settings. Defaults to off.
	if m.LastSettings.Editor.FormatOnSave {
		ed.SetFormatOnSave(true)
	} else {
		ed.SetFormatOnSave(false)
	}
	// Open the file on the LSP server.
	lspCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.LSPMgr.OpenFile(lspCtx, path, ed.Value())

	// Apply cached diagnostics immediately so the editor is never empty
	// while waiting for the server to respond.
	if cached := m.LSPMgr.FileDiagnostics(path); len(cached) > 0 {
		ed.SetDiagnostics(cached)
	}

	displayTitle := ed.Title()

	// Store the tab and point m.EditorState.Editor at it.
	m.EditorState.Tabs[path] = workspace.NewEditorTab(path, ed)
	m.EditorState.Editor = ed
	m.EditorState.ActiveView = path

	if m.Win.TabByTitle(tabTitle) == nil {
		m.Win.OpenTab(tabTitle, "✎", true)
	}
	if winTab := m.Win.TabByTitle(tabTitle); winTab != nil {
		m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(winTab.ID), displayTitle)
		m.Win.SwitchTo(m.Win.IndexOf(winTab.ID))
		m.handleTabSwitch()
	}

	return ed.BlinkCmd(), nil
}

// jumpToDiagnostic opens the file containing the given diagnostic
// (if not already open), positions the cursor on it, highlights the
// range, scrolls it into view, and refreshes the hover tooltip. The
// caller is responsible for choosing the diagnostic.
func (m *App) jumpToDiagnostic(filePath string, d editor.LSPDiagnostic) {
	if filePath == "" {
		return
	}
	// Open or activate the file. openEditorTab is a no-op if the
	// tab is already the active one.
	if _, err := m.openEditorTab(filePath); err != nil {
		m.StatusBar.SetError("Cannot open " + filePath)
		return
	}
	if m.EditorState.Editor == nil {
		return
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.dirty = true
}

// gotoFirstDiagnostic opens the first error diagnostic in the
// project (sorted by file path, then by line/col) and jumps to it.
// If a file is already open and has errors, that file's first error
// wins. If the project has no errors, shows a status bar message.
func (m *App) gotoFirstDiagnostic() {
	if m.LSPMgr == nil {
		m.StatusBar.SetInfo("LSP not configured")
		return
	}
	all := m.LSPMgr.AllDiagnostics()
	if len(all) == 0 {
		m.StatusBar.SetInfo("No errors in project")
		return
	}

	// Prefer an error in the currently-active file (if any). The
	// user's current focus is more important than file-path order.
	var paths []string
	if active := m.activeEditorPath(); active != "" {
		if diags, ok := all[active]; ok {
			if e := firstErrorOf(diags); e != nil {
				m.jumpToDiagnostic(active, *e)
				m.StatusBar.SetInfo(fmt.Sprintf("Jumped to %s:%d",
					filepath.Base(active), e.Line+1))
				return
			}
		}
		paths = append(paths, active)
	}
	// Collect remaining paths in sorted order so the navigation is
	// deterministic across runs.
	for p := range all {
		if p == m.activeEditorPath() {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		e := firstErrorOf(all[p])
		if e == nil {
			continue
		}
		m.jumpToDiagnostic(p, *e)
		m.StatusBar.SetInfo(fmt.Sprintf("Jumped to %s:%d",
			filepath.Base(p), e.Line+1))
		return
	}

	m.StatusBar.SetInfo("No errors in project")
}

// firstErrorOf returns the first error-severity diagnostic in the
// given list (sorted by line then col, as LSP publishes them). The
// returned pointer aliases the input slice — safe to dereference
// before the next mutation.
func firstErrorOf(diags []editor.LSPDiagnostic) *editor.LSPDiagnostic {
	for i := range diags {
		if diags[i].Severity == editor.DiagError {
			return &diags[i]
		}
	}
	return nil
}

// jumpToNextError moves the cursor to the next error diagnostic in
// the currently-open file (strictly after the current cursor). If
// there is no editor, no file is open, or no further errors, shows
// a status bar message and returns false. Returns true on success.
func (m *App) jumpToNextError() bool {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		m.StatusBar.SetInfo("No file open")
		return false
	}
	d := m.EditorState.Editor.NextError(m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol())
	if d == nil {
		// Wrap around to the first error so F8 cycles through
		// every error like VSCode's command.
		d = m.EditorState.Editor.FirstError()
		if d == nil {
			m.StatusBar.SetInfo("No errors in file")
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.StatusBar.SetInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	m.dirty = true
	return true
}

// jumpToPrevError moves the cursor to the previous error diagnostic
// in the currently-open file (strictly before the current cursor).
// Mirrors jumpToNextError and wraps around to the last error.
func (m *App) jumpToPrevError() bool {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		m.StatusBar.SetInfo("No file open")
		return false
	}
	d := m.EditorState.Editor.PrevError(m.EditorState.Editor.CursorLine(), m.EditorState.Editor.CursorCol())
	if d == nil {
		// Wrap to the last error in the file.
		diags := m.EditorState.Editor.Diagnostics()
		for i := len(diags) - 1; i >= 0; i-- {
			if diags[i].Severity == editor.DiagError {
				d = &diags[i]
				break
			}
		}
		if d == nil {
			m.StatusBar.SetInfo("No errors in file")
			return false
		}
	}
	m.EditorState.Editor.SetCursorPosition(d.Line, d.Col)
	m.EditorState.Editor.SetSelection(d.Line, d.Col, d.EndLine, d.EndCol)
	m.EditorState.Editor.ScrollToCursor()
	layout := m.computeLayout()
	m.EditorState.Editor.RefreshCursorHover(layout.SidebarW, layout.ChatAreaTop)
	m.StatusBar.SetInfo(fmt.Sprintf("Error at line %d: %s", d.Line+1, d.Message))
	m.dirty = true
	return true
}

func (m *App) closeEditorTab() {
	if m.activeEditorPath() == "" {
		return
	}
	path := m.activeEditorPath()

	// If the tab has unsaved edits, prompt before destroying the
	// buffer. The popup is modal; on confirm we re-enter with a
	// force flag.
	if tab, ok := m.EditorState.Tabs[path]; ok && tab.Editor != nil && tab.Editor.Modified() {
		m.showDirtyClosePrompt(path)
		return
	}

	m.closeEditorTabForce(path)
}

// closeEditorTabForce unconditionally closes the given editor tab.
// Used both for the normal close path and after the user confirms a
// close-with-unsaved-changes prompt.
func (m *App) closeEditorTabForce(path string) {
	// Close any popups associated with this editor before closing the tab.
	m.closeFindReplace()
	m.closeReferencesPopup()
	m.closeCompletionPopup()
	m.closeOutlinePopup()
	m.clearDiagHoverPopup()
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("diag-context")
	}

	tabTitle := m.editorTabTitle(path)
	if tab := m.Win.TabByTitle(tabTitle); tab != nil {
		m.Win.CloseTabByID(tab.ID)
	}

	tab, ok := m.EditorState.Tabs[path]
	if ok {
		lspCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.LSPMgr.CloseFile(lspCtx, path)
		tab.Editor.Close()
		delete(m.EditorState.Tabs, path)
	}

	// Pick a new active editor tab if any remain. The window tab
	// switcher has already advanced ActiveIndex; the next call to
	// handleTabSwitch() will look up the new active path in the map.
	m.handleTabSwitch()

	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		if m.OnListSessions != nil {
			m.launcher = NewLauncherState(m.OnListSessions())
			m.LastInteractionTime = time.Now()
		}
	}
}

// openFindReplace opens the find/replace floating popup above the
// active editor. If mode is "replace", the replace field is shown.
// If the popup is already open, the mode flag is updated and the
// popup re-rendered.
func (m *App) openFindReplace(replaceMode bool) {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return
	}
	if m.FindReplace == nil {
		m.FindReplace = &widgets.FindReplaceState{
			HoveredToggle: -1, // no toggle hovered initially
		}
	}
	m.FindReplace.ReplaceMode = replaceMode
	// Pull current search state from the editor so the popup opens
	// with the existing query if the user already had one typed.
	widgets.FindReplaceFromEditor(m.FindReplace, m.EditorState.Editor)
	m.FindReplace.FocusedField = 0
	// Move the find input cursor to the end of the query (the
	// standard "open the popup, ready to append" UX). The replace
	// input's cursor is wherever it was last left.

	// Wire callbacks to mutate the editor.
	fr := m.FindReplace
	fr.OnChange = func() {
		widgets.FindReplaceToEditor(fr, m.EditorState.Editor)
		fr.MatchCount = len(m.EditorState.Editor.Search().Matches)
		fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
		m.refreshFindReplacePopup()
	}
	fr.OnNext = func() {
		if n, ok := m.EditorState.Editor.NextMatch(); ok {
			fr.MatchCount = n
			fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnPrev = func() {
		if n, ok := m.EditorState.Editor.PrevMatch(); ok {
			fr.MatchCount = n
			fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnReplace = func() {
		if n := m.EditorState.Editor.ReplaceCurrent(); n >= 0 {
			fr.MatchCount = n
			fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
			m.refreshFindReplacePopup()
		}
	}
	fr.OnReplaceAll = func() {
		count := m.EditorState.Editor.ReplaceAll()
		m.StatusBar.SetInfo(fmt.Sprintf("Replaced %d occurrences", count))
		fr.MatchCount = len(m.EditorState.Editor.Search().Matches)
		fr.CurrentIdx = m.EditorState.Editor.Search().CurrentIdx
		m.refreshFindReplacePopup()
	}
	fr.OnClose = func() {
		m.closeFindReplace()
	}

	m.refreshFindReplacePopup()
}

// closeFindReplace closes the find/replace popup. The editor's
// search state is preserved (so re-opening shows the same query),
// but highlights stop rendering because Open is set to false on the
// editor's SearchState.
func (m *App) closeFindReplace() {
	if m.EditorState.Editor != nil {
		m.EditorState.Editor.SetSearchOpen(false)
	}
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("find-replace")
	}
	m.FindReplace = nil
	m.dirty = true
}

// refreshFindReplacePopup rebuilds the floating window's content
// lines. Called when the user types, toggles an option, or
// navigates.
func (m *App) refreshFindReplacePopup() {
	if m.windowManager == nil || m.FindReplace == nil {
		return
	}
	lines := widgets.FindReplaceLines(m.Theme, m.FindReplace)
	pop := widgets.NewPopupContent(lines)

	// FindReplaceLines now self-renders the unified popup chrome.
	// Use the popup's own width/height functions.
	w := widgets.FindReplacePopupWidth(m.FindReplace)
	h := len(lines)

	if fw := m.windowManager.Window("find-replace"); fw != nil {
		// Just refresh the content. Don't re-center, since the
		// user may have moved it.
		fw.SetSize(w, h)
		fw.SetContent(pop)
		fw.MarkContentDirty()
		return
	}

	// First time: create and position above the active editor.
	layout := m.computeLayout()

	// The chrome is self-rendered. The FloatingWindow is a plain
	// positioned box.
	fw := wm.NewFloatingWindow("find-replace", "", pop, w, h,
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false), // not modal — user can still click editor
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	// Position in the top-right of the editor area, with margin.
	x := layout.SidebarW + m.EditorState.Editor.PadL + (layout.EditorW - w) - 2
	if x < layout.SidebarW {
		x = layout.SidebarW + 2
	}
	y := layout.ChatAreaTop + 1
	fw.SetPosition(x, y)
	m.windowManager.AddWindow(fw)
}

// showDirtyClosePrompt pops up a 3-button dialog asking what to do with
// unsaved changes for the given tab: Save (saves and closes),
// Don't Save (discards and closes), Cancel (keeps the tab open).
// The 3-button design matches VSCode/JetBrains and eliminates the
// "Esc loop" trap where dismissing a 2-button prompt with Esc would
// re-trigger the close on the next Esc.
func (m *App) showDirtyClosePrompt(path string) {
	if m.windowManager == nil {
		// No floating-window support? Just close and lose the data.
		m.closeEditorTabForce(path)
		return
	}
	tab, ok := m.EditorState.Tabs[path]
	if !ok {
		return
	}
	filename := tab.Editor.Title()
	detail := fmt.Sprintf("'%s' has unsaved changes.", filename)
	// Default focus is "Cancel" (index 2) so an accidental Enter keeps
	// the user's work safe.
	lines := widgets.ConfirmPrompt3Button(m.Theme,
		"Unsaved changes",
		"What do you want to do with the unsaved changes?",
		detail,
		widgets.ConfirmPromptWarning,
		m.DirtyPromptChoice,
		62,
	)

	const id = "dirty-close-prompt"
	m.windowManager.RemoveWindow(id)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow(id, "", pop, 62, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.windowManager.AddWindow(fw)
	m.DirtyPromptPath = path
	// Reset choice to Cancel (safe default) on open.
	m.DirtyPromptChoice = 2
}

func popLongestLine(lines []string) string {
	longest := ""
	for _, l := range lines {
		// Strip ANSI sequences for the width check.
		plain := stripANSI(l)
		if len(plain) > len(longest) {
			longest = plain
		}
	}
	return longest
}

func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		if r == 0x1b {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// dirtyPromptSave is called when the user picks "Save" in the
// dirty-close prompt. Saves the file (if it has a path) and then
// closes the tab. If save fails, the popup stays open so the user
// can pick "Don't Save" or "Cancel".
func (m *App) dirtyPromptSave() {
	path := m.DirtyPromptPath
	if path == "" {
		return
	}
	tab, ok := m.EditorState.Tabs[path]
	if !ok {
		// Tab was already closed somehow; just dismiss.
		m.DirtyPromptPath = ""
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("dirty-close-prompt")
		}
		return
	}
	// Save (synchronous). The editor's Save() returns an error; on
	// failure, surface it in the status bar and keep the popup open.
	if err := tab.Editor.Save(); err != nil {
		m.StatusBar.SetError(fmt.Sprintf("Save failed: %v", err))
		return
	}
	// Saved successfully — close the tab.
	m.DirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
	m.closeEditorTabForce(path)
}

// dirtyPromptDiscard is called when the user picks "Don't Save" in
// the dirty-close prompt. Closes the tab without saving.
func (m *App) dirtyPromptDiscard() {
	path := m.DirtyPromptPath
	m.DirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
	if path != "" {
		m.closeEditorTabForce(path)
	}
}

// dirtyPromptCancel is called when the user picks "Cancel" in the
// dirty-close prompt. Dismisses the popup and keeps the tab open.
func (m *App) dirtyPromptCancel() {
	m.DirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
}

// handleDirtyPromptKey routes keypresses when the dirty-close dialog
// is open. Cycles focus between Save / Don't Save / Cancel and
// dispatches the chosen action.
func (m *App) handleDirtyPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.DirtyPromptPath == "" {
		return nil
	}
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m.DirtyPromptChoice = (m.DirtyPromptChoice + 2) % 3 // -1 mod 3
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyRight, tea.KeyDown:
		m.DirtyPromptChoice = (m.DirtyPromptChoice + 1) % 3
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyTab:
		// Shift+Tab moves focus left, plain Tab moves focus right.
		if msg.Mod.Contains(tea.ModShift) {
			m.DirtyPromptChoice = (m.DirtyPromptChoice + 2) % 3
		} else {
			m.DirtyPromptChoice = (m.DirtyPromptChoice + 1) % 3
		}
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyEnter:
		switch m.DirtyPromptChoice {
		case 0:
			m.dirtyPromptSave()
		case 1:
			m.dirtyPromptDiscard()
		default:
			m.dirtyPromptCancel()
		}
		return nil
	case tea.KeyEsc:
		m.dirtyPromptCancel()
		return nil
	}
	switch msg.String() {
	case "s", "S":
		m.DirtyPromptChoice = 0
		m.dirtyPromptSave()
		return nil
	case "d", "D":
		m.DirtyPromptChoice = 1
		m.dirtyPromptDiscard()
		return nil
	case "c", "C":
		m.DirtyPromptChoice = 2
		m.dirtyPromptCancel()
		return nil
	}
	return nil
}

// buildEditorContextMenu creates the menu shown on right-click in
// the editor. Items are smart: presence depends on whether the
// cursor is on a diagnostic, on a symbol, on a selection, or just on
// empty text. The menu's actions wire into existing LSP / editing
// infrastructure.
func (m *App) buildEditorContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return nil
	}
	st := &widgets.ContextMenuState{Title: "Editor"}

	// Cut/Copy/Paste (no section label: the popup title "Editor"
	// already provides context, and the items speak for themselves)
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		Items: []widgets.ContextMenuItem{
			{Label: "Copy", Detail: "Ctrl+C", Disabled: !m.EditorState.Editor.HasSelection(), Action: func() {
				m.copyEditorSelection()
			}},
			{Label: "Cut", Detail: "Ctrl+X", Disabled: !m.EditorState.Editor.HasSelection(), Action: func() {
				m.cutEditorSelection()
			}},
			{Label: "Paste", Detail: "Ctrl+V", Action: func() {
				m.pasteIntoEditor()
			}},
		},
	})

	// Selection-aware items
	selSection := widgets.ContextMenuSection{SectionLabel: "Selection"}
	selItems := []widgets.ContextMenuItem{}
	if m.EditorState.Editor.HasSelection() {
		selItems = append(selItems, widgets.ContextMenuItem{
			Label: "Format selection", Action: func() {
				// Best-effort: rely on Save+format-on-save for now.
				// This is a placeholder for a future range-format
				// hook.
			},
		})
	}
	selSection.Items = selItems
	if len(selItems) > 0 {
		st.Sections = append(st.Sections, selSection)
	}

	// Diagnostic- and symbol-aware items.
	// Get the line/col under the cursor.
	cx, cy := m.EditorState.Editor.CursorScreenPosition(0, 0)
	_ = cx
	_ = cy
	// Use the click position relative to the editor to find the
	// (line, col) — but for simplicity we just use the current
	// cursor position. Future: translate mouse X/Y to (line, col).
	pos := struct{ Line, Col int }{Line: m.EditorState.Editor.CursorLine(), Col: m.EditorState.Editor.CursorCol()}
	// Use the diagnostics helper on the editor. We don't expose
	// it, but the editor has a diagnosticsAt function via
	// SetDiagnostics/LSPDiagnostic; we just need the existence
	// check.
	diags := m.EditorState.Editor.DiagnosticsAt(pos.Line, pos.Col)
	hasLSP := m.LSPMgr != nil && m.LSPMgr.ServerReady()

	lspSection := widgets.ContextMenuSection{SectionLabel: "Code"}
	if hasLSP {
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Go to definition", Detail: "Alt+.", Action: func() {
				_, _ = m.requestDefinitionCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Find references", Detail: "Alt+;", Action: func() {
				_, _ = m.requestReferencesCmd(), 0
			},
		})
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: "Rename symbol", Detail: "F2", Action: func() {
				m.startRename()
			},
		})
	}
	if len(diags) > 0 {
		lspSection.Items = append(lspSection.Items, widgets.ContextMenuItem{
			Label: fmt.Sprintf("Show %d diagnostic(s)", len(diags)), Action: func() {
				m.showDiagnosticAt(pos.Line, pos.Col)
			},
		})
	}
	if len(lspSection.Items) > 0 {
		st.Sections = append(st.Sections, lspSection)
	}

	// View actions.
	viewSection := widgets.ContextMenuSection{
		SectionLabel: "View",
		Items: []widgets.ContextMenuItem{
			{Label: "Find", Detail: "Ctrl+F", Action: func() {
				m.openFindReplace(false)
			}},
			{Label: "Find & replace", Detail: "Ctrl+H", Action: func() {
				m.openFindReplace(true)
			}},
			{Label: "Document outline", Detail: "Alt+O", Disabled: !hasLSP, Action: func() {
				_, _ = m.requestDocumentSymbolsCmd(), 0
			}},
		},
	}
	st.Sections = append(st.Sections, viewSection)

	return st
}

// buildSidebarContextMenu creates the menu for right-click on a file
// tree item.
func (m *App) buildSidebarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.Tree.Model == nil {
		return nil
	}
	st := &widgets.ContextMenuState{Title: "File"}

	// Get the path under the click.
	path := m.pathUnderMouse(mouse)
	isDir := path != "" && m.Tree.Model.IsDir(path)

	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Actions",
		Items: []widgets.ContextMenuItem{
			{Label: "Open", Disabled: path == "", Action: func() {
				if path != "" {
					_, _ = m.openFileFromTree(path)
				}
			}},
			{Label: "Reveal in tree", Disabled: path == "", Action: func() {
				if path != "" {
					m.Tree.Model.ExpandToPath(path)
				}
			}},
		},
	})

	createSection := widgets.ContextMenuSection{SectionLabel: "Create"}
	if isDir {
		createSection.Items = append(createSection.Items,
			widgets.ContextMenuItem{Label: "New file", Action: func() {
				m.startNewFileInTree(path)
			}},
			widgets.ContextMenuItem{Label: "New folder", Action: func() {
				m.startNewFolderInTree(path)
			}},
		)
	}
	if path != "" {
		createSection.Items = append(createSection.Items,
			widgets.ContextMenuItem{Label: "New file (sibling)", Action: func() {
				m.startNewFileInTree(m.Tree.Model.ParentDir(path))
			}},
			widgets.ContextMenuItem{Label: "New folder (sibling)", Action: func() {
				m.startNewFolderInTree(m.Tree.Model.ParentDir(path))
			}},
		)
	}
	if len(createSection.Items) > 0 {
		st.Sections = append(st.Sections, createSection)
	}

	if path != "" {
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Modify",
			Items: []widgets.ContextMenuItem{
				{Label: "Rename", Action: func() {
					m.startRenameInTree(path)
				}},
				{Label: "Delete", Action: func() {
					m.startDeleteInTree(path)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
			},
		})
	}

	return st
}

// buildTabBarContextMenu creates the menu for right-click on a tab.
func (m *App) buildTabBarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	if m.Win == nil || m.Win.TabCount() == 0 {
		return nil
	}
	adjustedX := mouse.X
	if m.Tree.Visible && m.Tree.Model != nil {
		adjustedX -= m.Tree.Width + 1
	}
	adjustedX -= m.Theme.Spacing.PadLR
	if adjustedX < 0 {
		adjustedX = 0
	}
	hit := m.Win.HitTest(adjustedX, 0)
	if hit.TabIndex < 0 || hit.TabIndex >= m.Win.TabCount() {
		return nil
	}
	tabs := m.Win.Tabs()
	tab := tabs[hit.TabIndex]

	st := &widgets.ContextMenuState{Title: "Tab"}

	// Get tab-specific info.
	if m.isEditorTabTitle(tab.Title) {
		path := m.editorPathFromTabTitle(tab.Title)
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []widgets.ContextMenuItem{
				{Label: "Close", Detail: "Ctrl+W", Action: func() {
					m.EditorState.ActiveView = path
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Close others", Action: func() {
					m.closeAllEditorTabsExcept(path)
				}},
				{Label: "Close to the right", Action: func() {
					m.closeEditorTabsToRightOf(hit.TabIndex)
				}},
				{Label: "Copy path", Action: func() {
					m.copyToClipboard(path)
				}},
				{Label: "Reveal in tree", Action: func() {
					if m.Tree.Model != nil {
						m.Tree.Model.ExpandToPath(path)
					}
				}},
			},
		})
	} else if m.isAgentTabTitle(tab.Title) {
		agentID := m.agentIDFromTabTitle(tab.Title)
		st.Sections = append(st.Sections, widgets.ContextMenuSection{
			SectionLabel: "Tab",
			Items: []widgets.ContextMenuItem{
				{Label: "Close", Action: func() {
					_ = m.handleAgentTabClose(hit.TabIndex)
				}},
				{Label: "Cancel agent", Action: func() {
					if m.ActiveSession.OnCancelSubAgent != nil {
						go m.ActiveSession.OnCancelSubAgent(agentID)
					}
				}},
			},
		})
	}

	return st
}

// buildInputContextMenu creates the menu for the chat input.
func (m *App) buildInputContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Input"}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Clipboard",
		Items: []widgets.ContextMenuItem{
			{Label: "Paste", Detail: "Ctrl+V", Action: func() {
				m.pasteIntoInput()
			}},
			{Label: "Copy input", Action: func() {
				m.copyToClipboard(m.Input.Value())
			}},
			{Label: "Clear input", Action: func() {
				m.Input.ClearInput()
			}},
		},
	})
	return st
}

// buildChatContextMenu creates the menu for right-click in the
// message list. (For now: clipboard + a copy-all option.)
func (m *App) buildChatContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Chat"}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		Items: []widgets.ContextMenuItem{
			{Label: "Copy selection", Action: func() {
				ml := m.activeMsgList()
				if ml != nil {
					text := ml.SelectedText()
					if text != "" {
						m.copyToClipboard(text)
					}
				}
			}},
			{Label: "Copy last assistant", Action: func() {
				ml := m.activeMsgList()
				if ml != nil {
					text := ml.LastAssistantText()
					if text != "" {
						m.copyToClipboard(text)
					}
				}
			}},
			{Label: "Copy last code block", Action: func() {
				ml := m.activeMsgList()
				if ml == nil {
					return
				}
				raw := ml.LastAssistantText()
				lang, code, ok := rendering.ExtractLastCodeBlock(raw)
				if !ok {
					m.StatusBar.SetInfo("No code block in last assistant message")
					return
				}
				if lang != "" {
					m.StatusBar.SetInfo(fmt.Sprintf("Copied %s code block (%d chars)", lang, len(code)))
				} else {
					m.StatusBar.SetInfo(fmt.Sprintf("Copied code block (%d chars)", len(code)))
				}
				if err := clipboard.WriteText(code); err != nil {
					m.StatusBar.SetError("clipboard error: " + err.Error())
				}
			}},
		},
	})
	return st
}

// buildStatusBarContextMenu creates the menu for right-click on the
// bottom status bar. Items surface LSP controls, status-line copy,
// and a quick "go to first diagnostic" action.
func (m *App) buildStatusBarContextMenu(mouse tea.Mouse) *widgets.ContextMenuState {
	st := &widgets.ContextMenuState{Title: "Status Bar"}

	// LSP section. Always shown when an workspace.LSPManager exists; items
	// disable themselves when not applicable.
	if m.LSPMgr != nil {
		var lspItems []widgets.ContextMenuItem

		isConfigured := m.LSPMgr.IsConfigured()
		serverReady := m.LSPMgr.ServerReady()
		ins := m.LSPMgr.ProjectInsights()
		hasErrors := ins.Errors > 0

		lspItems = append(lspItems, widgets.ContextMenuItem{
			Label:    "Restart LSP",
			Detail:   "kill+relaunch",
			Disabled: !isConfigured,
			Action: func() {
				m.StopLSP()
				m.startLSPForWorkspace()
				m.StatusBar.SetInfo("LSP restarting…")
			},
		})

		if serverReady {
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label:  "Stop LSP",
				Detail: "free port",
				Action: func() {
					m.StopLSP()
					m.StatusBar.SetInfo("LSP stopped")
				},
			})
		} else if isConfigured {
			// Server was configured but not connected (e.g. failed
			// or still starting). Offer to start it.
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label: "Start LSP",
				Action: func() {
					m.startLSPForWorkspace()
					m.StatusBar.SetInfo("LSP starting…")
				},
			})
		}

		if hasErrors {
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label:  "Goto first error",
				Detail: "F8",
				Action: func() {
					m.gotoFirstDiagnostic()
				},
			})
		}

		if ins.FilesTracked > 0 {
			summary := fmt.Sprintf("%s · ✕%d ⚠%d ℹ%d · %d files · %d symbols",
				ins.ServerName, ins.Errors, ins.Warnings,
				ins.Info+ins.Hints, ins.FilesAffected, ins.SymbolsCount)
			lspItems = append(lspItems, widgets.ContextMenuItem{
				Label: "Copy diagnostics summary",
				Action: func() {
					m.copyToClipboard(summary)
				},
			})
		}

		if len(lspItems) > 0 {
			st.Sections = append(st.Sections, widgets.ContextMenuSection{
				SectionLabel: "LSP",
				Items:        lspItems,
			})
		}
	}

	// Status section. Always available.
	statusItems := []widgets.ContextMenuItem{
		{
			Label: "Copy status line",
			Action: func() {
				if m.StatusBar != nil {
					m.copyToClipboard(m.StatusBar.View())
				}
			},
		},
	}
	st.Sections = append(st.Sections, widgets.ContextMenuSection{
		SectionLabel: "Status",
		Items:        statusItems,
	})

	return st
}

// closeContextMenu removes the open context menu, if any.
func (m *App) closeContextMenu() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("context-menu")
	}
	m.LastContextMenu = nil
	m.dirty = true
}

// findFlatItemIndex returns the index in FlatItems for the item at
// (sectionIdx, itemIdx). Returns false if the item is disabled (and
// therefore not in FlatItems).
func findFlatItemIndex(st *widgets.ContextMenuState, sectionIdx, itemIdx int) (int, bool) {
	for i := 0; i < len(st.FlatItems); i++ {
		si, ii, ok := st.FlatItemAt(i)
		if ok && si == sectionIdx && ii == itemIdx {
			return i, true
		}
	}
	return 0, false
}

// handleContextMenuKey routes keypresses to the context menu. Up/
// Down navigate items, Enter activates, Esc dismisses.
func (m *App) handleContextMenuKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil {
		return nil
	}
	st := m.LastContextMenu
	if st == nil {
		m.closeContextMenu()
		return nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		m.closeContextMenu()
		return nil
	case tea.KeyEnter:
		st.Activate()
		m.closeContextMenu()
		return nil
	case tea.KeyDown:
		st.Next()
		m.refreshContextMenu()
		return nil
	case tea.KeyUp:
		st.Prev()
		m.refreshContextMenu()
		return nil
	}
	return nil
}

// refreshContextMenu re-renders the context menu popup after the
// cursor moved.
func (m *App) refreshContextMenu() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window("context-menu")
	if fw == nil || m.LastContextMenu == nil {
		return
	}
	lines := widgets.ContextMenuLines(m.Theme, m.LastContextMenu, 0)
	pop := widgets.NewPopupContent(lines)
	fw.SetContent(pop)
	fw.MarkContentDirty()
	m.dirty = true
}

// handleTreePromptKey routes keypresses to the focused tree name
// input popup (new file/folder, rename). Backspace, arrows, and
// printable characters edit the value; Enter submits; Esc cancels.
func (m *App) handleTreePromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.TreePrompt == nil || m.windowManager == nil {
		return nil
	}
	st := m.TreePrompt
	switch msg.Code {
	case tea.KeyEsc:
		if st.OnCancel != nil {
			st.OnCancel()
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyEnter:
		if st.OnSubmit != nil {
			if err := st.OnSubmit(st.Value); err != nil {
				m.StatusBar.SetError(err.Error())
				m.dirty = true
				return nil
			}
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyBackspace:
		if st.Cursor > 0 && len(st.Value) > 0 {
			st.Value = st.Value[:st.Cursor-1] + st.Value[st.Cursor:]
			st.Cursor--
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyDelete:
		if st.Cursor < len(st.Value) {
			st.Value = st.Value[:st.Cursor] + st.Value[st.Cursor+1:]
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyLeft:
		if st.Cursor > 0 {
			st.Cursor--
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyRight:
		if st.Cursor < len(st.Value) {
			st.Cursor++
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyHome:
		st.Cursor = 0
		m.refreshTreePrompt()
		return nil
	case tea.KeyEnd:
		st.Cursor = len(st.Value)
		m.refreshTreePrompt()
		return nil
	}
	if msg.Text != "" {
		st.Value = st.Value[:st.Cursor] + msg.Text + st.Value[st.Cursor:]
		st.Cursor += len(msg.Text)
		m.refreshTreePrompt()
	}
	return nil
}

func (m *App) closeTreePrompt() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-prompt")
	}
	m.TreePrompt = nil
	m.dirty = true
}

func (m *App) refreshTreePrompt() {
	if m.windowManager == nil || m.TreePrompt == nil {
		return
	}
	fw := m.windowManager.Window("tree-prompt")
	if fw == nil {
		return
	}
	lines := widgets.TextPromptLines(m.Theme, m.TreePrompt)
	pop := widgets.NewPopupContent(lines)
	fw.SetContent(pop)
	fw.MarkContentDirty()
	m.dirty = true
}

// handleTreeDeleteConfirmKey is the keyboard handler for the
// destructive confirm popup (delete file/folder). ←/↑ = Yes,
// →/↓ = No, Enter = activate focused, Esc = cancel.
func (m *App) handleTreeDeleteConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return nil
	}
	path := m.TreeDeletePending
	switch msg.Code {
	case tea.KeyEsc:
		m.cancelTreeDelete()
		return nil
	case tea.KeyEnter:
		m.confirmTreeDelete()
		return nil
	case tea.KeyLeft, tea.KeyUp:
		m.StatusBar.SetInfo("press enter to confirm delete of " + filepath.Base(path))
		return nil
	case tea.KeyRight, tea.KeyDown:
		m.cancelTreeDelete()
		return nil
	}
	return nil
}

func (m *App) confirmTreeDelete() {
	path := m.TreeDeletePending
	m.TreeDeletePending = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
	}
	if path == "" {
		return
	}
	isDir := m.Tree.Model.IsDir(path)
	var err error
	if isDir {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		m.StatusBar.SetError("delete " + filepath.Base(path) + ": " + err.Error())
		m.dirty = true
		return
	}
	m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
		Type: filetree.FileDeleted, Path: path,
	})
	// If the deleted file was open in an editor, close the tab.
	if tab, ok := m.EditorState.Tabs[path]; ok {
		m.closeEditorTabForce(tab.Path)
	}
	m.StatusBar.SetInfo("Deleted " + filepath.Base(path))
	m.dirty = true
}

func (m *App) cancelTreeDelete() {
	m.TreeDeletePending = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
	}
	m.dirty = true
}

// showContextMenuAt opens a context menu at the given screen
// position. Used by the Ctrl+. keyboard shortcut.
func (m *App) showContextMenuAt(x, y int) {
	msg := tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight}
	_, _ = m.dispatchMouseRightClick(msg)
}

// pathUnderMouse resolves a file tree path from a screen Y.
func (m *App) pathUnderMouse(mouse tea.Mouse) string {
	if m.Tree.Model == nil {
		return ""
	}
	visible := m.Tree.Model.VisibleRows()
	if visible == nil {
		return ""
	}
	row := mouse.Y - m.Tree.YOffset
	if row < 0 || row >= len(visible) {
		return ""
	}
	return visible[row]
}

// copyEditorSelection copies the active selection to the system
// clipboard. Falls back silently if there's no selection.
func (m *App) copyEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
}

// cutEditorSelection copies and deletes the selection.
func (m *App) cutEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
	m.EditorState.Editor.DeleteSelection()
	m.dirty = true
}

// pasteIntoEditor pastes from the system clipboard at the cursor.
// Reads the clipboard synchronously; this is fast (sub-10ms) on all
// supported platforms and the clipboard package is mutex-locked.
func (m *App) pasteIntoEditor() {
	if m.EditorState.Editor == nil {
		return
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.StatusBar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	m.EditorState.Editor.HandleEvent(tea.PasteMsg{Content: content})
	m.StatusBar.SetInfo(fmt.Sprintf("Pasted %d chars", len(content)))
	m.dirty = true
}

// pasteIntoInput pastes into the chat input. Reads the clipboard
// synchronously and forwards the result through the input's own
// paste handler so large pastes are properly offloaded to refs.
func (m *App) pasteIntoInput() {
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		m.StatusBar.SetError("clipboard is empty or unavailable")
		return
	}
	content := string(data)
	canType := !m.OverlayState.IsBlocking()
	updated, _, _ := m.Input.Update(tea.PasteMsg{Content: content},
		canType, m.OverlayState.IsBlocking(), m.ChatAreaHeight, m.Input.ShowHint())
	m.Input = updated
	m.StatusBar.SetInfo(fmt.Sprintf("Pasted %d chars into input", len(content)))
	m.dirty = true
}

// copyToClipboard copies a string to the system clipboard via the
// real platform layer (Unix wl-copy/pbcopy/xclip/termux, Windows
// Win32). Shows a status bar message on success or error.
func (m *App) copyToClipboard(s string) {
	if s == "" {
		return
	}
	if err := clipboard.WriteText(s); err != nil {
		m.StatusBar.SetError("clipboard error: " + err.Error())
		return
	}
	m.StatusBar.SetInfo(fmt.Sprintf("Copied %d chars", len(s)))
}

// showDiagnosticAt opens the diagnostic hover popup for the
// position, if there's a diagnostic.
func (m *App) showDiagnosticAt(line, col int) {
	diags := m.EditorState.Editor.DiagnosticsAt(line, col)
	if len(diags) == 0 {
		return
	}
	// Build inner content: one row per diagnostic message.
	var content []string
	for _, d := range diags {
		content = append(content, d.Message)
	}
	// Wrap in unified chrome.
	lines := widgets.PopupChromeLines(m.Theme, widgets.PopupChromeConfig{
		Title:    "Diagnostic",
		TitleIcon: "ℹ",
		Width:    60,
		Focused:  true,
	}, content)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow("diag-context", "", pop, 60, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(false),
		wm.WithFocusable(true),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("diag-context")
		m.windowManager.AddWindow(fw)
	}
}

// openFileFromTree opens a file in the editor when triggered from
// the file tree context menu.
func (m *App) openFileFromTree(path string) (tea.Cmd, error) {
	return m.openEditorTab(path)
}

// startNewFileInTree starts a small prompt for naming a new file
// inside the given directory. Opens a focused text-input popup
// above the active editor. On Enter, the file is created (empty)
// and the tree is updated.
func (m *App) startNewFileInTree(dir string) {
	m.openTreeTextPrompt(widgets.TextPromptNewFile, "New file",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("already exists: %s", value)
			}
			f, err := os.Create(full)
			if err != nil {
				return err
			}
			_ = f.Close()
			m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			m.StatusBar.SetInfo("Created " + value)
			// Open the new file in the editor so the user can
			// start typing immediately.
			if _, err := m.openEditorTab(full); err != nil {
				m.StatusBar.SetError("open " + value + ": " + err.Error())
			}
			return nil
		})
}

// startNewFolderInTree starts a small prompt for naming a new
// folder inside the given directory. On Enter, the folder is
// created and the tree is updated.
func (m *App) startNewFolderInTree(dir string) {
	m.openTreeTextPrompt(widgets.TextPromptNewFolder, "New folder",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("already exists: %s", value)
			}
			if err := os.MkdirAll(full, 0o755); err != nil {
				return err
			}
			m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			// Expand the parent so the new folder is visible.
			m.Tree.Model.ExpandToPath(full)
			m.StatusBar.SetInfo("Created folder " + value)
			return nil
		})
}

// startRenameInTree starts a rename prompt prefilled with the
// current name of the path. On Enter, the file or folder is
// renamed; if it was open in an editor, the tab key is updated
// to follow the new path.
func (m *App) startRenameInTree(path string) {
	if path == "" {
		return
	}
	base := filepath.Base(path)
	m.openTreeTextPrompt(widgets.TextPromptRename, "Rename",
		path, "", base, func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			if value == base {
				return fmt.Errorf("name unchanged")
			}
			dir := filepath.Dir(path)
			newPath := filepath.Join(dir, value)
			if _, err := os.Stat(newPath); err == nil {
				return fmt.Errorf("target already exists: %s", value)
			}
			if err := os.Rename(path, newPath); err != nil {
				return err
			}
			m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileRenamed, OldPath: path, Path: newPath,
			})
			// If the file was open in an editor tab, retarget
			// the tab to the new path so the user keeps their
			// cursor/scroll/undo.
			if tab, ok := m.EditorState.Tabs[path]; ok {
				delete(m.EditorState.Tabs, path)
				tab.Path = newPath
				m.EditorState.Tabs[newPath] = tab
			}
			// Update the active editor pointer if the renamed
			// file is currently active.
			if m.EditorState.ActiveView == path {
				m.EditorState.ActiveView = newPath
			}
			m.StatusBar.SetInfo(fmt.Sprintf("Renamed to %s", value))
			m.dirty = true
			return nil
		})
}

// startDeleteInTree starts a destructive-action confirmation. For
// directories the warning is more emphatic; for files it's brief.
// On Yes, the file or directory is removed, the tree updates, and
// any open editor tab for the file is closed.
func (m *App) startDeleteInTree(path string) {
	if path == "" {
		return
	}
	isDir := m.Tree.Model.IsDir(path)
	base := filepath.Base(path)

	var question, detail string
	if isDir {
		question = fmt.Sprintf("Delete folder %q and all its contents?", base)
		detail = path
	} else {
		question = fmt.Sprintf("Delete %q?", base)
		detail = path
	}
	lines := widgets.ConfirmPrompt(m.Theme,
		"Delete",
		question,
		detail,
		widgets.ConfirmPromptDanger,
		false,
		60,
	)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow("tree-delete-confirm", "", pop,
		60, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
		m.windowManager.AddWindow(fw)
	}
	m.TreeDeletePending = path
	m.dirty = true
}

// openTreeTextPrompt opens the focused text-input popup used by
// new-file / new-folder / rename. Centralizes the popup creation
// so the three callers stay small. The submit callback returns a
// non-nil error to surface validation issues; the popup stays open
// in that case so the user can fix the name.
func (m *App) openTreeTextPrompt(kind widgets.TextPromptKind, title, prompt, dir, initial string, onSubmit func(value string) error) {
	if onSubmit == nil {
		return
	}
	st := &widgets.TextPromptState{
		Title:        title,
		Prompt:       prompt,
		Kind:         kind,
		InitialValue: initial,
		Value:        initial,
		Cursor:       len(initial),
		Focused:      true,
		OnSubmit:     onSubmit,
		OnCancel:     func() {},
	}
	lines := widgets.TextPromptLines(m.Theme, st)
	pop := widgets.NewPopupContent(lines)
	// TextPromptLines now self-renders the unified popup chrome.
	fw := wm.NewFloatingWindow("tree-prompt", "", pop,
		widgets.TextPromptPopupWidth(st),
		widgets.TextPromptPopupHeight(st),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-prompt")
		m.windowManager.AddWindow(fw)
	}
	m.TreePrompt = st
	m.dirty = true
	_ = dir // dir is captured via closure in the caller
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

// closeAllEditorTabsExcept closes every editor tab except the one
// at the given path.
func (m *App) closeAllEditorTabsExcept(path string) {
	toClose := []string{}
	for p := range m.EditorState.Tabs {
		if p != path {
			toClose = append(toClose, p)
		}
	}
	for _, p := range toClose {
		m.closeEditorTabForce(p)
	}
}

// closeEditorTabsToRightOf closes every tab whose index is greater
// than the given window-tab index.
func (m *App) closeEditorTabsToRightOf(idx int) {
	tabs := m.Win.Tabs()
	for i := len(tabs) - 1; i > idx; i-- {
		t := tabs[i]
		if m.isEditorTabTitle(t.Title) {
			p := m.editorPathFromTabTitle(t.Title)
			m.closeEditorTabForce(p)
		}
	}
}

// startRename arms the rename flow.
func (m *App) startRename() {
	_, _ = m.startRenameCmd(), 0
}

// refreshDirtyPrompt rebuilds the dirty-close popup's content lines
// to reflect the current focus state.
func (m *App) refreshDirtyPrompt() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return
	}
	tab, ok := m.EditorState.Tabs[m.DirtyPromptPath]
	if !ok {
		return
	}
	filename := tab.Editor.Title()
	detail := fmt.Sprintf("'%s' has unsaved changes.", filename)
	lines := widgets.ConfirmPrompt3Button(m.Theme,
		"Unsaved changes",
		"What do you want to do with the unsaved changes?",
		detail,
		widgets.ConfirmPromptWarning,
		m.DirtyPromptChoice,
		62,
	)
	pop := widgets.NewPopupContent(lines)
	fw.SetSize(62, len(lines))
	fw.SetContent(pop)
	fw.MarkContentDirty()
}

// pasteIntoFindReplace reads the system clipboard and inserts the
// content into the focused find/replace field. The text is inserted
// at the cursor position (or replaces the active selection if any).
// Returns nil (the read happens eagerly to keep the popup's response
// synchronous and the cursor position updateable on the same frame
// as the keypress).
func (m *App) pasteIntoFindReplace(fr *widgets.FindReplaceState) tea.Cmd {
	if fr == nil {
		return nil
	}
	data := clipboard.Read(clipboard.FmtText)
	if len(data) == 0 {
		return nil
	}
	text := string(data)
	input := fr.ActiveInput()
	if input == nil {
		return nil
	}
	// If there's a selection, replace it; otherwise insert at cursor.
	if input.HasActiveSelection() {
		s, e := input.SelectionRange()
		input.DeleteRange(s, e)
	}
	input.InsertString(text)
	if fr.OnChange != nil {
		fr.OnChange()
	}
	return nil
}

// handleFindReplaceKey routes keypresses when the find/replace popup
// is focused. The text-editing keys (characters, Backspace, Delete,
// Left/Right, Home/End, Ctrl+Left/Right, Ctrl+A/E/U/K/W) are
// delegated to the embedded components.TextInput — so all the
// features that work in the chat input work here too (real selection,
// rune-safe cursor, word motion, etc.). The popup-level keys
// (Esc, Tab, Enter, Alt+C/W/R, Ctrl+Alt+Enter, Shift+Enter, Ctrl+C/V)
// are handled here.
func (m *App) handleFindReplaceKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.FindReplace == nil {
		return nil
	}
	fr := m.FindReplace

	// Navigation/modifier keys first.
	switch msg.Code {
	case tea.KeyEsc:
		m.closeFindReplace()
		return nil
	case tea.KeyTab:
		if msg.Mod.Contains(tea.ModShift) {
			fr.FocusedField = 0
		} else {
			fr.FocusedField = 1 - fr.FocusedField
		}
		m.refreshFindReplacePopup()
		return nil
	case tea.KeyEnter:
		if fr.FocusedField == 1 && fr.ReplaceMode {
			// Replace the current match.
			if fr.OnReplace != nil {
				fr.OnReplace()
			}
			return nil
		}
		// Default: next match.
		if fr.OnNext != nil {
			fr.OnNext()
		}
		return nil
	}

	// Toggle shortcuts.
	switch msg.String() {
	case "alt+c":
		fr.CaseSensitive = !fr.CaseSensitive
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "alt+w":
		fr.WholeWord = !fr.WholeWord
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "alt+r":
		fr.Regex = !fr.Regex
		if fr.OnChange != nil {
			fr.OnChange()
		}
		return nil
	case "ctrl+alt+enter":
		if fr.OnReplaceAll != nil {
			fr.OnReplaceAll()
		}
		return nil
	case "shift+enter":
		if fr.OnPrev != nil {
			fr.OnPrev()
		}
		return nil
	}

	// MS Edit shortcuts. Now that the popup uses real TextInput
	// (with selection state), these work the same as in every
	// other text field in the app: Ctrl+C copies the selection (or
	// the whole field if no selection), Ctrl+X cuts, Ctrl+A
	// selects all, Ctrl+V pastes.
	switch msg.String() {
	case "ctrl+c":
		// Copy: if there's a selection, copy that. Otherwise, copy
		// the whole field (matches VSCode's find-dialog behavior).
		input := fr.ActiveInput()
		if input == nil {
			return nil
		}
		var text string
		if input.HasActiveSelection() {
			text = input.SelectedText()
		} else {
			text = input.Value()
		}
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}
		return nil
	case "ctrl+x":
		// Cut: copy selection (or whole field) and delete it.
		input := fr.ActiveInput()
		if input == nil {
			return nil
		}
		var text string
		if input.HasActiveSelection() {
			text = input.SelectedText()
			s, e := input.SelectionRange()
			input.DeleteRange(s, e)
		} else {
			text = input.Value()
			input.Clear()
		}
		if fr.OnChange != nil {
			fr.OnChange()
		}
		if text != "" {
			return rendering.CopyToClipboardCmd(text)
		}
		return nil
	case "ctrl+a":
		// Select all: anchor=0, end=length. TextInput handles this
		// via the default key handler (Ctrl+A moves to col 0), so
		// we route it explicitly to set a real selection.
		input := fr.ActiveInput()
		if input != nil {
			input.SetSelection(0, input.Len())
		}
		m.refreshFindReplacePopup()
		return nil
	case "ctrl+v":
		// Paste: read clipboard, insert at cursor (replacing
		// selection if present). Synchronous read keeps the
		// popup's response on the same frame as the keypress.
		return m.pasteIntoFindReplace(fr)
	}

	// All other keys: delegate to the focused input's TextInput.
	// This gives us character insertion, Backspace, Delete, Left/Right,
	// Home/End, Ctrl+Left/Right (word motion), Ctrl+E/U/K/W (line ops),
	// Up/Down (history-like, harmless for single-line fields), etc.
	input := fr.ActiveInput()
	if input != nil {
		oldVal := input.Value()
		updated, cmd := input.Update(msg, false)
		*input = updated
		if input.Value() != oldVal && fr.OnChange != nil {
			fr.OnChange()
		}
		return cmd
	}
	return nil
}

func (m *App) activeEditorPath() string {
	return m.EditorState.ActiveView
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

// handleLSPDiagnosticsUpdate processes a diagnostics update that arrived
// through the workspace.EventBus from the LSP server's reader goroutine. Runs on the
// main Bubble Tea goroutine, so it can safely update the tree model and editor.
func (m *App) handleLSPDiagnosticsUpdate(msg workspace.LSPDiagnosticsMsg) {
	if m.Tree.Model != nil {
		errs, warns, hints := 0, 0, 0
		for _, d := range msg.Diagnostics {
			switch d.Severity {
			case editor.DiagError:
				errs++
			case editor.DiagWarning:
				warns++
			default:
				hints++
			}
		}
		m.Tree.Model.SetDiagnostics(msg.FilePath, errs, warns, hints)
	}
	if msg.FilePath == m.EditorState.Editor.FilePath() && m.EditorState.Editor.IsOpen() {
		m.EditorState.Editor.SetDiagnostics(msg.Diagnostics)
	}
}

// handleFileWatcherEvent processes a file system change that arrived through
// the workspace.EventBus from the file watcher goroutine. Forwards the change to the
// LSP server for re-analysis.
func (m *App) handleFileWatcherEvent(msg workspace.FileWatcherMsg) {
	if m.LSPMgr == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	m.LSPMgr.NotifyWatchedFilesChanged(ctx, msg.FilePath, msg.ChangeType)
	m.LSPMgr.SyncFileFromDisk(ctx, msg.FilePath, m.EditorState.Editor.FilePath(), msg.ChangeType)
}

// startLSPForWorkspace scans the working directory and starts LSP
// servers for detected project roots.
func (m *App) startLSPForWorkspace() {
	wsDir, err := os.Getwd()
	if err != nil {
		wsDir = "."
	}
	if m.Logger != nil {
		m.Logger.Debug("LSP", "starting_for_workspace", "dir", wsDir)
	}
	if m.LSPMgr != nil {
		// Clear cached diagnostics on every (re)start so a stopped
		// server doesn't leave stale errors visible until the new
		// session republishes them.
		m.LSPMgr.ClearDiagnostics()
		// Also clear per-editor diagnostic caches for open tabs.
		// The new server will repopulate them via the onDiagnostics
		// callback as it walks the project.
		m.clearOpenEditorsDiagnostics()
		m.LSPMgr.StartForWorkspace(wsDir)
	}
}

// clearOpenEditorsDiagnostics wipes the diagnostic cache for every
// open editor tab. Called on LSP restart to avoid showing stale
// errors from the previous server session.
func (m *App) clearOpenEditorsDiagnostics() {
	if m.EditorState.Editor != nil {
		m.EditorState.Editor.SetDiagnostics(nil)
	}
	for _, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor != nil {
			tab.Editor.SetDiagnostics(nil)
		}
	}
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

	win := wm.NewFloatingWindow("filepicker", "", &m.FilePicker,
		60, 20,
		wm.WithDecoration(false),
		wm.WithClosable(false),
		wm.WithResizable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithMinSize(32, 10),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	wm.CenterOnScreen(win, m.width, m.height)
	m.windowManager.AddWindow(win)
}

// StopLSP shuts down all LSP server connections.
func (m *App) StopLSP() {
	if m.LSPMgr != nil {
		m.LSPMgr.Stop()
	}
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
			m.dirty = true
			// If no tabs left, show launcher
			if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && !m.Win.IsOpen() {
				if m.OnListSessions != nil {
					m.launcher = NewLauncherState(m.OnListSessions())
					m.LastInteractionTime = time.Now()
				}
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
func (m *App) openOrchestratorSettings() {
	m.Settings.SaveSnapshot()
	m.Settings.OpenCategory("default")
	m.setMode(ModeSettings)
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
	if m.ActiveSession.OnOrchestratorReset != nil {
		go m.ActiveSession.OnOrchestratorReset(id)
		m.StatusBar.SetInfo("Orchestrator overrides reset")
	}
}

// closeAllWindows removes all floating windows.
func (m *App) closeAllWindows() {
	for _, w := range m.windowManager.Windows() {
		m.windowManager.RemoveWindow(w.ID)
	}
}
