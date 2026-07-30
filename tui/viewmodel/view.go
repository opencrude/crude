package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltree"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

// mainViewKind identifies which product surface the main-content ANSI cache
// was built for. Used to refuse serving a launcher/chat cache while the
// editor is active (and vice versa).
type mainViewKind uint8

const (
	mainViewNone mainViewKind = iota
	mainViewEditor
	mainViewLauncher
	mainViewChat
)

func (m *App) currentMainViewKind() mainViewKind {
	if m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
		return mainViewEditor
	}
	if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		return mainViewLauncher
	}
	return mainViewChat
}

func (m *App) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}

	isDragging := m.windowManager.IsDragging()
	wmNeedsRender := m.windowManager.NeedsRender()

	// Throttle: during drag, if no position change since last render,
	// return cached output without re-compositing.
	if isDragging && !wmNeedsRender {
		v := tea.NewView(m.cachedOutput)
		v.AltScreen = true
		v.MouseMode = tea.MouseModeAllMotion
		return v
	}

	// Only cache the main content when the editor is the active view —
	// chroma syntax highlighting is expensive. Agent tabs use the fresh
	// main-view path, while the native launcher surface is painted every
	// frame so cursor, hover, scroll, and animation state stay responsive.
	isEditorMain := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	currentKind := m.currentMainViewKind()
	// Defensive: never sit on a blank/stale frame waiting for cursor blink.
	// Force rebuild when editorRect was never laid out, the cache belongs to
	// a different view kind, or the cached editor path is not the active tab.
	forceMainRebuild := false
	if isEditorMain && m.editorRect.Empty() {
		forceMainRebuild = true
	}
	if m.mainContentCached != "" && m.mainContentCacheKind != currentKind {
		forceMainRebuild = true
	}
	if isEditorMain && m.mainContentCacheKind == mainViewEditor &&
		m.mainContentCacheEditorPath != "" &&
		m.mainContentCacheEditorPath != m.EditorState.ActiveView {
		forceMainRebuild = true
	}
	useCached := isEditorMain && m.sessionDeckHeight(m.height) == 0 && !m.contentDirty && !forceMainRebuild && m.mainContentCached != "" && m.mainContentSurf != nil

	m.compositor.BeginFrame()
	// Fill the root with the active theme background so uncovered cells
	// (launcher holes, padding rows, transparent blits) follow the preset
	// instead of the terminal's default colour — otherwise light presets
	// leave dark strips where only header/status/sidebar filled explicitly.
	if m.Theme != nil {
		bg := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
		m.compositor.Root().Buffer().Fill(bg)
	}
	m.renderCtx.SpinFrame = m.SpinFrame
	m.syncRenderFocus()
	m.resizeLayerSurfaces()

	// Re-add persistent layer surfaces (BeginFrame clears children).
	root := m.compositor.Root()
	if m.mainContentSurf != nil {
		root.AddChild(m.mainContentSurf)
		m.mainContentSurf.SetZOrder(0)
	}
	if m.mainSessionDeckSurf != nil {
		root.AddChild(m.mainSessionDeckSurf)
		m.mainSessionDeckSurf.SetZOrder(1)
		m.mainSessionDeckSurf.SetVisible(m.sessionDeckHeight(m.height) > 0)
	}
	if m.mainLauncherSurf != nil {
		root.AddChild(m.mainLauncherSurf)
		m.mainLauncherSurf.SetZOrder(1)
	}
	if m.mainChatSurf != nil {
		root.AddChild(m.mainChatSurf)
		m.mainChatSurf.SetZOrder(1)
	}
	if m.mainEditorSurf != nil {
		root.AddChild(m.mainEditorSurf)
		m.mainEditorSurf.SetZOrder(1)
	}
	if m.mainSidebarSurf != nil {
		root.AddChild(m.mainSidebarSurf)
		m.mainSidebarSurf.SetZOrder(2)
		m.mainSidebarSurf.SetVisible(m.Tree.Visible)
	}
	if m.mainStatusbarSurf != nil {
		root.AddChild(m.mainStatusbarSurf)
		m.mainStatusbarSurf.SetZOrder(3)
	}
	if m.mainHeaderSurf != nil {
		root.AddChild(m.mainHeaderSurf)
		m.mainHeaderSurf.SetZOrder(4)
	}
	if m.mainTabBarSurf != nil {
		root.AddChild(m.mainTabBarSurf)
		m.mainTabBarSurf.SetZOrder(3)
		m.mainTabBarSurf.SetVisible(m.tabBarVisible())
	}
	if m.mainInputSurf != nil {
		root.AddChild(m.mainInputSurf)
		m.mainInputSurf.SetZOrder(2)
	}

	// Capture before buildMainView clears contentDirty — editor surface
	// reuses its cell buffer when only chrome (tree/tabs) changed.
	editorNeedsPaint := m.contentDirty || forceMainRebuild || m.mainEditorSurf == nil

	// Main content surface stays transparent — only ANSI-rendered cells
	// set explicit backgrounds. Transparent cells (like the top padding
	// row) show the terminal default, matching the original behaviour.
	if useCached {
		rendering.RenderToSurface(m.mainContentSurf, m.mainContentCached)
	} else {
		mainContent := m.buildMainView()
		m.mainContentCached = mainContent
		m.mainContentCacheKind = currentKind
		if currentKind == mainViewEditor {
			m.mainContentCacheEditorPath = m.EditorState.ActiveView
		} else {
			m.mainContentCacheEditorPath = ""
		}
		m.contentDirty = false
		rendering.RenderToSurface(m.mainContentSurf, mainContent)
	}
	m.renderSessionDeckToSurface()
	m.renderLauncherToSurface()
	m.renderChatToSurface()
	m.renderEditorToSurface(editorNeedsPaint)
	m.renderInputToSurface()

	// Cheap layers: always re-render (surface fill + ANSI render).
	m.renderHeaderToSurface()
	m.renderTabBarToSurface()
	if m.Tree.Visible {
		m.renderSidebarToSurface()
	}
	m.syncWorkspaceStatusView()
	m.renderStatusbarToSurface()

	m.windowManager.Render(m.compositor.Root(), m.renderCtx)
	if isDragging {
		m.windowManager.ClearNeedsRender()
	}

	m.compositor.Composite()

	output := m.compositor.Present()
	m.cachedOutput = output
	m.contentDirty = false

	v := tea.NewView(output)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// buildMainView renders the main TUI content (header, chat, input, status)
