package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/workspace"
)

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

	m.compositor.BeginFrame()
	// Fill the root with the active theme background so uncovered cells
	// (padding rows, transparent blits) follow the preset instead of the
	// terminal's default colour — otherwise light presets leave dark strips.
	if m.Theme != nil {
		bg := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
		m.compositor.Root().Buffer().Fill(bg)
	}
	// BeginFrame clears root children; re-mount the single shell surface.
	if m.mainSurf != nil {
		m.compositor.Root().AddChild(m.mainSurf)
		m.mainSurf.SetZOrder(0)
	}
	m.renderCtx.SpinFrame = m.SpinFrame
	m.syncRenderFocus()
	m.syncRootFocus()

	// The root tree: one Host paints the whole shell into one full-screen
	// surface. Floating windows are rendered by the WM on top afterwards.
	m.configureShell()
	m.renderShellToSurface()

	m.windowManager.Render(m.compositor.Root(), m.renderCtx)
	if isDragging {
		m.windowManager.ClearNeedsRender()
	}

	m.compositor.Composite()

	output := m.compositor.Present()
	m.cachedOutput = output

	v := tea.NewView(output)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

// contentLayout is the per-frame shell geometry computed by
// computeContentLayout. It drives BOTH the shell slot rects in the root
// tree AND the viewmodel's mouse-policy regions (editor/divider/status).
type contentLayout struct {
	SidebarW       int
	ContentW       int
	InnerW         int
	PadLR          int
	ChatAreaTop    int
	ChatH          int
	InputLineCount int
	ShowMainInput  bool
	InputRect      ui.Rect
	ChatRect       ui.Rect
	EditorRect     ui.Rect
	DeckRect       ui.Rect
	TermDockRect   ui.Rect
}

