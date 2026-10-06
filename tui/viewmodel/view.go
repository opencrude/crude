package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/activitylist"
	"crude/tui/screens/gitreview"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shelltree"
	"crude/tui/screens/sourcecontrol"
	"crude/tui/ui"
	"crude/workspace"
)

func (m *App) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return m.newTerminalView("")
	}
	m.syncMemoryBrowserLifecycle()
	if m.shouldDeferFrame() {
		return m.newTerminalView(m.cachedOutput)
	}
	m.maybeHydrateComposerDraft()

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
	m.windowManager.ClearNeedsRender()

	m.compositor.Composite()

	output := m.compositor.Present()
	m.cachedOutput = output
	m.noteFramePresented(time.Now())

	return m.newTerminalView(output)
}

func (m *App) newTerminalView(output string) tea.View {
	v := tea.NewView(output)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.ReportFocus = true
	v.WindowTitle = m.terminalIdentity()
	v.IconName = v.WindowTitle
	return v
}

func (m *App) terminalIdentity() string {
	workspaceName := filepath.Base(filepath.Clean(m.RootPath))
	workspaceName = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, workspaceName))
	if workspaceName == "" || workspaceName == "." || workspaceName == string(filepath.Separator) {
		return "Crude"
	}
	return workspaceName + " - Crude"
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

// boundedContentGeometry turns theme padding into effective per-frame
// padding. Normal widths keep the requested padding exactly; narrow widths
// spend at most half the real content columns on each side.
func boundedContentGeometry(contentW, requestedPad int) (padLR, innerW int) {
	contentW = max(0, contentW)
	padLR = min(max(0, requestedPad), contentW/2)
	return padLR, contentW - 2*padLR
}