// as an ANSI string. The output is fed into the compositor's root surface
// via RenderToSurface. Floating windows are rendered on top separately by
// the compositor pipeline.
func (m *App) buildMainView() string {
	// Account for file tree sidebar when visible.
	sidebarW := 0
	sidebarVisible := m.Tree.Visible && m.Tree.Model != nil && m.sidebarScreen != nil
	if sidebarVisible {
		sidebarW = m.Tree.Width + 1 // +1 for divider
	}

	m.Win.SetWidth(m.width - 2*m.Theme.Spacing.PadLR - sidebarW)

	contentW := m.width - sidebarW
	innerW := contentW - m.Theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	isEditorActive := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	isLauncherActive := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && m.launcherScreen != nil
	showMainInput := !isLauncherActive && !isEditorActive && !m.OverlayState.IsOverlay()

	m.syncFormHint()

	var inputLineCount int
	if showMainInput {
		inputLineCount = m.Input.ShellHeight(contentW)
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	agentTabBarVisible := m.tabBarVisible()

	baseNonChat := 3 + inputLineCount
	if agentTabBarVisible {
		baseNonChat += 2
	}
	deckAvailable := m.height - 2 - baseNonChat - 1
	deckLineCount := m.sessionDeckHeight(max(0, deckAvailable))
	nonChat := baseNonChat + deckLineCount

	chatH := m.height - 2 - nonChat
	if chatH < 1 {
		chatH = 1
	}
	m.ChatAreaHeight = chatH
	deckY := 3 + chatH
	if agentTabBarVisible {
		deckY += 2
	}
	if deckLineCount > 0 {
		m.sessionDeckRect = ui.Rect{X: sidebarW + m.Theme.Spacing.PadLR, Y: deckY, W: innerW, H: deckLineCount}
	} else {
		m.sessionDeckRect = ui.Rect{}
	}

	var chatVisible []string

	if isEditorActive {
		editorPadL := m.Theme.Spacing.PadLR
		if sidebarVisible {
			editorPadL = 0
		}
		m.EditorState.Editor.SetPadding(editorPadL)
		m.EditorState.Editor.SetStatusChrome(false)
		// DocumentEditor paints into mainEditorSurf; reserve blank rows.
		chatVisible = make([]string, chatH)
		chatAreaTop := 3
		if agentTabBarVisible {
			chatAreaTop += 2
		}
		m.editorRect = ui.Rect{
			X: sidebarW,
			Y: chatAreaTop,
			W: contentW,
			H: chatH,
		}
		m.chatRect = ui.Rect{}
	} else if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		// The native launcher screen paints into its own surface; the main
		// content only needs to reserve the vertical space.
		chatVisible = make([]string, chatH)
		m.chatRect = ui.Rect{}
		m.editorRect = ui.Rect{}
	} else {
		// Agent chat paints into mainChatSurf; reserve blank placeholder rows
		// so the main ANSI string no longer embeds composed message content.
		chatVisible = make([]string, chatH)
		chatAreaTop := 3
		if agentTabBarVisible {
			chatAreaTop += 2
		}
		m.chatRect = ui.Rect{
			X: sidebarW,
			Y: chatAreaTop,
			W: contentW,
			H: chatH,
		}
		m.editorRect = ui.Rect{}
		// Prepare MessageList geometry for subpane render/export.
		if activeML := m.activeMessageList(); activeML != nil {
			activeML.SetChatAreaBounds(chatAreaTop, chatH)
			activeML.SetSize(m.width-sidebarW, m.height)
		}
	}

	// The main content surface is m.height - 2 rows (status bar is a
	// separate 2-row layer). Header and header separator are rendered
	// into the header layer surface (mainHeaderSurf) at position (0, 1)
	// and overlaid on top. Row 0 of the main content is the padding
	// row — it appears at the very top of the app.
	contentH := m.height - 2
	lines := make([]string, 0, contentH)

	// Top padding row (visible at the very top of the app).
	lines = append(lines, "")

	// Header placeholder row — blank space where the header overlay
	// surface (z=3) renders the header line.
	lines = append(lines, strings.Repeat(" ", contentW))

	// Header separator placeholder — blank space where the header
	// overlay renders the separator.
	lines = append(lines, strings.Repeat(" ", contentW))

	if agentTabBarVisible {
		lines = append(lines, strings.Repeat(" ", contentW))
		lines = append(lines, strings.Repeat(" ", contentW))
	}

	for _, line := range chatVisible {
		lines = append(lines, line)
	}

	for range deckLineCount {
		lines = append(lines, strings.Repeat(" ", contentW))
	}

	if showMainInput {
		inputY := 3 + chatH
		if agentTabBarVisible {
			inputY += 2
		}
		inputY += deckLineCount
		m.inputRect = ui.Rect{
			X: sidebarW,
			Y: inputY,
			W: contentW,
			H: inputLineCount,
		}
		for range inputLineCount {
			lines = append(lines, strings.Repeat(" ", contentW))
		}
	} else {
		m.inputRect = ui.Rect{}
	}

	// The status separator below is rendered by mainStatusbarSurf.

	// The sidebar is rendered into its own layer surface
	// (mainSidebarSurf). Update tree height/scroll state for it.
	if m.Tree.Visible && m.Tree.Model != nil {
		treeStart := 3
		m.Tree.YOffset = treeStart
		sidebarH := contentH - treeStart
		if sidebarH < 0 {
			sidebarH = 0
		}
		m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
	}

	if len(lines) > contentH {
		lines = lines[:contentH]
	}

	return strings.Join(lines, "\n")
}