// computeContentLayout computes the shell geometry for one frame. It keeps
// every side effect the legacy buildMainView produced (window width, input
// shell height, chat/editor/input/deck rects, tree height messages, message
// list bounds) but produces no ANSI: the root tree paints the shell.
func (m *App) computeContentLayout() contentLayout {
	lay := contentLayout{}

	activityW := m.activityChromeWidth()
	sidebarVisible := m.sidebarHostVisible()
	if sidebarVisible {
		lay.SidebarW = activityW + m.Tree.Width + 1 // activity + content + divider
	} else {
		lay.SidebarW = activityW
	}

	m.Win.SetWidth(m.width - 2*m.Theme.Spacing.PadLR - lay.SidebarW)

	lay.ContentW = m.width - lay.SidebarW
	lay.PadLR = m.Theme.Spacing.PadLR
	lay.InnerW = lay.ContentW - m.Theme.Spacing.PadLR*2
	if lay.InnerW < 20 {
		lay.InnerW = 20
	}

	isEditorActive := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	isLauncherActive := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && m.launcherScreen != nil
	lay.ShowMainInput = !isLauncherActive && !isEditorActive && !m.OverlayState.IsOverlay()

	m.syncFormHint()

	if lay.ShowMainInput {
		lay.InputLineCount = m.Input.ShellHeight(lay.ContentW)
		if lay.InputLineCount < 1 {
			lay.InputLineCount = 1
		}
	}

	agentTabBarVisible := m.tabBarVisible()
	dockH := m.termDockHeightClamped()
	bottomChrome := 2 + dockH

	baseNonChat := 3 + lay.InputLineCount
	if agentTabBarVisible {
		baseNonChat += 2
	}
	deckAvailable := m.height - bottomChrome - baseNonChat - 1
	deckLineCount := m.sessionDeckHeight(max(0, deckAvailable))
	nonChat := baseNonChat + deckLineCount

	// Clamp the input shell to the space above the status bar (the deck is
	// squeezed first by the deckAvailable math above, so it is known here).
	// The old layer-surface world masked oversized inputs with the status
	// surface's higher z-order; the root host paints status FIRST, so an
	// unclamped shell would paint its rows over the status separator and the
	// status content (long/multiline inputs "push the status out of screen").
	// Keep one chat row minimum.
	if lay.ShowMainInput {
		budget := m.height - bottomChrome - 3 - deckLineCount
		if agentTabBarVisible {
			budget -= 2
		}
		budget -= 1 // at least one chat row
		if lay.InputLineCount > budget {
			lay.InputLineCount = budget
		}
		if lay.InputLineCount < 1 {
			lay.InputLineCount = 1
		}
		nonChat = 3 + lay.InputLineCount + deckLineCount
		if agentTabBarVisible {
			nonChat += 2
		}
	}

	lay.ChatH = m.height - bottomChrome - nonChat
	if lay.ChatH < 1 {
		lay.ChatH = 1
	}
	m.ChatAreaHeight = lay.ChatH
	deckY := 3 + lay.ChatH
	if agentTabBarVisible {
		deckY += 2
	}
	if deckLineCount > 0 {
		lay.DeckRect = ui.Rect{X: lay.SidebarW + m.Theme.Spacing.PadLR, Y: deckY, W: lay.InnerW, H: deckLineCount}
		m.sessionDeckRect = lay.DeckRect
	} else {
		lay.DeckRect = ui.Rect{}
		m.sessionDeckRect = ui.Rect{}
	}

	if isEditorActive {
		editorPadL := m.Theme.Spacing.PadLR
		if sidebarVisible {
			editorPadL = 0
		}
		m.EditorState.Editor.SetPadding(editorPadL)
		m.EditorState.Editor.SetStatusChrome(false)
		chatAreaTop := 3
		if agentTabBarVisible {
			chatAreaTop += 2
		}
		lay.ChatAreaTop = chatAreaTop
		lay.EditorRect = ui.Rect{
			X: lay.SidebarW,
			Y: chatAreaTop,
			W: lay.ContentW,
			H: lay.ChatH,
		}
		m.editorRect = lay.EditorRect
		m.chatRect = ui.Rect{}
	} else if isLauncherActive {
		chatAreaTop := 3
		if agentTabBarVisible {
			chatAreaTop += 2
		}
		lay.ChatAreaTop = chatAreaTop
		m.chatRect = ui.Rect{}
		m.editorRect = ui.Rect{}
	} else {
		// Agent chat.
		chatAreaTop := 3
		if agentTabBarVisible {
			chatAreaTop += 2
		}
		lay.ChatAreaTop = chatAreaTop
		lay.ChatRect = ui.Rect{
			X: lay.SidebarW,
			Y: chatAreaTop,
			W: lay.ContentW,
			H: lay.ChatH,
		}
		m.chatRect = lay.ChatRect
		m.editorRect = ui.Rect{}
		// Prepare MessageList geometry for subpane render/export.
		if activeML := m.activeMessageList(); activeML != nil {
			activeML.SetChatAreaBounds(chatAreaTop, lay.ChatH)
			activeML.SetSize(m.width-lay.SidebarW, m.height)
		}
	}

	if lay.ShowMainInput {
		inputY := 3 + lay.ChatH
		if agentTabBarVisible {
			inputY += 2
		}
		inputY += deckLineCount
		lay.InputRect = ui.Rect{
			X: lay.SidebarW,
			Y: inputY,
			W: lay.ContentW,
			H: lay.InputLineCount,
		}
		m.inputRect = lay.InputRect
	} else {
		lay.InputRect = ui.Rect{}
		m.inputRect = ui.Rect{}
	}

	// The sidebar begins below the global application header and keeps the
	// full remaining height beside the terminal panel (VS Code layout: the
	// bottom panel occupies only the content column, never the sidebar).
	if m.Tree != nil && m.Tree.Visible && m.Tree.Model != nil {
		treeStart := sidebarTopRow
		m.Tree.YOffset = treeStart
		contentH := m.height - 2
		sidebarH := contentH - treeStart
		if sidebarH < 0 {
			sidebarH = 0
		}
		m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
	}

	if dockH > 0 {
		lay.TermDockRect = ui.Rect{
			X: lay.SidebarW,
			Y: m.height - 2 - dockH,
			W: max(0, m.width-lay.SidebarW),
			H: dockH,
		}
		m.termDockRect = lay.TermDockRect
	} else {
		lay.TermDockRect = ui.Rect{}
		m.termDockRect = ui.Rect{}
	}

	return lay
}

