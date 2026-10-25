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
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shellheader"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/workspace"
)

func (m *App) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return m.newTerminalView("")
	}
	m.syncMemoryBrowserLifecycle()
	if m.shouldDeferFrame() {
		return m.newSurfaceTerminalView()
	}
	m.renderCtx.SpinFrame = m.SpinFrame
	m.syncRenderFocus()
	m.configureShell()
	m.prepareShellSurface()
	// Host generations own both source ink and mounted geometry. With no
	// floating layers, a clean retained Host is already the complete frame;
	// reuse its publication rather than clearing/composing/snapshotting it.
	// Floating owners keep their ordinary independent composition path.
	if m.framePresented && !m.rootHost.NeedsPaint() && !m.windowManager.HasWindows() && !m.windowManager.NeedsRender() {
		return m.newSurfaceTerminalView()
	}
	m.compositor.BeginFrame()
	// Fill the root with the active theme background so uncovered cells
	// (padding rows, transparent blits) follow the preset instead of the
	// terminal's default colour; otherwise light presets leave dark strips.
	if m.Theme != nil {
		bg := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
		m.compositor.Root().Buffer().Fill(bg)
	}
	// BeginFrame clears root children; re-mount the single shell surface.
	if m.mainSurf != nil {
		m.compositor.Root().AddChild(m.mainSurf)
		m.mainSurf.SetZOrder(0)
	}
	// The root tree: one Host paints the whole shell into one full-screen
	// surface. Floating windows are rendered by the WM on top afterwards.
	m.paintShellSurface()
	m.paintPaneDropPreview()
	// Preview ink is transient and drawn after Host paint. Leave its owning
	// region damaged so moving/cancelling it restores the retained content.
	if m.mainPanes != nil && m.mainPanes.drop != nil {
		m.rootHost.MarkComponentPaint(m.mainPanes.ID())
	}
	m.syncPlanHover()
	m.syncConnectionHover()

	m.windowManager.Render(m.compositor.Root(), m.renderCtx)
	m.windowManager.ClearNeedsRender()

	m.compositor.Composite()

	m.compositor.PresentSurface()
	m.noteFramePresented(time.Now())

	return m.newSurfaceTerminalView()
}

func (m *App) newSurfaceTerminalView() tea.View {
	v := m.newTerminalView("")
	v.Surface = m.compositor.CurrentSurface()
	return v
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
	workspaceName := filepath.Base(filepath.Clean(m.ws.RootPath()))
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
	frame := m.frameInsets()
	left := frame.Left + m.activityChromeWidth()
	if m.sidebarHostVisible() {
		left += m.sidebarPanelWidth() + 1
	}
	width := max(0, m.width-frame.Right-left)
	pad, inner := boundedContentGeometry(width, m.Theme.Spacing.PadLR)
	dockH := m.termDockHeightClamped()
	bottom := max(m.shellTopRow(), m.height-m.chromeBottomRows()-dockH)
	p := m.ensureMainPanes()
	p.resolve(ui.Rect{X: left, Y: m.shellTopRow(), W: width, H: max(0, bottom-m.shellTopRow())})
	m.Win.SetWidth(inner)
	lay := contentLayout{SidebarW: left, ContentW: width, InnerW: inner, PadLR: pad}
	if dockH > 0 {
		lay.TermDockRect = ui.Rect{X: left, Y: bottom, W: width, H: dockH}
	}
	m.termDockRect = lay.TermDockRect
	if m.Tree != nil && m.Tree.Visible && m.Tree.Model != nil {
		m.Tree.YOffset = m.shellTopRow()
		m.Tree.Model.Update(filetree.TreeHeightMsg{Height: max(0, m.height-m.chromeBottomRows()-m.shellTopRow())})
	}
	// Derive focused-view geometry using the same pane layout as painting.
	p.configure()
	if g := p.groups[m.Win.ActiveGroup()]; g != nil {
		lay.ChatRect, lay.InputRect, lay.EditorRect, lay.DeckRect = g.chat, g.input, g.editor, g.deck
		lay.ChatAreaTop = g.body.Y
		lay.ChatH = g.body.H
		if !g.chat.Empty() {
			lay.ChatH = g.chat.H
		}
		lay.InputLineCount = g.input.H
		lay.ShowMainInput = !g.input.Empty()
	}
	m.chatRect, m.inputRect, m.editorRect, m.sessionDeckRect = lay.ChatRect, lay.InputRect, lay.EditorRect, lay.DeckRect
	m.ChatAreaHeight = lay.ChatH
	return lay
}

