package viewmodel

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/widgets"
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

	// Only cache the main content when the editor is the active view —
	// chroma syntax highlighting is expensive. Agent tabs use the fresh
	// main-view path, while the native launcher surface is painted every
	// frame so cursor, hover, scroll, and animation state stay responsive.
	isEditorMain := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	useCached := isEditorMain && m.sessionDeckHeight(m.height) == 0 && !m.contentDirty && m.mainContentCached != "" && m.mainContentSurf != nil

	m.compositor.BeginFrame()
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

	// Main content surface stays transparent — only ANSI-rendered cells
	// set explicit backgrounds. Transparent cells (like the top padding
	// row) show the terminal default, matching the original behaviour.
	if useCached {
		rendering.RenderToSurface(m.mainContentSurf, m.mainContentCached)
	} else {
		mainContent := m.buildMainView()
		m.mainContentCached = mainContent
		m.contentDirty = false
		rendering.RenderToSurface(m.mainContentSurf, mainContent)
	}
	m.renderSessionDeckToSurface()
	m.renderLauncherToSurface()

	// Cheap layers: always re-render (surface fill + ANSI render).
	m.renderHeaderToSurface()
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
	sidebarVisible := m.Tree.Visible && m.Tree.Model != nil && m.Tree.Renderer != nil
	if sidebarVisible {
		sidebarW = m.Tree.Width + 1 // +1 for divider
	}

	m.Win.SetWidth(m.width - 2*m.Theme.Spacing.PadLR - sidebarW)

	pad := strings.Repeat(" ", m.Theme.Spacing.PadLR)
	contentW := m.width - sidebarW
	innerW := contentW - m.Theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	isEditorActive := m.EditorState.ActiveView != "" && m.EditorState.Editor.IsOpen()
	isLauncherActive := m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" && m.launcherScreen != nil
	showMainInput := !isLauncherActive && !isEditorActive && !m.OverlayState.IsOverlay()

	m.syncFormHint()

	var inputView string
	var inputLineCount int
	if showMainInput {
		inputView = m.Input.View(contentW, m.height, false, false, m.SpinFrame)
		inputLineCount = len(strings.Split(inputView, "\n"))
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	agentTabBarLine := ""
	agentTabBarVisible := false
	if m.Win.IsOpen() && m.Win.TabCount() >= 1 {
		agentTabBarLine = m.Win.RenderTabBar()
		agentTabBarVisible = agentTabBarLine != ""
	}
	if m.FocusStack.Top() == FocusTabBar && agentTabBarVisible {
		focusStyle := lipgloss.NewStyle().Foreground(m.Theme.Colors.Accent)
		agentTabBarLine = focusStyle.Render(agentTabBarLine)
	}

	baseNonChat := 3 + inputLineCount
	if showMainInput {
		baseNonChat++ // separator above the actual composer
	}
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
	var totalSBW int

	if isEditorActive {
		editorPadL := m.Theme.Spacing.PadLR
		editorPadR := m.Theme.Spacing.PadLR
		if sidebarVisible {
			editorPadL = 0
		}
		m.EditorState.Editor.SetPadding(editorPadL)
		chatVisible = m.EditorState.Editor.View(contentW, chatH, editorPadL, editorPadR)
		totalSBW = 0
	} else if m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		// The native launcher screen paints into its own surface; the main
		// content only needs to reserve the vertical space.
		chatVisible = make([]string, chatH)
		totalSBW = 0
	} else {
		activeML := m.activeMessageList()
		activeAgent := m.agentForView()
		activeTyping := false
		if activeAgent != nil {
			activeTyping = activeAgent.ComputeActiveTyping()
		}
		if activeML == nil {
			chatVisible = []string{""}
			totalSBW = 0
		} else {
			nestingLevel := m.subPaneProjections.ScrollbarNestingLevel(m.paneDoneMap())
			paneSBW := nestingLevel*scrollbar.ScrollbarWidth + nestingLevel
			if sp := m.PaneMgr.Get(m.AgentMgr.ActiveID()); sp != nil && m.subPaneProjections.Has(sp.ID) {
				m.subPaneProjections.SetPaneScrollbarWidth(sp.ID, paneSBW)
			}
			if activeML != nil {
				activeML.SetPaneScrollbarWidth(0)
			}
			activeML.SetStreaming(activeAgent != nil && activeAgent.Streaming)
			activeML.SetSpinFrame(m.SpinFrame)
			activeML.SetActiveTyping(activeTyping)
			activeML.SetPhase(m.StatusBar.Phase())
			chatAreaTop := 3
			if agentTabBarVisible {
				chatAreaTop += 2
			}
			activeML.SetChatAreaBounds(chatAreaTop, chatH)

			activeML.SetSize(m.width-sidebarW, m.height)
			activeML.SetLeftOffset(sidebarW)

			chatVisible = activeML.Render(innerW)

			mainSBVisible := activeML.ScrollbarWidth() > 0

			var mainSBLines []string
			var paneLineMap []widgets.PaneLineInfo
			var paneSBViews map[string][]string

			totalSBW = scrollbar.ScrollbarWidth * (1 + nestingLevel)
			if nestingLevel > 0 {
				totalSBW += nestingLevel
			}
			if !mainSBVisible && nestingLevel == 0 {
				totalSBW = 0
			}
			if !mainSBVisible && nestingLevel > 0 {
				totalSBW = scrollbar.ScrollbarWidth*nestingLevel + nestingLevel
			}

			mainScrollbarView := activeML.ScrollbarView()
			mainSBLines = strings.Split(mainScrollbarView, "\n")

			paneLineMap = activeML.PaneLineMap()

			paneSBViews = make(map[string][]string)
			if nestingLevel > 0 {
				for _, info := range paneLineMap {
					if info.PaneID != "" {
						if _, ok := paneSBViews[info.PaneID]; !ok {
							sp := m.PaneMgr.Get(info.PaneID)
							if sp != nil {
								view := m.subPaneProjections.ScrollbarView(info.PaneID, sp)
								paneSBViews[info.PaneID] = strings.Split(view, "\n")
							}
						}
					}
				}
			}

			subPaneBgSeq := ""
			{
				s := lipgloss.NewStyle().Background(m.Theme.Colors.SubPaneBg).Render(" ")
				idx := strings.Index(s, " ")
				if idx > 0 {
					subPaneBgSeq = s[:idx]
				}
			}

			reapplySubPaneBg := func(s string) string {
				re := regexp.MustCompile(`\x1b\[(?:0?m|39m|49m)`)
				return re.ReplaceAllString(s, "${0}"+subPaneBgSeq)
			}

			for i, line := range chatVisible {
				maxContentW := innerW - totalSBW
				if totalSBW > 0 {
					maxContentW = innerW - totalSBW - 1
				}
				if maxContentW < 10 {
					maxContentW = 10
				}

				isPaneLine := i < len(paneLineMap) && paneLineMap[i].PaneID != ""
				hasBg := false
				if isPaneLine {
					sp := m.PaneMgr.Get(paneLineMap[i].PaneID)
					hasBg = sp != nil && sp.Type == workspace.PaneAgent
				}

				var trimmed string
				var visW int
				if hasBg {
					trimmed = line
					visW = rendering.VisibleWidth(trimmed)
					if visW > maxContentW {
						trimmed = rendering.TruncateLine(trimmed, maxContentW)
						visW = rendering.VisibleWidth(trimmed)
					}
				} else {
					trimmed = rendering.TrimRightAnsiSpaces(line)
					visW = rendering.VisibleWidth(trimmed)
					if visW > maxContentW {
						trimmed = rendering.TruncateLine(trimmed, maxContentW)
						visW = rendering.VisibleWidth(trimmed)
					}
				}
				line = trimmed

				if totalSBW > 0 {
					var paneSBLine, mainSBLine string

					if nestingLevel > 0 && isPaneLine {
						info := paneLineMap[i]
						if paneSBLines, ok := paneSBViews[info.PaneID]; ok {
							if info.LineIdx >= 0 && info.LineIdx < len(paneSBLines) && paneSBLines[info.LineIdx] != "" {
								paneSBLine = paneSBLines[info.LineIdx]
							}
						}
						if paneSBLine == "" {
							paneSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
						}
						if mainSBVisible && i < len(mainSBLines) {
							mainSBLine = mainSBLines[i]
						}
						if mainSBLine == "" {
							mainSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
						}
					} else if nestingLevel > 0 {
						paneSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
						if mainSBVisible && i < len(mainSBLines) {
							mainSBLine = mainSBLines[i]
						}
						if mainSBLine == "" {
							mainSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
						}
					} else {
						if mainSBVisible && i < len(mainSBLines) && mainSBLines[i] != "" {
							mainSBLine = mainSBLines[i]
						} else {
							mainSBLine = strings.Repeat(" ", scrollbar.ScrollbarWidth)
						}
					}

					padRight := innerW - totalSBW - 1 - visW
					if padRight < 0 {
						padRight = 0
					}

					var bgPart, noBgPart string
					if nestingLevel > 0 {
						bgPart = line + strings.Repeat(" ", padRight) + " " + paneSBLine
						noBgPart = " " + mainSBLine
					} else {
						bgPart = line + strings.Repeat(" ", padRight)
						noBgPart = " " + mainSBLine
					}

					if hasBg && subPaneBgSeq != "" {
						bgPart = subPaneBgSeq + reapplySubPaneBg(bgPart) + "\x1b[0m"
					}
					line = pad + bgPart + noBgPart + pad
				} else {
					padRight := innerW - visW
					if padRight < 0 {
						padRight = 0
					}
					inner := line + strings.Repeat(" ", padRight)
					if hasBg && subPaneBgSeq != "" {
						inner = subPaneBgSeq + reapplySubPaneBg(inner) + "\x1b[0m"
					}
					line = pad + inner + pad
				}

				chatVisible[i] = line
			}
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
		lines = append(lines, pad+agentTabBarLine+pad)
		if m.FocusStack.Top() == FocusTabBar {
			accentSepStyle := lipgloss.NewStyle().Foreground(m.Theme.Colors.Accent)
			lines = append(lines, accentSepStyle.Render(strings.Repeat("━", contentW)))
		} else {
			lines = append(lines, widgets.RenderSep(contentW, m.Theme.Colors.Border))
		}
	}

	for _, line := range chatVisible {
		lines = append(lines, line)
	}

	for range deckLineCount {
		lines = append(lines, strings.Repeat(" ", contentW))
	}

	if showMainInput {
		lines = append(lines, widgets.RenderSep(contentW, m.Theme.Colors.Border))
	}

	if showMainInput {
		refChips := m.renderReferenceChips()
		if len(refChips) > 0 {
			for _, rc := range refChips {
				visW := rendering.VisibleWidth(rc)
				rightPad := contentW - 2*m.Theme.Spacing.PadLR - visW
				if rightPad < 0 {
					rightPad = 0
				}
				lines = append(lines, pad+rc+strings.Repeat(" ", rightPad)+pad)
			}
		}
		for _, il := range strings.Split(inputView, "\n") {
			visW := rendering.VisibleWidth(il)
			rightPad := contentW - 2*m.Theme.Spacing.PadLR - visW
			if rightPad < 0 {
				rightPad = 0
			}
			lines = append(lines, pad+il+strings.Repeat(" ", rightPad)+pad)
		}
	}

	// The status separator below is rendered by mainStatusbarSurf.

	// The sidebar is rendered into its own layer surface
	// (mainSidebarSurf). Update tree height/scroll state for it.
	if m.Tree.Visible && m.Tree.Model != nil && m.Tree.Renderer != nil {
		treeStart := 3
		m.Tree.YOffset = treeStart
		sidebarH := contentH - treeStart
		if sidebarH < 0 {
			sidebarH = 0
		}
		m.Tree.Model.Update(filetree.TreeHeightMsg{Height: sidebarH})
		m.Tree.Renderer.SetScrollbarContentHeight(sidebarH)
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
	m.mainSessionDeckSurf.Buffer().Clear()
	projection.deckHost.Resize(r.W, r.H)
	projection.deckHost.Layout()
	m.focusSessionDeckAfterLayout()
	projection.deckHost.Paint(m.mainSessionDeckSurf.Buffer())
}

func (m *App) syncFormHint() {
	if m.FocusStack.Top() == FocusTabBar {
		m.FormHint = "[ tab bar focused · ← →: navigate · enter: select · esc: unfocus ]"
		return
	}
	m.FormHint = ""
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
	m.mainLauncherSurf.Buffer().Clear()

	m.launcherHost.Resize(innerW, chatH)
	m.launcherHost.Layout()
	m.launcherScreen.SyncFocus()
	m.launcherHost.SetFrame(m.SpinFrame)
	m.launcherHost.Paint(m.mainLauncherSurf.Buffer())
}

func (m *App) renderReferenceChips() []string {
	refs := m.Input.PromptReferences()
	if len(refs) == 0 {
		return nil
	}
	var chips []string
	for _, ref := range refs {
		if !ref.Active() {
			continue
		}
		label := ref.DisplayName()
		chipStyle := lipgloss.NewStyle().
			Foreground(m.Theme.Colors.Accent2).
			Background(m.Theme.Colors.Selection).
			Padding(0, 1)
		closeStyle := lipgloss.NewStyle().Foreground(m.Theme.Colors.Error).Bold(true)
		chip := chipStyle.Render(label) + closeStyle.Render(" ×")
		chips = append(chips, chip)
	}
	if len(chips) == 0 {
		return nil
	}
	return []string{strings.Join(chips, " ")}
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
	left := m.StatusBar.ActivityView()
	m.syncFormHint()
	right := lipgloss.NewStyle().Foreground(m.Theme.Colors.Muted).Render(m.contextualHelpView())
	leftW := rendering.VisibleWidth(left)
	rightW := rendering.VisibleWidth(right)
	available := m.width - 2*m.Theme.Spacing.PadLR
	if available < 0 {
		available = 0
	}
	if rightW > available {
		right = rendering.TruncateLine(right, available)
		rightW = rendering.VisibleWidth(right)
	}
	if leftW+rightW+2 > available {
		left = rendering.TruncateLine(left, max(0, available-rightW-2))
		leftW = rendering.VisibleWidth(left)
	}
	rightPad := available - leftW - rightW
	if rightPad < 0 {
		rightPad = 0
	}
	full := widgets.RenderSep(m.width, m.Theme.Colors.Border) + "\n" + pad + left + strings.Repeat(" ", rightPad) + right + pad
	rendering.RenderToSurface(m.mainStatusbarSurf, full)
}

func (m *App) contextualHelpView() string {
	if m.windowManager != nil {
		if fw := m.windowManager.Focused(); fw != nil && fw.ID == findInFilesWindowID {
			return "type to search  ·  ↑↓ preview  ·  enter open  ·  double-click open  ·  ctrl+r regex  ·  esc close"
		}
	}
	if m.sessionDeckFocused() {
		return "tab next  ·  shift+tab previous  ·  ctrl+[ ] switch panels  ·  esc back"
	}
	if m.launcherScreen != nil && m.AgentMgr.Count() == 0 && m.EditorState.ActiveView == "" {
		return m.launcherHelpView()
	}
	if m.FocusStack != nil && m.FocusStack.Top() == FocusTabBar {
		return compactStatusHint(m.FormHint)
	}
	if m.OverlayState.IsOverlay() {
		return compactStatusHint(m.FormHint)
	}
	if m.EditorState.ActiveView != "" && m.EditorState.Editor != nil && m.EditorState.Editor.IsOpen() {
		info := fmt.Sprintf(
			"%s  %d:%d  %dL",
			m.EditorState.Editor.Title(),
			m.EditorState.Editor.CursorLine()+1,
			m.EditorState.Editor.CursorCol()+1,
			m.EditorState.Editor.LineCount(),
		)
		editorHintIDs := []ui.ActionID{"file.save", "edit.undo", "edit.redo"}
		hints := make([]string, 0, len(editorHintIDs)+1)
		if m.actionRegistry != nil {
			for _, id := range editorHintIDs {
				if hl := m.actionRegistry.HintLabelFor(id); hl != "" {
					hints = append(hints, hl)
				}
			}
		}
		hints = append(hints, "esc close")
		return info + "  ·  " + strings.Join(hints, "  ·  ")
	}
	return m.Input.ContextHint()
}

func compactStatusHint(hint string) string {
	hint = strings.TrimSpace(hint)
	if !strings.HasPrefix(hint, "[") {
		return hint
	}
	end := strings.Index(hint, "]")
	if end < 0 {
		return strings.TrimSpace(strings.TrimPrefix(hint, "["))
	}
	main := strings.TrimSpace(hint[1:end])
	rest := strings.TrimSpace(hint[end+1:])
	if rest != "" {
		return main + "  ·  " + rest
	}
	return main
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

	viewLabel := ""
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
		viewLabel = agentLabel
		if catLabel != "" {
			viewLabel = catLabel + ":" + agentLabel
		}
	}
	headerLine := widgets.RenderHeader(
		m.Theme,
		m.ModelName,
		m.OverlayState.IsYolo(),
		m.StartTime,
		m.StatusBar.UsageView(),
		viewLabel,
		m.width,
	)

	// Row 0: header line
	// Row 1: separator
	full := pad + headerLine + pad + "\n" + widgets.RenderSep(m.width, m.Theme.Colors.Border)
	rendering.RenderToSurface(m.mainHeaderSurf, full)
}