// configureShell updates the root-tree slots and screen projections for one
// frame. It must run after computeContentLayout so the rects are fresh.
func (m *App) configureShell() {
	lay := m.computeContentLayout()

	// Persistent chrome: header and status are always mounted.
	m.shell.setSlot(shellHeaderSlot, true, ui.Rect{X: 0, Y: 1, W: m.width, H: 2})
	m.shell.setSlot(shellStatusSlot, true, ui.Rect{X: 0, Y: m.height - 2, W: m.width, H: 2})

	// Tab bar: inset below the header separator, above the chat area.
	m.shell.setSlot(shellTabsSlot, m.tabBarVisible(), ui.Rect{
		X: lay.SidebarW + lay.PadLR, Y: 3, W: lay.InnerW, H: 2,
	})

	// Activity bar: always-on left rail below the global application header.
	activityW := m.activityChromeWidth()
	sidebarH := (m.height - 2) - sidebarTopRow
	if sidebarH < 1 {
		sidebarH = 1
	}
	m.shell.setSlot(shellActivitySlot, activityW > 0, ui.Rect{X: 0, Y: sidebarTopRow, W: activityW, H: sidebarH})

	// Sidebar content: files / agents / terminals / git, right of the rail.
	sidebarActive := m.sidebarHostVisible()
	sidebarW := 0
	if sidebarActive {
		sidebarW = m.Tree.Width + 1
	}
	m.shell.setSlot(shellSidebarSlot, sidebarActive, ui.Rect{X: activityW, Y: sidebarTopRow, W: sidebarW, H: sidebarH})
	if m.activityView == activityGit && m.sourceControl != nil {
		m.shell.SetScreen(shellSidebarSlot, m.sourceControl)
	} else if m.activityView != activityFiles && m.activityList != nil {
		m.shell.SetScreen(shellSidebarSlot, m.activityList)
	} else if m.sidebarScreen != nil {
		m.shell.SetScreen(shellSidebarSlot, m.sidebarScreen)
	}

	// Main content views are mutually exclusive: launcher / chat / editor.
	launcherActive := m.launcherHostVisible()
	m.shell.setSlot(shellLauncherSlot, launcherActive, ui.Rect{
		X: lay.SidebarW + lay.PadLR, Y: lay.ChatAreaTop, W: lay.InnerW, H: lay.ChatH,
	})
	m.shell.setSlot(shellChatSlot, m.chatHostVisible(), lay.ChatRect)
	m.shell.setSlot(shellEditorSlot, m.isEditorActive(), lay.EditorRect)
	m.shell.setSlot(shellInputSlot, !lay.InputRect.Empty(), lay.InputRect)
	m.shell.setSlot(shellDeckSlot, !lay.DeckRect.Empty(), lay.DeckRect)
	m.shell.setSlot(shellTermDockSlot, !lay.TermDockRect.Empty(), lay.TermDockRect)
	if m.termDock != nil && !lay.TermDockRect.Empty() {
		m.termDock.SetFocused(m.termDockFocused)
		m.termDock.PrepareResize(lay.TermDockRect.Y, 8, 6)
	}

	// Screen projections/binds (paint happens once via rootHost.Paint).
	m.configureHeaderScreen()
	m.configureTabBarScreen()
	m.configureSidebarScreen()
	m.refreshActivityList()
	m.configureInputScreen()
	m.configureChatScreen()
	m.configureEditorScreen()
	m.configureLauncherScreen()
	m.configureStatusScreen()
}

func (m *App) isEditorActive() bool {
	return m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen()
}

// renderShellToSurface paints the whole root tree into the single
// full-screen surface. Relayout is unconditional: the shell is shallow
// (measure+layout of ~10 components), and relayouting every frame guarantees
// the hit tree, focus registry, and paint all come from THIS frame's
// geometry — the one-geometry-source contract of the root host.
//
// The surface is filled with the theme background BEFORE the tree paints:
// unmounted slots (the launcher replacing the editor, chat replacing the
// launcher, ...) must not leave their previous opaque cells behind — every
// cell not covered by an active slot shows the clean background.
func (m *App) renderShellToSurface() {
	if m.mainSurf == nil || m.rootHost == nil || m.shell == nil {
		return
	}
	if m.mainSurf.Width() != m.width || m.mainSurf.Height() != m.height {
		m.mainSurf.Resize(m.width, m.height)
	}
	m.mainSurf.SetPosition(0, 0)
	m.mainSurf.SetVisible(true)
	m.shell.ClearDirty()

	bg := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
	m.mainSurf.Buffer().Fill(bg)

	m.rootHost.SetFrame(m.SpinFrame)
	m.rootHost.Resize(m.width, m.height)
	m.rootHost.MarkLayout()
	m.rootHost.Paint(m.mainSurf.Buffer())
}

