package viewmodel

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shelleditor"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shellinput"
	"crude/tui/screens/shelltabs"
	"crude/tui/screens/shelltree"
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
}

// computeContentLayout computes the shell geometry for one frame. It keeps
// every side effect the legacy buildMainView produced (window width, input
// shell height, chat/editor/input/deck rects, tree height messages, message
// list bounds) but produces no ANSI: the root tree paints the shell.
func (m *App) computeContentLayout() contentLayout {
	lay := contentLayout{}

	sidebarVisible := m.Tree.Visible && m.Tree.Model != nil && m.sidebarScreen != nil
	if sidebarVisible {
		lay.SidebarW = m.Tree.Width + 1 // +1 for divider
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

	baseNonChat := 3 + lay.InputLineCount
	if agentTabBarVisible {
		baseNonChat += 2
	}
	deckAvailable := m.height - 2 - baseNonChat - 1
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
		budget := m.height - 2 - 3 - deckLineCount
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

	lay.ChatH = m.height - 2 - nonChat
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

	// The sidebar is a slot in the root tree starting at row 3. Update the
	// tree height for the model (used for scroll extent and rendering).
	if m.Tree.Visible && m.Tree.Model != nil {
		treeStart := 3
		m.Tree.YOffset = treeStart
		contentH := m.height - 2
		sidebarH := contentH - treeStart
		if sidebarH < 0 {
			sidebarH = 0
		}
		m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
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

	// Sidebar: full-height panel left of the content column.
	sidebarActive := m.sidebarHostVisible()
	sidebarH := (m.height - 2) - 3
	if sidebarH < 1 {
		sidebarH = 1
	}
	sidebarW := 0
	if sidebarActive {
		sidebarW = m.Tree.Width + 1
	}
	m.shell.setSlot(shellSidebarSlot, sidebarActive, ui.Rect{X: 0, Y: 3, W: sidebarW, H: sidebarH})

	// Main content views are mutually exclusive: launcher / chat / editor.
	launcherActive := m.launcherHostVisible()
	m.shell.setSlot(shellLauncherSlot, launcherActive, ui.Rect{
		X: lay.SidebarW + lay.PadLR, Y: lay.ChatAreaTop, W: lay.InnerW, H: lay.ChatH,
	})
	m.shell.setSlot(shellChatSlot, m.chatHostVisible(), lay.ChatRect)
	m.shell.setSlot(shellEditorSlot, m.isEditorActive(), lay.EditorRect)
	m.shell.setSlot(shellInputSlot, !lay.InputRect.Empty(), lay.InputRect)
	m.shell.setSlot(shellDeckSlot, !lay.DeckRect.Empty(), lay.DeckRect)

	// Screen projections/binds (paint happens once via rootHost.Paint).
	m.configureHeaderScreen()
	m.configureTabBarScreen()
	m.configureSidebarScreen()
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
	var id ui.ComponentID
	switch m.focusTarget() {
	case FocusTree:
		id = shelltree.TreeID
	case FocusTabBar:
		id = shelltabs.TabsID
	case FocusEditor:
		id = shelleditor.ScreenID
	case FocusLauncher:
		id = launcher.ScreenID
	case FocusInput:
		id = shellinput.ScreenID
	default:
		id = shellchat.ScreenID
	}
	m.rootHost.Focus(id)
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
	// The sidebar screen derives its width/height from the slot rect during
	// layout and syncs the model from the tree; no per-frame binds needed.
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
	if m.Tree.Visible {
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