func (m *App) renderSessionDeckToSurface() {
	if m.mainSessionDeckSurf == nil {
		return
	}
	projection := m.activeSessionDeck()
	r := m.sessionDeckRect
	if projection == nil || !projection.deck.IsOpen() || r.Empty() {
		m.mainSessionDeckSurf.SetVisible(false)
		return
	}
	m.mainSessionDeckSurf.SetVisible(true)
	m.sessionProjections.SetTheme(m.Theme)
	m.mainSessionDeckSurf.Resize(r.W, r.H)
	m.mainSessionDeckSurf.SetPosition(r.X, r.Y)
	bgCell := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
	m.mainSessionDeckSurf.Buffer().Fill(bgCell)
	projection.deckHost.Resize(r.W, r.H)
	projection.deckHost.Layout()
	m.focusSessionDeckAfterLayout()
	projection.deckHost.Paint(m.mainSessionDeckSurf.Buffer())
}

func (m *App) syncFormHint() {
	// Tab-bar / overlay hints are built in contextualHelpSegments.
}

// renderLauncherToSurface paints the native launcher component tree into its
// dedicated layer. It is called every frame (no caching) so animations and
// hover states stay responsive.
func (m *App) renderLauncherToSurface() {
	if m.mainLauncherSurf == nil {
		return
	}
	active := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == ""
	if !active || m.launcherScreen == nil || m.launcherHost == nil {
		m.mainLauncherSurf.SetVisible(false)
		return
	}

	layout := m.computeLayout()
	innerW := layout.InnerW
	chatH := m.ChatAreaHeight
	if innerW < 20 || chatH < 3 {
		m.mainLauncherSurf.SetVisible(false)
		return
	}

	m.launcherScreen.SetShortcuts(m.launcherShortcuts())
	m.launcherScreen.SetAnimation(m.SpinFrame, time.Since(m.LastInteractionTime).Seconds())

	m.mainLauncherSurf.SetVisible(true)
	m.mainLauncherSurf.Resize(innerW, chatH)
	m.mainLauncherSurf.SetPosition(layout.SidebarW+layout.PadLR, layout.ChatAreaTop)
	bgCell := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
	m.mainLauncherSurf.Buffer().Fill(bgCell)

	m.launcherHost.Resize(innerW, chatH)
	m.launcherHost.Layout()
	m.launcherScreen.SyncFocus()
	m.launcherHost.SetFrame(m.SpinFrame)
	m.launcherHost.Paint(m.mainLauncherSurf.Buffer())
}