// configureShell updates the root-tree slots and screen projections for one
// frame. It must run after computeContentLayout so the rects are fresh.
func (m *App) configureShell() {
	// Lazy history materialization changes projected rows during configure,
	// independently of the message that caused this presentation opportunity.
	if m.mainPanes != nil && m.subPaneProjections.AnyMaterializing() {
		m.rootHost.MarkComponentPaint(m.mainPanes.ID())
	}
	m.syncPaneWindowFocus()
	m.syncEditorTabPresentation()
	m.syncPaneWindows()
	lay := m.computeContentLayout()
	m.mainPanes.configureMessages()
	frame := m.frameInsets()

	// Persistent top chrome is inset by the shell frame; frame rows/columns
	// stay background-painted. Bottom rows belong to content now.
	m.shell.setSlot(shellHeaderSlot, true, ui.Rect{
		X: frame.Left, Y: frame.Top, W: m.width - frame.Left - frame.Right, H: 2,
	})
	m.shell.setSlot(shellFooterSlot, true, ui.Rect{
		X: frame.Left, Y: m.height - frame.Bottom - 2, W: m.width - frame.Left - frame.Right, H: 2,
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

	// Shared tool sidebar, right of the activity rail.
	sidebarActive := m.sidebarHostVisible()
	sidebarW := 0
	if sidebarActive {
		sidebarW = m.sidebarPanelWidth() + 1
	}
	m.shell.setSlot(shellSidebarSlot, sidebarActive, ui.Rect{
		X: frame.Left + activityW, Y: m.shellTopRow(), W: sidebarW, H: sidebarH,
	})
	if m.activityView != activityFiles && m.activityList != nil {
		m.shell.SetScreen(shellSidebarSlot, m.activityList)
	} else if m.sidebarScreen != nil {
		m.shell.SetScreen(shellSidebarSlot, m.sidebarScreen)
	}

	m.shell.SetScreen(shellMainPanesSlot, m.mainPanes)
	m.shell.setSlot(shellMainPanesSlot, true, m.mainPanes.Bounds())
	m.shell.setSlot(shellTermDockSlot, !lay.TermDockRect.Empty(), lay.TermDockRect)
	if m.termDock != nil && !lay.TermDockRect.Empty() {
		m.termDock.SetFocused(m.termDockFocused)
		m.termDock.PrepareResize(lay.TermDockRect.Y, 8, 6)
	}

	// Screen projections/binds (paint happens once via rootHost.Paint).
	m.configureHeaderScreen()
	m.configureFooterScreen()
	m.configureSidebarScreen()
	m.refreshActivityList()
	m.configureLauncherScreen()
}

func (m *App) isEditorActive() bool {
	return m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen()
}

// renderShellToSurface re-derives the single hit/focus/paint geometry, then
// paints only Host-owned source damage into the retained shell grid. Changed
// geometry or mounted identity clears the full grid, including vacated cells.
func (m *App) renderShellToSurface() {
	m.prepareShellSurface()
	m.paintShellSurface()
}

func (m *App) prepareShellSurface() {
	if m.mainSurf == nil || m.rootHost == nil || m.shell == nil {
		return
	}
	if m.mainSurf.Width() != m.width || m.mainSurf.Height() != m.height {
		m.mainSurf.Resize(m.width, m.height)
	}
	m.mainSurf.SetPosition(0, 0)
	m.mainSurf.SetVisible(true)
	m.shell.ClearDirty()
	m.rootHost.SetFrame(m.SpinFrame)
	m.rootHost.Resize(m.width, m.height)
	m.rootHost.RefreshLayout()
	// Structural tab operations can mount a different content screen. Resolve
	// focus after that screen is registered, before the first frame paints.
	m.rootHost.Layout()
	m.syncRootFocus()
}

func (m *App) paintShellSurface() {
	if m.mainSurf == nil || m.rootHost == nil || m.shell == nil {
		return
	}
	bg := cell.BlankCell(rendering.ThemeColorToCell(m.Theme.Colors.Bg))
	m.rootHost.PaintRegions(m.mainSurf.Buffer(), bg)
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
		switch m.activityView {
		case activityFiles:
			id = shelltree.TreeID
			if current := m.rootHost.Focused(); current == shelltree.FilterID || current == shelltree.TreeID {
				id = current
			}
		default:
			id = activitylist.ListID
			if current := m.rootHost.Focused(); current == activitylist.FilterID || current == activitylist.ListID {
				id = current
			}
		}
	case FocusTabBar:
		id = m.tabScreen.TabsControl().ID()
	case FocusEditor:
		id = m.editorScreen.ID()
	case FocusLauncher:
		id = launcher.ScreenID
		if m.launcherScreen != nil && m.rootHost.Focused() == launcher.ScreenID {
			m.launcherScreen.SyncFocus()
		}
		if current := m.rootHost.Focused(); m.launcherScreen != nil && m.launcherScreen.OwnsFocus(current) {
			id = current
		}
	case FocusInput:
		if m.emptyPaneActive() {
			id = m.ensureMainPanes().group(m.Win.ActiveGroup()).empty.ID()
		} else if view := m.activeGitReview(); view != nil {
			id = view.screen.DocumentID()
			if current := m.rootHost.Focused(); strings.HasPrefix(string(current), string(view.screen.ID())+"/") {
				id = current
			}
		} else if content := m.activePanelTabContent(); content != nil {
			id = preferredFocus(content)
			if current := m.rootHost.Focused(); panelOwnsFocus(content, current) {
				id = current
			}
		} else if view := m.activeLocalDiff(); view != nil {
			id = view.screen.DocumentID()
			if current := m.rootHost.Focused(); current == view.screen.ID() || strings.HasPrefix(string(current), string(view.screen.ID())+"/") {
				id = current
			}
		} else {
			id = m.inputScreen.ID()
		}
	default:
		id = m.chatScreen.ID()
	}
	// The focused group may be mounted in a window: focus lives in exactly
	// one host, so the other paints no second caret.
	host := m.hostFor(id)
	var pane *ui.Host
	if m.Win != nil {
		pane = m.paneWindowHost(m.Win.ActiveGroup())
	}
	if host != m.rootHost {
		m.rootHost.Focus(ui.RootID)
	} else if pane != nil {
		pane.Focus(ui.RootID)
	}
	host.Focus(id)
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
		if fw := m.focusedPopup(); fw != nil {
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
	if focusedFloating != "" {
		m.lastTerminalID = focusedFloating
	} else if dockActive != "" {
		m.lastTerminalID = dockActive
	}
	for id, s := range m.termScreens {
		if s == nil {
			continue
		}
		s.SetTermFocused(id == focusedFloating || id == dockActive)
	}
}

// configureHeaderScreen pushes the header's presentation data. Width mirrors
// the header slot (terminal width minus the horizontal shell frame) so the
// right-aligned clock/usage runs stay inside the padded slot.
func (m *App) configureHeaderScreen() {
	if m.headerScreen == nil {
		return
	}
	// The elapsed clock is a source even on terminal-only publications. Do not
	// let a quiet tick queue freeze it during a continuous stream of PTY frames.
	if start := m.ws.StartTime(); !start.IsZero() {
		if second := int64(time.Since(start) / time.Second); second != m.headerClockSecond {
			m.headerClockSecond = second
			m.rootHost.MarkComponentPaint(m.headerScreen.ID())
		}
	}
	frame := m.frameInsets()
	// One transport observation owns both the wordmark and its animation state.
	health := m.connectionHealth()
	left := m.statusHeaderLeftSegments()
	left[0] = shellheader.Wordmark(health.brand)
	if m.headerScreen.SetProjection(shellheader.Projection{
		ModelName: m.ModelName,
		YOLO:      m.OverlayState.IsYolo(),
		StartTime: m.ws.StartTime(),
		// No ViewLabel: the active agent already has its own window tab, so
		// the header stays on app + model only.
		Width:         m.width - frame.Left - frame.Right,
		LeftSegments:  left,
		RightSegments: m.statusHeaderRightSegments(),
		Connection:    health.brand,
	}) {
		m.rootHost.MarkComponentPaint(m.headerScreen.ID())
	}
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

func (m *App) configureChatScreenWithLayout(_ contentLayout) {
	m.bindMainPaneFocus()
	if tab := m.Win.ActiveTab(); tab != nil {
		m.configurePaneChat(m.AgentView.ActiveID(), m.mainPanes.view(tab), m.chatRect)
	}
}

func (m *App) configurePaneChat(agentID string, view *mainPaneView, rect ui.Rect) {
	if view == nil || view.chat == nil {
		return
	}
	screen := view.chat
	pad, inner := boundedContentGeometry(rect.W, m.Theme.Spacing.PadLR)
	layout := contentLayout{ContentW: rect.W, InnerW: inner, PadLR: pad}
	projection := m.subPaneProjections.GetExisting(agentID)
	if projection == nil {
		return
	}
	activeML := projection.renderer
	if rect.Empty() || screen == nil {
		return
	}
	if activeML == nil {
		return
	}

	innerW := layout.InnerW
	renderContentW, renderInnerW := layout.ContentW, innerW
	if m.mainViewResizeActive() {
		if view.reflowContentW == 0 {
			view.reflowContentW, view.reflowInnerW = renderContentW, renderInnerW
		} else {
			renderContentW, renderInnerW = view.reflowContentW, view.reflowInnerW
		}
	} else {
		view.reflowContentW, view.reflowInnerW = renderContentW, renderInnerW
	}
	if agentID == m.AgentView.ActiveID() {
		m.chatReflowContentW, m.chatReflowInnerW = renderContentW, renderInnerW
	}
	activity := m.ws.AgentActivity(agentID)
	if node := m.ws.AgentMgr().Get(agentID); node != nil && m.thinkingPaneID(node.State) != "" {
		activity.Thinking, activity.ThinkingID = "", ""
	}
	if activity.ThinkingID != "" {
		activity.ThinkingExpanded = activeML.DisclosureExpanded(activity.ThinkingID)
	}
	screen.SetActivity(activity)
	chatH := rect.H - shellchat.ActivityRows(rect.H)

	var activeAgent *workspace.AgentState
	if node := m.ws.AgentMgr().Get(agentID); node != nil {
		activeAgent = node.State
	}
	activeTyping := false
	if activeAgent != nil {
		activeTyping = activeAgent.ComputeActiveTyping()
	}

	activeML.SetStreaming(activeAgent != nil && activeAgent.Streaming)
	activeML.SetSpinFrame(m.SpinFrame)
	activeML.SetActiveTyping(activeTyping)
	if center := m.ws.Status(); center != nil {
		if status, ok := center.Agent(agentID); ok {
			activeML.SetPhase(string(status.Phase))
		}
	}
	activeML.SetChatAreaBounds(rect.Y, chatH)
	activeML.SetSize(renderContentW, rect.H)
	activeML.BindScroll(screen.ScrollModel())

	activeAgentID := agentID
	// The first export renders inline subpanes, and that render is what sets
	// each pane's ScrollModel content/viewport. The nested-gutter nesting
	// level (does any pane reserve a scrollbar column?) depends on those
	// metrics, so it must be computed after this pass; otherwise a thinking
	// pane that just appeared on this frame computes nesting=0 and shows no
	// scrollbar until the next frame.
	snapshot := activeML.ExportMessageRowsSnapshot(renderInnerW)
	msgRows, paneLineMap, contentH := snapshot.Rows, snapshot.PaneLineMap, snapshot.ContentHeight
	syncPaneScroll(screen, chatH, contentH)

	paneGutters := view.embedded.BuildPaneGutters(paneLineMap, m)
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
		syncPaneScroll(screen, chatH, contentH)
		paneGutters = view.embedded.BuildPaneGutters(paneLineMap, m)
	}

	mainSBVisible := contentH > chatH
	// The renderer owns clock-sensitive rows (including idle cursor/recovery
	// clocks). Reuse this export's generation rather than reclassifying message
	// roles or repainting the whole static transcript on every shell tick.
	if view.chatFrame != m.SpinFrame && (view.chatSource != snapshot.SourceID || view.chatGeneration != snapshot.Generation || activity.Active) {
		m.rootHost.MarkSubtreePaint(screen.ID())
	}
	view.chatSource, view.chatGeneration, view.chatFrame = snapshot.SourceID, snapshot.Generation, m.SpinFrame

	composeParams := shellchat.ComposeParams{
		InnerW:        renderInnerW,
		PadLR:         layout.PadLR,
		Theme:         m.Theme,
		NestingLevel:  nestingLevel,
		MainSBVisible: mainSBVisible,
		PaneLineMap:   paneLineMap,
		PaneGutters:   paneGutters,
	}

	paint := shellchat.PaintParams{SourceID: snapshot.SourceID, Generation: snapshot.Generation, ChangedIndices: snapshot.ChangedIndices, StructureChanged: snapshot.StructureChanged, Compose: composeParams, PaneLineMap: paneLineMap, OnPaneScroll: func(paneID string) { m.subPaneProjections.InvalidateSubPane(activeAgentID, paneID) }, OnPaneScrollChain: func(ids []string) {
		chain := make([]string, 0, len(ids)+1)
		chain = append(chain, activeAgentID)
		chain = append(chain, ids...)
		view.embedded.InvalidatePaneChain(chain)
	}}
	if screen.HasActiveSelection() {
		paint.HasSelection = true
		paint.SelStartLine, paint.SelStartCol, paint.SelEndLine, paint.SelEndCol = screen.SelectionRange()
	}
	screen.SetMessages(msgRows, paint)
}

func syncPaneScroll(screen *shellchat.Screen, viewport, content int) {
	scroll := screen.ScrollModel()
	scroll.SetViewport(viewport)
	scroll.SetContent(content)
	if scroll.AutoScroll() {
		scroll.ApplyAutoScroll()
	} else {
		scroll.Clamp()
	}
}

func (m *App) configureLauncherScreen() {
	if m.launcherScreen == nil || !m.launcherHostVisible() {
		return
	}
	m.launcherScreen.SetRegistry(m.actionRegistry)
	m.launcherScreen.SetAnimation(m.SpinFrame, time.Since(m.LastInteractionTime).Seconds())
}

func (m *App) contextualHelpSegments() (prefix string, segs []ui.HintSegment) {
	if m.emptyPaneActive() && m.focusTarget() == FocusInput {
		return "", []ui.HintSegment{ui.TextSeg("N new session · O open file · Tab choose"), ui.ActionSeg("pane.next"), ui.ActionSeg("file.close")}
	}
	if m.windowManager != nil {
		if fw := m.focusedPopup(); fw != nil {
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
			if node := m.AgentView.ActiveNode(); node != nil && node.State != nil && node.State.Completed {
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
	if node := m.AgentView.ActiveNode(); node != nil && node.State != nil && node.State.Completed {
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