// computeContentLayout computes the shell geometry for one frame. It keeps
// every side effect the legacy buildMainView produced (window width, input
// shell height, chat/editor/input/deck rects, tree height messages, message
// list bounds) but produces no ANSI: the root tree paints the shell.
func (m *App) computeContentLayout() contentLayout {
	lay := contentLayout{}
	frame := m.frameInsets()

	activityW := m.activityChromeWidth()
	sidebarVisible := m.sidebarHostVisible()
	if sidebarVisible {
		lay.SidebarW = frame.Left + activityW + m.sidebarPanelWidth() + 1 // frame + activity + content + divider
	} else {
		lay.SidebarW = frame.Left + activityW
	}

	// Rect geometry must describe columns that actually exist. Minimum-width
	// product policies (for example, whether the launcher is usable) remain
	// visibility decisions; they must not widen shell slots past the terminal.
	lay.ContentW = max(0, m.width-frame.Right-lay.SidebarW)
	lay.PadLR, lay.InnerW = boundedContentGeometry(lay.ContentW, m.Theme.Spacing.PadLR)
	m.Win.SetWidth(lay.InnerW)

	isEditorActive := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	isReviewActive := m.isGitReviewActive()
	isLauncherActive := !isReviewActive && m.AgentMgr.VisibleCount() == 0 && m.EditorState.ActiveView == "" && m.launcherScreen != nil
	// Attachment viewers and file pickers float above the retained draft.
	// Inspecting an attachment must not make its composer disappear underneath.
	overlayHidesInput := m.OverlayState.IsOverlay() && !m.OverlayState.Is(workspace.ModeFilePicker) && !m.OverlayState.Is(workspace.ModeTextOverlay)
	reviewingCompletedAgent := false
	if node := m.AgentMgr.ActiveNode(); node != nil && node.State != nil {
		reviewingCompletedAgent = node.State.Completed
	}
	lay.ShowMainInput = !isReviewActive && !isLauncherActive && !isEditorActive && !overlayHidesInput && !reviewingCompletedAgent

	m.syncFormHint()

	if lay.ShowMainInput {
		lay.InputLineCount = m.Input.ShellHeight(lay.ContentW)
		if lay.InputLineCount < 1 {
			lay.InputLineCount = 1
		}
	}

	agentTabBarVisible := m.tabBarVisible()
	dockH := m.termDockHeightClamped()
	bottomChrome := m.chromeBottomRows() + dockH
	if lay.ShowMainInput {
		// Composer growth may never take over the conversation. The input
		// already caps its text at six rows; short grids additionally cap the
		// whole composer (rule/chips/menus included) to one third of the column.
		available := max(0, m.height-bottomChrome-m.chromeTopRow())
		lay.InputLineCount = min(lay.InputLineCount, max(2, available/3))
	}

	baseNonChat := m.shellTopRow() + lay.InputLineCount
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
		budget := m.height - bottomChrome - m.shellTopRow() - deckLineCount
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
		nonChat = m.shellTopRow() + lay.InputLineCount + deckLineCount
		if agentTabBarVisible {
			nonChat += 2
		}
	}

	lay.ChatH = m.height - bottomChrome - nonChat
	if lay.ChatH < 1 {
		lay.ChatH = 1
	}
	m.ChatAreaHeight = lay.ChatH
	deckY := m.chromeTopRow() + lay.ChatH
	if deckLineCount > 0 {
		// The deck slot spans the full content column so separator rules can
		// run edge to edge (like the terminal dock); the PanelDeck applies
		// the theme's horizontal padding to its tabs and panel content.
		lay.DeckRect = ui.Rect{X: lay.SidebarW, Y: deckY, W: lay.ContentW, H: deckLineCount}
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
		chatAreaTop := m.chromeTopRow()
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
		lay.ChatAreaTop = m.chromeTopRow()
		m.chatRect = ui.Rect{}
		m.editorRect = ui.Rect{}
	} else {
		// Agent chat.
		chatAreaTop := m.chromeTopRow()
		lay.ChatAreaTop = chatAreaTop
		lay.ChatRect = ui.Rect{
			X: lay.SidebarW,
			Y: chatAreaTop,
			W: lay.ContentW,
			H: lay.ChatH,
		}
		m.chatRect = lay.ChatRect
		m.editorRect = ui.Rect{}
	}

	if lay.ShowMainInput {
		inputY := m.chromeTopRow() + lay.ChatH + deckLineCount
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
		treeStart := m.shellTopRow()
		m.Tree.YOffset = treeStart
		contentH := m.height - m.chromeBottomRows()
		sidebarH := contentH - treeStart
		if sidebarH < 0 {
			sidebarH = 0
		}
		m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
	}

	if dockH > 0 {
		lay.TermDockRect = ui.Rect{
			X: lay.SidebarW,
			Y: m.height - m.chromeBottomRows() - dockH,
			W: max(0, m.width-frame.Right-lay.SidebarW),
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
	frame := m.frameInsets()

	// Persistent top chrome is inset by the shell frame; frame rows/columns
	// stay background-painted. Bottom rows belong to content now.
	m.shell.setSlot(shellHeaderSlot, true, ui.Rect{
		X: frame.Left, Y: frame.Top, W: m.width - frame.Left - frame.Right, H: 2,
	})
	m.shell.setSlot(shellFooterSlot, true, ui.Rect{
		X: frame.Left, Y: m.height - frame.Bottom - 2, W: m.width - frame.Left - frame.Right, H: 2,
	})
	// Tab bar: full content column below the header separator so its bottom
	// rule runs edge to edge; the shelltabs screen applies the theme's
	// horizontal padding to the tabs control itself.
	m.shell.setSlot(shellTabsSlot, m.tabBarVisible(), ui.Rect{
		X: lay.SidebarW, Y: m.shellTopRow(), W: lay.ContentW, H: 2,
	})

	// Activity bar: always-on left rail below the global application header.
	activityW := m.activityChromeWidth()
	sidebarH := (m.height - m.chromeBottomRows()) - m.shellTopRow()
	if sidebarH < 1 {
		sidebarH = 1
	}
	m.shell.setSlot(shellActivitySlot, activityW > 0, ui.Rect{
		X: frame.Left, Y: m.shellTopRow(), W: activityW, H: sidebarH,
	})

	// Sidebar content: files / agents / terminals / git, right of the rail.
	sidebarActive := m.sidebarHostVisible()
	sidebarW := 0
	if sidebarActive {
		sidebarW = m.sidebarPanelWidth() + 1
	}
	m.shell.setSlot(shellSidebarSlot, sidebarActive, ui.Rect{
		X: frame.Left + activityW, Y: m.shellTopRow(), W: sidebarW, H: sidebarH,
	})
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
	m.shell.SetScreen(shellGitReviewSlot, m.gitReview)
	m.shell.setSlot(shellGitReviewSlot, m.isGitReviewActive(), ui.Rect{X: lay.SidebarW, Y: lay.ChatAreaTop, W: lay.ContentW, H: lay.ChatH})
	if m.gitReview != nil {
		m.gitReview.SetSidebarAvailable(m.sidebarHostVisible() && m.activityView == activityGit)
	}
	m.shell.setSlot(shellInputSlot, !lay.InputRect.Empty(), lay.InputRect)
	m.shell.setSlot(shellDeckSlot, !lay.DeckRect.Empty(), lay.DeckRect)
	m.shell.setSlot(shellTermDockSlot, !lay.TermDockRect.Empty(), lay.TermDockRect)
	if m.termDock != nil && !lay.TermDockRect.Empty() {
		m.termDock.SetFocused(m.termDockFocused)
		m.termDock.PrepareResize(lay.TermDockRect.Y, 8, 6)
	}

	// Screen projections/binds (paint happens once via rootHost.Paint).
	m.configureHeaderScreen()
	m.configureFooterScreen()
	m.configureTabBarScreen()
	m.configureSidebarScreen()
	m.refreshActivityList()
	m.configureInputScreen()
	m.configureChatScreenWithLayout(lay)
	m.configureEditorScreen()
	m.configureLauncherScreen()
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
		if m.termDock.OwnsFocus(cur) {
			return
		}
		m.rootHost.Focus(m.termDock.PreferredFocus())
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
			if current := m.rootHost.Focused(); current == sourcecontrol.FilterID || current == sourcecontrol.ListID || current == sourcecontrol.ActionsID {
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
		if m.launcherScreen != nil && m.rootHost.Focused() == launcher.ScreenID {
			m.launcherScreen.SyncFocus()
		}
		if current := m.rootHost.Focused(); m.launcherScreen != nil && m.launcherScreen.OwnsFocus(current) {
			id = current
		}
	case FocusInput:
		if m.isGitReviewActive() {
			id = gitreview.ScreenID
			if current := m.rootHost.Focused(); strings.HasPrefix(string(current), "gitreview") {
				id = current
			}
		} else {
			id = shellinput.ScreenID
		}
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
	if focusedFloating == "" && !wmFocused && m.terminalDockOwnsKeys() && m.termDock != nil {
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

// configureHeaderScreen pushes the header's presentation data. Width mirrors
// the header slot (terminal width minus the horizontal shell frame) so the
// right-aligned clock/usage runs stay inside the padded slot.
func (m *App) configureHeaderScreen() {
	if m.headerScreen == nil {
		return
	}
	frame := m.frameInsets()
	m.headerScreen.SetProjection(shellheader.Projection{
		ModelName: m.ModelName,
		YOLO:      m.OverlayState.IsYolo(),
		StartTime: m.StartTime,
		// No ViewLabel: the active agent already has its own window tab, so
		// the header stays on app + model only.
		Width:         m.width - frame.Left - frame.Right,
		LeftSegments:  m.statusHeaderLeftSegments(),
		RightSegments: m.statusHeaderRightSegments(),
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

// configureChatScreen keeps focused tests and non-frame callers on the same
// bounded geometry contract by deriving widths from the already-final chat
// rect. Production frames call configureChatScreenWithLayout with the exact
// computeContentLayout result.
func (m *App) configureChatScreen() {
	contentW := max(0, m.chatRect.W)
	padLR, innerW := boundedContentGeometry(contentW, m.Theme.Spacing.PadLR)
	m.configureChatScreenWithLayout(contentLayout{
		ContentW: contentW,
		InnerW:   innerW,
		PadLR:    padLR,
	})
}

func (m *App) configureChatScreenWithLayout(layout contentLayout) {
	if m.chatRect.Empty() || m.chatScreen == nil {
		return
	}
	activeML := m.activeMessageList()
	if activeML == nil {
		return
	}

	innerW := layout.InnerW
	renderContentW, renderInnerW := layout.ContentW, innerW
	if m.sidebarResizeActive() {
		if m.chatReflowContentW == 0 {
			m.chatReflowContentW, m.chatReflowInnerW = renderContentW, renderInnerW
		} else {
			renderContentW, renderInnerW = m.chatReflowContentW, m.chatReflowInnerW
		}
	} else {
		m.chatReflowContentW, m.chatReflowInnerW = renderContentW, renderInnerW
	}
	activity := m.AgentActivity(m.AgentMgr.ActiveID())
	if activity.ThinkingID != "" {
		activity.ThinkingExpanded = activeML.DisclosureExpanded(activity.ThinkingID)
	}
	m.chatScreen.SetActivity(activity)
	chatH := m.chatRect.H - shellchat.ActivityRows(m.chatRect.H)

	activeAgent := m.agentForView()
	activeTyping := false
	if activeAgent != nil {
		activeTyping = activeAgent.ComputeActiveTyping()
	}

	activeML.SetStreaming(activeAgent != nil && activeAgent.Streaming)
	activeML.SetSpinFrame(m.SpinFrame)
	activeML.SetActiveTyping(activeTyping)
	if center := m.Status; center != nil {
		if status, ok := center.Agent(m.AgentMgr.ActiveID()); ok {
			activeML.SetPhase(string(status.Phase))
		}
	}
	activeML.SetChatAreaBounds(m.chatRect.Y, chatH)
	activeML.SetSize(renderContentW, m.height)
	activeML.BindScroll(m.chatScreen.ScrollModel())

	activeAgentID := m.AgentMgr.ActiveID()
	switched := m.beginAgentChatScrollSwitch(activeAgentID)
	// The first export renders inline subpanes, and that render is what sets
	// each pane's ScrollModel content/viewport. The nested-gutter nesting
	// level (does any pane reserve a scrollbar column?) depends on those
	// metrics, so it must be computed after this pass — otherwise a thinking
	// pane that just appeared on this frame computes nesting=0 and shows no
	// scrollbar until the next frame.
	snapshot := activeML.ExportMessageRowsSnapshot(renderInnerW)
	msgRows, paneLineMap, contentH := snapshot.Rows, snapshot.PaneLineMap, snapshot.ContentHeight
	m.finishAgentChatScrollMetrics(activeAgentID, chatH, contentH)
	if switched {
		snapshot = activeML.ExportMessageRowsSnapshot(renderInnerW)
		msgRows, paneLineMap, contentH = snapshot.Rows, snapshot.PaneLineMap, snapshot.ContentHeight
		m.finishAgentChatScrollMetrics(activeAgentID, chatH, contentH)
	}

	paneGutters := m.subPaneProjections.BuildPaneGutters(paneLineMap, m.PaneMgr)
	nestingLevel := 0
	for _, gutter := range paneGutters {
		if gutter.Visible && gutter.Scroll != nil {
			nestingLevel = 1
			break
		}
	}
	if activeML.SetPaneScrollbarWidth(nestingLevel * ui.ScrollbarWidth) {
		// The discovery pass above establishes each pane's vertical scroll
		// metrics. Re-export once when the horizontal gutter footprint changes
		// so the pane width and painted columns agree on this same frame.
		snapshot = activeML.ExportMessageRowsSnapshot(renderInnerW)
		msgRows, paneLineMap, contentH = snapshot.Rows, snapshot.PaneLineMap, snapshot.ContentHeight
		m.finishAgentChatScrollMetrics(activeAgentID, chatH, contentH)
		paneGutters = m.subPaneProjections.BuildPaneGutters(paneLineMap, m.PaneMgr)
	}

	mainSBVisible := contentH > chatH

	composeParams := shellchat.ComposeParams{
		InnerW:        renderInnerW,
		PadLR:         layout.PadLR,
		Theme:         m.Theme,
		NestingLevel:  nestingLevel,
		MainSBVisible: mainSBVisible,
		PaneLineMap:   paneLineMap,
		PaneGutters:   paneGutters,
	}

	paint := shellchat.PaintParams{SourceID: snapshot.SourceID, Generation: snapshot.Generation, ChangedIndices: snapshot.ChangedIndices, StructureChanged: snapshot.StructureChanged, Compose: composeParams, PaneLineMap: paneLineMap, OnPaneScroll: func(paneID string) { m.subPaneProjections.InvalidateSubPane(activeAgentID, paneID) }}
	if m.chatScreen.HasActiveSelection() {
		paint.HasSelection = true
		paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol = m.chatScreen.SelectionRange()
	}
	m.chatScreen.SetMessages(msgRows, paint)
}

// configureEditorScreen binds the DocumentEditor for the next paint. Pointer
// input enters through shelleditor.Screen so root Host capture remains global.
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
	m.launcherScreen.SetRegistry(m.actionRegistry)
	m.launcherScreen.SetAnimation(m.SpinFrame, time.Since(m.LastInteractionTime).Seconds())
}

func (m *App) contextualHelpSegments() (prefix string, segs []ui.HintSegment) {
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil {
			switch fw.ID {
			case string(ui.OverlayFindReplace):
				// The in-buffer popup stays chrome-clean (no hint row):
				// hints live here in the dedicated bottom-right hint box.
				if m.FindReplace != nil && m.FindReplace.ReplaceMode {
					return "", []ui.HintSegment{
						ui.ActionSeg("replace.hint.replace"),
						ui.ActionSeg("replace.hint.replace-all"),
						ui.ActionSeg("find.hint.previous"),
						ui.ActionSeg("find.hint.filters"),
						ui.ActionSeg("overlay.close"),
					}
				}
				return "", []ui.HintSegment{
					ui.ActionSeg("find.hint.navigate"),
					ui.ActionSeg("find.hint.previous"),
					ui.ActionSeg("find.hint.filters"),
					ui.ActionSeg("overlay.close"),
				}
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
		if m.activityView == activityAgents {
			prefix := "agents"
			if node := m.AgentMgr.ActiveNode(); node != nil && node.State != nil && node.State.Completed {
				prefix = "read-only agent review"
			}
			return prefix, []ui.HintSegment{
				ui.ActionSeg("list.navigate"),
				ui.ActionSeg("list.open"),
				ui.ActionSeg("view.focus.sidebar"),
			}
		}
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
		// Title() already carries the unsaved marker as a prefix; appending a
		// second dot here produced "name ● ●".
		title := ed.Title()
		prefix = fmt.Sprintf(
			"%s  %d:%d  %dL",
			title,
			ed.CursorLine()+1,
			ed.CursorCol()+1,
			ed.LineCount(),
		)
		if blame := ed.CursorBlameSummary(); blame != "" {
			prefix += "  " + blame
		}
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
	if node := m.AgentMgr.ActiveNode(); node != nil && node.State != nil && node.State.Completed {
		return "read-only agent review", []ui.HintSegment{
			ui.TextSeg("scroll ↑↓"),
			ui.ActionSeg("view.focus.sidebar"),
		}
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