func (m *App) renderInputToSurface() {
	if m.mainInputSurf == nil || m.inputScreen == nil || m.inputHost == nil {
		return
	}
	r := m.inputRect
	if r.Empty() {
		m.mainInputSurf.SetVisible(false)
		return
	}
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainInputSurf.Resize(r.W, r.H)
	m.mainInputSurf.SetPosition(r.X, r.Y)
	m.mainInputSurf.Buffer().Fill(bgCell)
	m.mainInputSurf.SetVisible(true)

	m.Input.SetSpinFrame(m.SpinFrame)
	m.inputScreen.Bind(&m.Input)
	m.inputScreen.SetWidth(r.W)
	m.inputHost.Resize(r.W, r.H)
	m.inputHost.SetFrame(m.SpinFrame)
	m.inputHost.Focus(shellinput.AreaID)
	m.inputHost.Layout()
	m.inputHost.Paint(m.mainInputSurf.Buffer())
}

// renderChatToSurface paints the agent message list into its dedicated layer.
// ScrollModel, scrollbar drag, and text selection live on shellchat;
// MessageList exports rendered message rows via chatprojection.
func (m *App) renderChatToSurface() {
	if m.mainChatSurf == nil {
		return
	}
	r := m.chatRect
	isEditorActive := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	isLauncher := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == ""
	if r.Empty() || isEditorActive || isLauncher || m.chatScreen == nil || m.chatHost == nil {
		m.mainChatSurf.SetVisible(false)
		return
	}

	activeML := m.activeMessageList()
	if activeML == nil {
		m.mainChatSurf.SetVisible(false)
		return
	}

	layout := m.computeLayout()
	innerW := layout.InnerW
	sidebarW := layout.SidebarW
	chatH := r.H

	activeAgent := m.agentForView()
	activeTyping := false
	if activeAgent != nil {
		activeTyping = activeAgent.ComputeActiveTyping()
	}

	nestingLevel := m.subPaneProjections.ScrollbarNestingLevel(m.paneDoneMap())
	paneSBW := nestingLevel*ui.ScrollbarWidth + nestingLevel
	if sp := m.PaneMgr.Get(m.AgentMgr.ActiveID()); sp != nil && m.subPaneProjections.Has(sp.ID) {
		m.subPaneProjections.SetPaneScrollbarWidth(sp.ID, paneSBW)
	}
	activeML.SetPaneScrollbarWidth(0)
	activeML.SetStreaming(activeAgent != nil && activeAgent.Streaming)
	activeML.SetSpinFrame(m.SpinFrame)
	activeML.SetActiveTyping(activeTyping)
	if m.StatusBar != nil {
		activeML.SetPhase(m.StatusBar.Phase())
	}
	activeML.SetChatAreaBounds(r.Y, chatH)
	activeML.SetSize(m.width-sidebarW, m.height)
	activeML.BindScroll(m.chatScreen.ScrollModel())

	activeAgentID := m.AgentMgr.ActiveID()
	switched := m.beginAgentChatScrollSwitch(activeAgentID)
	msgRows, paneLineMap, contentH := activeML.ExportMessageRows(innerW)
	m.finishAgentChatScrollMetrics(activeAgentID, chatH, contentH)
	if switched {
		msgRows, paneLineMap, contentH = activeML.ExportMessageRows(innerW)
	}

	mainSBVisible := contentH > chatH
	paneGutters := m.subPaneProjections.BuildPaneGutters(paneLineMap, m.PaneMgr)

	composeParams := shellchat.ComposeParams{
		InnerW:        innerW,
		PadLR:         layout.PadLR,
		Theme:         m.Theme,
		NestingLevel:  nestingLevel,
		MainSBVisible: mainSBVisible,
		PaneLineMap:   paneLineMap,
		PaneGutters:   paneGutters,
		HasPaneBg: func(paneID string) bool {
			sp := m.PaneMgr.Get(paneID)
			return sp != nil && sp.Type == workspace.PaneAgent
		},
	}

	paint := shellchat.PaintParams{Compose: composeParams, PaneLineMap: paneLineMap}
	if m.chatScreen != nil && m.chatScreen.HasActiveSelection() {
		paint.HasSelection = true
		paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol = m.chatScreen.SelectionRange()
	}
	m.chatScreen.SetMessages(msgRows, paint)

	m.mainChatSurf.Resize(r.W, r.H)
	m.mainChatSurf.SetPosition(r.X, r.Y)
	bgCell := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
	m.mainChatSurf.Buffer().Fill(bgCell)
	m.mainChatSurf.SetVisible(true)

	m.chatHost.Resize(r.W, r.H)
	m.chatHost.SetFrame(m.SpinFrame)
	m.chatHost.Layout()
	m.chatHost.Paint(m.mainChatSurf.Buffer())
}