// syncRootFocus maps the viewmodel focus policy (focusTarget / FocusStack)
// onto the root Host's single focused component. Runs every frame so paint
// (Focused states) and key dispatch agree with the FocusStack.
func (m *App) syncRootFocus() {
	if m.rootHost == nil {
		return
	}
	defer m.syncFloatingTerminalFocus()
	defer m.syncTerminalFocusReports()
	if m.termDockOwnsKeys() && m.termDock != nil {
		cur := m.rootHost.Focused()
		if cur == termdock.TabsID || cur == termdock.DividerID {
			return
		}
		if child := m.termDock.Active(); child != nil {
			m.rootHost.Focus(child.ID())
			return
		}
		m.rootHost.Focus(termdock.ScreenID)
		return
	}
	var id ui.ComponentID
	switch m.focusTarget() {
	case FocusTree:
		if m.activityView == activityFiles {
			id = shelltree.TreeID
			if current := m.rootHost.Focused(); current == shelltree.FilterID || current == shelltree.TreeID {
				id = current
			}
		} else if m.activityView == activityGit && m.sourceControl != nil {
			id = sourcecontrol.ListID
			if current := m.rootHost.Focused(); current == sourcecontrol.CommitID || current == sourcecontrol.ListID {
				id = current
			}
		} else {
			id = activitylist.ListID
			if current := m.rootHost.Focused(); current == activitylist.FilterID || current == activitylist.ListID {
				id = current
			}
		}
	case FocusTabBar:
		id = shelltabs.TabsID
	case FocusEditor:
		id = shelleditor.ScreenID
	case FocusLauncher:
		id = launcher.ScreenID
		switch current := m.rootHost.Focused(); current {
		case launcher.ScreenID, launcher.HeroID, launcher.NewWorkspaceID,
			launcher.SearchID, launcher.WorkspaceListID:
			id = current
		}
	case FocusInput:
		id = shellinput.ScreenID
	default:
		id = shellchat.ScreenID
	}
	m.rootHost.Focus(id)
}

func (m *App) syncFloatingTerminalFocus() {
	if m.overlayHost == nil {
		return
	}
	for _, e := range m.overlayHost.All() {
		if e == nil || e.Window == nil || !ui.IsTerminalOverlay(e.ID) {
			continue
		}
		id, ok := ui.TerminalIDFromOverlay(e.ID)
		if !ok {
			continue
		}
		screen := m.termScreens[id]
		if screen == nil {
			continue
		}
		w, ok := e.Window.Content.(*ui.WindowFrameWidget)
		if !ok || w.Host() == nil {
			continue
		}
		w.Host().Focus(screen.ID())
	}
}

// syncTerminalFocusReports tells each PTY child whether its pane owns input
// (DEC mode 1004 focus reporting). Idempotent: screens only write CSI I/O on
// an actual transition, so a per-frame sweep is safe and catches every focus
// path (dock toggle, floating focus, palette stealing keys, ...).
func (m *App) syncTerminalFocusReports() {
	focusedFloating := ""
	wmFocused := false
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			wmFocused = true
			if ui.IsTerminalOverlay(ui.OverlayID(fw.ID)) {
				if id, ok := ui.TerminalIDFromOverlay(ui.OverlayID(fw.ID)); ok {
					focusedFloating = id
				}
			}
		}
	}
	dockActive := ""
	if focusedFloating == "" && !wmFocused && m.termDockOwnsKeys() && m.termDock != nil {
		dockActive = m.termDock.ActiveID()
	}
	for id, s := range m.termScreens {
		if s == nil {
			continue
		}
		s.SetTermFocused(id == focusedFloating || id == dockActive)
	}
}

func (m *App) syncFormHint() {
	// Tab-bar / overlay hints are built in contextualHelpSegments.
}

func (m *App) configureHeaderScreen() {
	if m.headerScreen == nil {
		return
	}
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
}

func (m *App) configureTabBarScreen() {
	if m.tabScreen == nil {
		return
	}
	m.syncTabBarHost()
}

func (m *App) configureSidebarScreen() {
	if m.sidebarScreen != nil {
		maxW := m.width / 2
		if maxW < 12 {
			maxW = 12
		}
		m.sidebarScreen.SetSplitMaxFirst(maxW)
	}
	if m.activityList != nil {
		maxW := m.width / 2
		if maxW < 12 {
			maxW = 12
		}
		m.activityList.SetSplitMaxFirst(maxW)
	}
	if m.sourceControl != nil {
		maxW := m.width / 2
		if maxW < 12 {
			maxW = 12
		}
		m.sourceControl.SetSplitMaxFirst(maxW)
	}
}