// renderEditorToSurface paints DocumentEditor chrome into mainEditorSurf via
// shelleditor Host. When force is false, the previous cell buffer is kept
// (contentDirty cache for expensive chroma). Chrome-only frames must not force.
func (m *App) renderEditorToSurface(force bool) {
	if m.mainEditorSurf == nil {
		return
	}
	r := m.editorRect
	active := m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen()
	if !active || r.Empty() || m.editorScreen == nil || m.editorHost == nil {
		m.mainEditorSurf.SetVisible(false)
		return
	}

	sizeChanged := m.mainEditorSurf.Width() != r.W || m.mainEditorSurf.Height() != r.H
	m.mainEditorSurf.Resize(r.W, r.H)
	m.mainEditorSurf.SetPosition(r.X, r.Y)
	m.mainEditorSurf.SetVisible(true)

	if !force && !sizeChanged {
		return
	}

	editorPadL := m.Theme.Spacing.PadLR
	editorPadR := m.Theme.Spacing.PadLR
	if m.Tree.Visible {
		editorPadL = 0
	}
	m.EditorState.Editor.SetPadding(editorPadL)
	m.EditorState.Editor.SetStatusChrome(false)
	m.EditorState.Editor.SetTokens(m.editorHost.Tokens())

	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainEditorSurf.Buffer().Fill(bgCell)

	m.editorScreen.Bind(m.EditorState.Editor)
	m.editorScreen.SetPadding(editorPadL, editorPadR)
	m.editorScreen.SetFocused(m.focusTarget() == FocusEditor)
	m.editorHost.Resize(r.W, r.H)
	m.editorHost.SetFrame(m.SpinFrame)
	m.editorHost.Layout()
	m.editorHost.Paint(m.mainEditorSurf.Buffer())
}

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
		m.mainStatusbarSurf.SetZOrder(3)
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
		m.mainSidebarSurf.SetZOrder(2)
	} else if sidebarW > 0 {
		m.mainSidebarSurf.Resize(sidebarW, sidebarH)
	}
	m.mainSidebarSurf.SetPosition(0, m.Tree.YOffset)

	if m.mainHeaderSurf == nil {
		m.mainHeaderSurf = surface.NewSurface("main-header", m.width, 2, cell.TransparentBlank())
		m.compositor.Root().AddChild(m.mainHeaderSurf)
		m.mainHeaderSurf.SetZOrder(4)
	} else {
		m.mainHeaderSurf.Resize(m.width, 2)
	}
	// Header surface starts at Y=1 to leave room for the padding row
	// (row 0) which appears at the very top of the app.
	m.mainHeaderSurf.SetPosition(0, 1)

	tabVisible := m.tabBarVisible()
	if m.mainTabBarSurf == nil {
		m.mainTabBarSurf = surface.NewSurface("main-tabbar", contentW, 2, cell.TransparentBlank())
		m.compositor.Root().AddChild(m.mainTabBarSurf)
		m.mainTabBarSurf.SetZOrder(3)
	}
	if tabVisible {
		layout := m.computeLayout()
		m.mainTabBarSurf.Resize(layout.InnerW, 2)
		m.mainTabBarSurf.SetPosition(layout.SidebarW+layout.PadLR, 3)
	}
	m.mainTabBarSurf.SetVisible(tabVisible)
}

// renderSidebarToSurface paints the native file-tree component tree into the
// sidebar layer surface (tree panel + divider column).
func (m *App) renderSidebarToSurface() {
	if m.mainSidebarSurf == nil || m.sidebarScreen == nil || m.sidebarHost == nil || m.Tree.Model == nil {
		return
	}

	sidebarH := m.mainSidebarSurf.Height()
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainSidebarSurf.Buffer().Fill(bgCell)

	m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
	m.sidebarScreen.SetWidth(m.Tree.Width)
	m.sidebarScreen.SetVisible(m.Tree.Visible)

	sidebarW := m.Tree.Width + 1
	m.sidebarHost.Resize(sidebarW, sidebarH)
	m.sidebarHost.SetFrame(m.SpinFrame)
	m.sidebarHost.Layout()
	// Cursor chrome follows focusTarget so the tree does not look focused
	// while the launcher (or editor) owns keys.
	if m.focusTarget() == FocusTree {
		m.sidebarHost.Focus(shelltree.TreeID)
	} else {
		m.sidebarHost.Focus(ui.RootID)
	}
	m.sidebarHost.Paint(m.mainSidebarSurf.Buffer())
}

// renderStatusbarToSurface paints the native status bar into its layer surface.
func (m *App) renderStatusbarToSurface() {
	if m.mainStatusbarSurf == nil || m.StatusBar == nil || m.statusHost == nil {
		return
	}
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainStatusbarSurf.Buffer().Fill(bgCell)

	m.syncFormHint()
	prefix, segs := m.contextualHelpSegments()
	m.StatusBar.SetContextHelp(m.actionRegistry, prefix, segs)
	m.StatusBar.SetWidth(m.width)

	m.mainStatusbarSurf.SetVisible(true)
	m.statusHost.Resize(m.width, 2)
	m.statusHost.SetFrame(m.SpinFrame)
	m.statusHost.Layout()
	m.statusHost.Paint(m.mainStatusbarSurf.Buffer())
}