func (m *App) configureInputScreen() {
	if m.inputScreen == nil || m.inputRect.Empty() {
		return
	}
	m.Input.SetSpinFrame(m.SpinFrame)
	m.inputScreen.Bind(&m.Input)
	m.inputScreen.SetWidth(m.inputRect.W)
}

func (m *App) configureChatScreen() {
	if m.chatRect.Empty() || m.chatScreen == nil {
		return
	}
	activeML := m.activeMessageList()
	if activeML == nil {
		return
	}

	layout := m.computeLayout()
	innerW := layout.InnerW
	sidebarW := layout.SidebarW
	chatH := m.chatRect.H

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
	activeML.SetChatAreaBounds(m.chatRect.Y, chatH)
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
	if m.chatScreen.HasActiveSelection() {
		paint.HasSelection = true
		paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol = m.chatScreen.SelectionRange()
	}
	m.chatScreen.SetMessages(msgRows, paint)
}

// configureEditorScreen binds the DocumentEditor for the next paint. Editor
// input remains viewmodel-routed (shelleditor.Screen.Handle is a no-op).
func (m *App) configureEditorScreen() {
	if !m.isEditorActive() || m.editorScreen == nil {
		return
	}
	editorPadL := m.Theme.Spacing.PadLR
	editorPadR := m.Theme.Spacing.PadLR
	if m.Tree != nil && m.Tree.Visible {
		editorPadL = 0
	}
	m.EditorState.Editor.SetPadding(editorPadL)
	m.EditorState.Editor.SetStatusChrome(false)
	m.EditorState.Editor.SetTokens(m.rootHost.Tokens())
	m.editorScreen.Bind(m.EditorState.Editor)
	m.editorScreen.SetPadding(editorPadL, editorPadR)
	m.editorScreen.SetFocused(m.focusTarget() == FocusEditor)
}

func (m *App) configureLauncherScreen() {
	if m.launcherScreen == nil || !m.launcherHostVisible() {
		return
	}
	m.launcherScreen.SetShortcuts(m.launcherShortcuts())
	m.launcherScreen.SetAnimation(m.SpinFrame, time.Since(m.LastInteractionTime).Seconds())
}

func (m *App) configureStatusScreen() {
	if m.StatusBar == nil {
		return
	}
	m.syncFormHint()
	prefix, segs := m.contextualHelpSegments()
	m.StatusBar.SetContextHelp(m.actionRegistry, prefix, segs)
	m.StatusBar.SetWidth(m.width)
	m.syncWorkspaceStatusView()
}

func (m *App) contextualHelpSegments() (prefix string, segs []ui.HintSegment) {
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			switch fw.ID {
			case findInFilesWindowID, replaceInFilesWindowID:
				return "", []ui.HintSegment{
					ui.TextSeg("type to search"),
					ui.ActionSeg("list.preview.navigate"),
					ui.ActionSeg("list.open"),
					ui.ActionSeg("search.regex.toggle"),
					ui.ActionSeg("overlay.close"),
				}
			case string(ui.OverlayCommandPalette):
				return "", []ui.HintSegment{
					ui.TextSeg("navigate ↑↓"),
					ui.TextSeg("run Enter"),
					ui.ActionSeg("overlay.close"),
				}
			case string(ui.OverlayWorkspaceSettings):
				return "workspace settings", []ui.HintSegment{
					ui.TextSeg("navigate ↑↓"),
					ui.TextSeg("next Tab"),
					ui.TextSeg("edit Enter"),
					ui.ActionSeg("overlay.close"),
				}
			default:
				if fw.Focusable() {
					return "", []ui.HintSegment{ui.ActionSeg("overlay.close")}
				}
			}
		}
	}
	if m.sessionDeckFocused() {
		return "", []ui.HintSegment{
			ui.ActionSeg("focus.tab.next"),
			ui.ActionSeg("focus.tab.previous"),
			ui.ActionSeg("panel.previous"),
			ui.ActionSeg("panel.next"),
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
			ui.ActionSeg("tabbar.previous"),
			ui.ActionSeg("tabbar.next"),
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