func (m *App) contextualHelpSegments() (prefix string, segs []ui.HintSegment) {
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil && fw.ID == findInFilesWindowID {
			return "", []ui.HintSegment{
				ui.TextSeg("type to search"),
				ui.ActionSeg("list.preview.navigate"),
				ui.ActionSeg("list.open"),
				ui.ActionSeg("list.open.mouse"),
				ui.ActionSeg("search.regex.toggle"),
				ui.ActionSeg("overlay.close"),
			}
		}
	}
	if m.sessionDeckFocused() {
		return "", []ui.HintSegment{
			ui.ActionSeg("focus.tab.next"),
			ui.ActionSeg("focus.tab.previous"),
			ui.ChordSeg("Ctrl+[ ]", "switch panels"),
			ui.ActionSeg("panel.close"),
		}
	}
	switch m.focusTarget() {
	case FocusTree:
		if m.Tree != nil && m.Tree.Model != nil && m.Tree.Model.FilterActive() {
			return "", []ui.HintSegment{
				ui.TextSeg("filter files"),
				ui.ActionSeg("list.navigate"),
				ui.ActionSeg("list.open"),
				ui.ActionSeg("launcher.filter.clear"),
			}
		}
		return "", []ui.HintSegment{
			ui.TextSeg("file tree"),
			ui.ActionSeg("list.navigate"),
			ui.ActionSeg("list.open"),
			ui.ActionSeg("launcher.filter"),
			ui.ActionSeg("view.focus.sidebar"),
		}
	case FocusLauncher:
		return "", m.launcherHelpSegments()
	case FocusTabBar:
		return "", []ui.HintSegment{
			ui.TextSeg("tab bar focused"),
			ui.ChordSeg("← →", "navigate"),
			ui.ActionSeg("tabbar.select"),
			ui.ActionSeg("tabbar.unfocus"),
		}
	}
	if m.OverlayState.IsOverlay() {
		return "", nil
	}
	if m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
		ed := m.EditorState.Editor
		title := ed.Title()
		if ed.Modified() {
			title += " ●"
		}
		prefix = fmt.Sprintf(
			"%s  %d:%d  %dL",
			title,
			ed.CursorLine()+1,
			ed.CursorCol()+1,
			ed.LineCount(),
		)
		if errs, warns, hints := ed.DiagnosticCounts(); errs > 0 || warns > 0 || hints > 0 {
			var parts []string
			if errs > 0 {
				parts = append(parts, fmt.Sprintf("✕%d", errs))
			}
			if warns > 0 {
				parts = append(parts, fmt.Sprintf("⚠%d", warns))
			}
			if hints > 0 {
				parts = append(parts, fmt.Sprintf("ℹ%d", hints))
			}
			prefix += "  " + strings.Join(parts, " ")
		}
		if m.actionRegistry != nil {
			segs = []ui.HintSegment{
				ui.ActionSeg("file.save"),
				ui.ActionSeg("edit.undo"),
				ui.ActionSeg("edit.redo"),
				ui.ActionSeg("editor.close"),
			}
		} else {
			segs = []ui.HintSegment{
				ui.ChordSeg("Ctrl+S", "save"),
				ui.ChordSeg("Ctrl+Z", "undo"),
				ui.ChordSeg("Ctrl+Y", "redo"),
				ui.ChordSeg("Esc", "close"),
			}
		}
		return prefix, segs
	}
	return "", m.Input.ContextHelpSegments()
}

// contextualHelpView returns flattened status help for tests.
func (m *App) contextualHelpView() string {
	prefix, segs := m.contextualHelpSegments()
	h := ui.NewHint("test-hint", m.actionRegistry, nil)
	h.SetPrefix(prefix)
	h.SetSegments(segs)
	return h.PlainText()
}

// renderHeaderToSurface paints the native header into its layer surface.
func (m *App) renderHeaderToSurface() {
	if m.mainHeaderSurf == nil || m.headerScreen == nil || m.headerHost == nil {
		return
	}
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainHeaderSurf.Buffer().Fill(bgCell)

	viewLabel := ""
	if node := m.AgentMgr.ActiveNode(); node != nil {
		viewLabel = workspace.AgentDisplayLabel(node)
	}
	usage := ""
	if m.StatusBar != nil {
		usage = m.StatusBar.UsageView()
	}
	m.headerScreen.SetProjection(shellheader.Projection{
		ModelName: m.ModelName,
		YOLO:      m.OverlayState.IsYolo(),
		StartTime: m.StartTime,
		Usage:     usage,
		ViewLabel: viewLabel,
		Width:     m.width,
	})

	m.mainHeaderSurf.SetVisible(true)
	m.headerHost.Resize(m.width, 2)
	m.headerHost.Layout()
	m.headerHost.Paint(m.mainHeaderSurf.Buffer())
}

// renderTabBarToSurface paints the native main tab bar into its layer surface.
func (m *App) renderTabBarToSurface() {
	if !m.tabBarVisible() || m.mainTabBarSurf == nil || m.tabScreen == nil || m.tabHost == nil {
		if m.mainTabBarSurf != nil {
			m.mainTabBarSurf.SetVisible(false)
		}
		return
	}
	bgCell := cell.NewCell(' ', cell.NoColor, rendering.ThemeColorToCell(m.Theme.Colors.Bg), 0)
	m.mainTabBarSurf.Buffer().Fill(bgCell)

	layout := m.computeLayout()
	m.syncTabBarHost()
	m.mainTabBarSurf.SetVisible(true)
	m.tabHost.Resize(layout.InnerW, 2)
	m.tabHost.Layout()
	m.tabHost.Paint(m.mainTabBarSurf.Buffer())
}
