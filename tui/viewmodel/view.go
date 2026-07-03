package viewmodel

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/widgets"
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

	// Cheap layers (sidebar, header, status bar) are always re-rendered
	// every frame — they're fast surface fills + ANSI renders. The
	// expensive part is the main content (editor with chroma/glamour).
	// contentDirty tracks whether the editor tab's content actually
	// changed (keypress, scroll, cursor blink). When false, we reuse
	// the cached main content string.
	//
	// The launcher is also fast to render and has animations (spin
	// frame, filter typing, scrolling), so it always gets a full
	// re-render — no caching.
	isLauncherMain := len(m.agents) == 0 && m.activeEditorView == "" && m.launcher != nil
	useCached := !isLauncherMain && !m.contentDirty && m.mainContentCached != "" && m.mainContentSurf != nil

	m.compositor.BeginFrame()
	m.renderCtx.SpinFrame = m.spinFrame
	m.syncRenderFocus()
	m.resizeLayerSurfaces()

	// Re-add persistent layer surfaces (BeginFrame clears children).
	root := m.compositor.Root()
	if m.mainContentSurf != nil {
		root.AddChild(m.mainContentSurf)
		m.mainContentSurf.SetZOrder(0)
	}
	if m.mainSidebarSurf != nil {
		root.AddChild(m.mainSidebarSurf)
		m.mainSidebarSurf.SetZOrder(1)
		m.mainSidebarSurf.SetVisible(m.treeVisible)
	}
	if m.mainStatusbarSurf != nil {
		root.AddChild(m.mainStatusbarSurf)
		m.mainStatusbarSurf.SetZOrder(2)
	}
	if m.mainHeaderSurf != nil {
		root.AddChild(m.mainHeaderSurf)
		m.mainHeaderSurf.SetZOrder(3)
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

	// Cheap layers: always re-render (surface fill + ANSI render).
	m.renderHeaderToSurface()
	if m.treeVisible {
		m.renderSidebarToSurface()
	}
	m.renderStatusbarToSurface()

	m.windowManager.Render(m.compositor.Root(), m.renderCtx)
	if isDragging {
		m.windowManager.ClearNeedsRender()
	}

	m.compositor.Composite()

	output := m.compositor.Present()
	m.dirty = false
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
	sidebarVisible := m.treeVisible && m.treeModel != nil && m.treeRenderer != nil
	if sidebarVisible {
		sidebarW = m.treeWidth + 1 // +1 for divider
	}

	m.win.SetWidth(m.width - 2*m.theme.Spacing.PadLR - sidebarW)
	m.overlay.SetWidth(m.width - 2*m.theme.Spacing.PadLR - sidebarW)

	pad := strings.Repeat(" ", m.theme.Spacing.PadLR)
	contentW := m.width - sidebarW
	innerW := contentW - m.theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)

	isSpecialMode := m.Mode != ModeChat
	showMainInputForQuestion := m.Mode == ModeQuestion && m.questionForm.IsTextQuestion(m.questionForm.Focused())
	isEditorActive := m.activeEditorView != "" && m.editor.IsOpen()

	m.syncFormHint()

	var inputView string
	var inputLineCount int
	if isSpecialMode && !showMainInputForQuestion {
		inputLineCount = 1
		hint := m.formHint
		if hint == "" {
			hint = " "
		}
		inputView = dim.Render(hint)
	} else if len(m.agents) == 0 && m.activeEditorView == "" {
		inputLineCount = 1
		hint := "↑↓: navigate  ·  enter: open  ·  type to search sessions"
		hintW := rendering.VisibleWidth(hint)
		if hintW < innerW {
			hint += strings.Repeat(" ", innerW-hintW)
		}
		inputView = dim.Render(hint)
	} else if isEditorActive {
		inputLineCount = 1
		fileInfo := m.editor.Title()
		cursorInfo := fmt.Sprintf("%d:%d", m.editor.CursorLine()+1, m.editor.CursorCol()+1)
		lineCount := m.editor.LineCount()
		hintText := fmt.Sprintf("[ %s  %s  %dL  ·  ctrl+s save  ctrl+z undo  ctrl+y redo  esc close ]", fileInfo, cursorInfo, lineCount)
		inputView = dim.Render(hintText)
	} else {
		inputView = m.input.View(contentW, m.height, false, false, m.spinFrame)
		inputLineCount = len(strings.Split(inputView, "\n"))
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	asyncBarLines := m.statusbar.RenderAsyncBar()
	asyncLineCount := len(asyncBarLines)

	agentTabBarLine := ""
	agentTabBarVisible := false
	if m.win.IsOpen() && m.win.TabCount() >= 1 {
		agentTabBarLine = m.win.RenderTabBar()
		agentTabBarVisible = agentTabBarLine != ""
	}
	if m.tabBarFocused && agentTabBarVisible {
		focusStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent)
		agentTabBarLine = focusStyle.Render(agentTabBarLine)
	}

	overlayLines := m.renderOverlayArea()
	overlayLineCount := len(overlayLines)

	nonChat := 4 + inputLineCount + overlayLineCount
	if agentTabBarVisible {
		nonChat += 2
	}
	if asyncLineCount > 0 {
		nonChat += 2 + asyncLineCount
	}

	chatH := m.height - 2 - nonChat
	if chatH < 1 {
		chatH = 1
	}
	m.chatAreaHeight = chatH

	var chatVisible []string
	var totalSBW int

	if isEditorActive {
		editorPadL := m.theme.Spacing.PadLR
		editorPadR := m.theme.Spacing.PadLR
		if sidebarVisible {
			editorPadL = 0
		}
		m.editor.SetPadding(editorPadL)
		chatVisible = m.editor.View(contentW, chatH, editorPadL, editorPadR)
		totalSBW = 0
	} else if len(m.agents) == 0 && m.activeEditorView == "" {
		if m.launcher != nil {
			launcherLines := m.renderLauncherMainView(innerW, chatH)
			chatVisible = launcherLines
		} else {
			chatVisible = []string{""}
		}
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
			nestingLevel := m.paneMgr.ScrollbarNestingLevel()
			paneSBW := nestingLevel*scrollbar.ScrollbarWidth + nestingLevel
			if activeAgent := m.agents[m.activeAgentView]; activeAgent != nil && activeAgent.MsgList != nil {
				activeAgent.MsgList.SetPaneScrollbarWidth(paneSBW)
			}
			if activeML != nil {
				activeML.SetPaneScrollbarWidth(0)
			}
			activeML.SetStreaming(activeAgent != nil && activeAgent.Streaming)
			activeML.SetSpinFrame(m.spinFrame)
			activeML.SetActiveTyping(activeTyping)
			activeML.SetPhase(m.statusbar.Phase())
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
							sp := m.paneMgr.Get(info.PaneID)
							if sp != nil {
								view := sp.ScrollbarView()
								paneSBViews[info.PaneID] = strings.Split(view, "\n")
							}
						}
					}
				}
			}

			subPaneBgSeq := ""
			{
				s := lipgloss.NewStyle().Background(m.theme.Colors.SubPaneBg).Render(" ")
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
					sp := m.paneMgr.Get(paneLineMap[i].PaneID)
					hasBg = sp != nil && sp.Type == PaneAgent
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
		if m.tabBarFocused {
			accentSepStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent)
			lines = append(lines, accentSepStyle.Render(strings.Repeat("━", contentW)))
		} else {
			lines = append(lines, widgets.RenderSep(contentW, m.theme.Colors.Border))
		}
	}

	for _, line := range chatVisible {
		lines = append(lines, line)
	}

	// Overlay area (settings, confirm, question, yolo) rendered inline.
	// TextOverlay and FilePicker remain floating windows.
	if overlayLineCount > 0 {
		for _, ol := range overlayLines {
			visW := rendering.VisibleWidth(ol)
			if visW > innerW {
				lines = append(lines, ol)
			} else {
				rightPad := innerW - visW
				if rightPad < 0 {
					rightPad = 0
				}
				lines = append(lines, pad+ol+strings.Repeat(" ", rightPad)+pad)
			}
		}
	}

	if asyncLineCount > 0 {
		lines = append(lines, "")
		lines = append(lines, widgets.RenderSep(contentW, m.theme.Colors.Border))
		for _, al := range asyncBarLines {
			visW := rendering.VisibleWidth(al)
			rightPad := contentW - 2*m.theme.Spacing.PadLR - visW
			if rightPad < 0 {
				rightPad = 0
			}
			lines = append(lines, pad+al+strings.Repeat(" ", rightPad)+pad)
		}
	}

	lines = append(lines, widgets.RenderSep(contentW, m.theme.Colors.Border))

	if isSpecialMode && !isEditorActive {
		visW := rendering.VisibleWidth(inputView)
		rightPad := contentW - 2*m.theme.Spacing.PadLR - visW
		if rightPad < 0 {
			rightPad = 0
		}
		lines = append(lines, pad+inputView+strings.Repeat(" ", rightPad)+pad)
	} else if isEditorActive {
		visW := rendering.VisibleWidth(inputView)
		rightPad := contentW - 2*m.theme.Spacing.PadLR - visW
		if rightPad < 0 {
			rightPad = 0
		}
		lines = append(lines, pad+inputView+strings.Repeat(" ", rightPad)+pad)
	} else {
		refChips := m.renderReferenceChips()
		if len(refChips) > 0 {
			for _, rc := range refChips {
				visW := rendering.VisibleWidth(rc)
				rightPad := contentW - 2*m.theme.Spacing.PadLR - visW
				if rightPad < 0 {
					rightPad = 0
				}
				lines = append(lines, pad+rc+strings.Repeat(" ", rightPad)+pad)
			}
		}
		for _, il := range strings.Split(inputView, "\n") {
			visW := rendering.VisibleWidth(il)
			rightPad := contentW - 2*m.theme.Spacing.PadLR - visW
			if rightPad < 0 {
				rightPad = 0
			}
			lines = append(lines, pad+il+strings.Repeat(" ", rightPad)+pad)
		}
	}

	// The separator after the input and the status bar below are now
	// rendered into the status bar layer surface (mainStatusbarSurf).
	// Do NOT append them here.

	// The sidebar is rendered into its own layer surface
	// (mainSidebarSurf). Update tree height/scroll state for it.
	if m.treeVisible && m.treeModel != nil && m.treeRenderer != nil {
		treeStart := 3
		m.treeYOffset = treeStart
		sidebarH := contentH - treeStart
		if sidebarH < 0 {
			sidebarH = 0
		}
		m.treeModel.Update(filetree.TreeHeightMsg{Height: sidebarH})
		m.treeRenderer.SetScrollbarContentHeight(sidebarH)
	}

	if len(lines) > contentH {
		lines = lines[:contentH]
	}

	return strings.Join(lines, "\n")
}

func (m *App) renderOverlayArea() []string {
	if !m.overlay.IsOpen() {
		return nil
	}

	activeTab := m.overlay.ActiveTab()
	if activeTab == nil {
		return nil
	}

	// Compute content width accounting for sidebar
	sidebarW := 0
	if m.treeVisible && m.treeModel != nil && m.treeRenderer != nil {
		sidebarW = m.treeWidth + 1
	}
	contentW := m.width - sidebarW

	m.overlay.SetWidth(contentW - 2*m.theme.Spacing.PadLR)

	tabBarLine := m.overlay.RenderTabBar()

	contentLines := m.getOverlayContentLines(activeTab.Title)

	stripLeadingTrailingEmpty := func(ss []string) []string {
		start := 0
		for start < len(ss) && ss[start] == "" {
			start++
		}
		end := len(ss)
		for end > start && ss[end-1] == "" {
			end--
		}
		return ss[start:end]
	}
	contentLines = stripLeadingTrailingEmpty(contentLines)

	var lines []string
	lines = append(lines, widgets.RenderSep(contentW, m.theme.Colors.Border))
	if m.overlay.TabCount() >= 2 {
		lines = append(lines, tabBarLine)
		lines = append(lines, widgets.RenderSep(contentW, m.theme.Colors.Border))
	}
	lines = append(lines, contentLines...)
	return lines
}

func (m *App) getOverlayContentLines(tabTitle string) []string {
	var rawLines []string

	// Compute content width accounting for sidebar
	sidebarW := 0
	if m.treeVisible && m.treeModel != nil && m.treeRenderer != nil {
		sidebarW = m.treeWidth + 1
	}
	overlayContentW := m.width - sidebarW - 2*m.theme.Spacing.PadLR
	if overlayContentW < 20 {
		overlayContentW = 20
	}

	switch {
	case tabTitle == "Settings":
		settingsView := m.settings.View(m.spinFrame)
		for _, sl := range strings.Split(settingsView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(sl, overlayContentW))
		}
	case tabTitle == "Confirm":
		m.confirm.SetWidth(overlayContentW)
		confirmView := m.confirm.View(m.spinFrame)
		for _, cl := range strings.Split(confirmView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(cl, overlayContentW))
		}
	case tabTitle == "Question":
		questionView := m.questionForm.View(m.spinFrame, overlayContentW)
		for _, ql := range strings.Split(questionView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(ql, overlayContentW))
		}
	case tabTitle == "YOLO":
		m.yoloDlg.SetWidth(overlayContentW)
		yoloView := m.yoloDlg.View()
		for _, yl := range strings.Split(yoloView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(yl, overlayContentW))
		}
	}

	return rawLines
}

func (m *App) syncFormHint() {
	if m.tabBarFocused {
		m.formHint = "[ tab bar focused · ← →: navigate · enter: select · esc: unfocus ]"
		return
	}
	if !m.overlay.IsOpen() {
		m.formHint = ""
		return
	}
	activeTab := m.overlay.ActiveTab()
	if activeTab == nil {
		m.formHint = ""
		return
	}
	switch {
	case activeTab.Title == "Settings":
		m.formHint = m.settings.Hint()
	case activeTab.Title == "Confirm":
		m.formHint = "[ confirmation required · ← →: choose · enter: select · y/a/n: quick select · esc: deny ]"
	case activeTab.Title == "YOLO":
		m.formHint = "[ yolo mode confirmation · ← →: choose · enter: select · y/n: quick select · esc: cancel ]"
	case activeTab.Title == "Question":
		m.formHint = m.questionForm.Hint()
	default:
		m.formHint = ""
	}
}

// renderLauncherMainView renders the launcher in the main content area.
// Renders launcher session content (same pattern as buildMainView but replaces chat
// area with launcher session list). Sidebar support is inherited from buildMainView.
func (m *App) renderLauncherMainView(innerW, chatH int) []string {
	if m.launcher == nil || innerW < 20 || chatH < 3 {
		return []string{""}
	}

	idleSeconds := time.Since(m.lastInteractionTime).Seconds()
	visibleSessions := m.launcher.visibleSessions()
	sessionLines := widgets.RenderLauncher(
		visibleSessions,
		m.launcher.selected,
		m.launcher.filterActive,
		m.launcher.filterText,
		m.theme,
		innerW,
		chatH,
		m.spinFrame,
		idleSeconds,
	)

	// Apply scroll offset for long lists
	totalSessionLines := len(sessionLines)
	scrollOffset := m.launcher.scrollOffset

	selectedLineIdx := m.launcherSelectedLineIndex(sessionLines)
	if selectedLineIdx >= scrollOffset+chatH {
		scrollOffset = selectedLineIdx - chatH + 1
	}
	if selectedLineIdx < scrollOffset {
		scrollOffset = selectedLineIdx
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	m.launcher.scrollOffset = scrollOffset

	endIdx := scrollOffset + chatH
	if endIdx > totalSessionLines {
		endIdx = totalSessionLines
	}
	var visibleLines []string
	if scrollOffset < totalSessionLines {
		visibleLines = sessionLines[scrollOffset:endIdx]
	}

	// Pad visible lines to fill the height and match the chat content wrapping
	// (pad+inner+pad pattern from activeML.Render's no-scrollbar path).
	padStr := strings.Repeat(" ", m.theme.Spacing.PadLR)
	result := make([]string, chatH)
	for i := 0; i < chatH; i++ {
		if i < len(visibleLines) {
			line := visibleLines[i]
			lw := rendering.VisibleWidth(line)
			padRight := innerW - lw
			if padRight < 0 {
				padRight = 0
			}
			result[i] = padStr + line + strings.Repeat(" ", padRight) + padStr
		} else {
			result[i] = strings.Repeat(" ", innerW + 2*m.theme.Spacing.PadLR)
		}
	}
	return result
}

// launcherSelectedLineIndex finds which rendered line the selected item is on.
func (m *App) launcherSelectedLineIndex(sessionLines []string) int {
	if m.launcher == nil {
		return 0
	}
	// The selected index maps to rendered lines:
	// Index 0 = "New Session" = line 0
	// Then date separators and session lines follow.
	// We need to find which line corresponds to the selected item.
	if m.launcher.selected == 0 {
		return 0
	}

	// Walk through the groups to find the line
	visibleSessions := m.launcher.visibleSessions()
	groups := widgets.BuildLauncherGroups(visibleSessions)

	innerW := m.width - m.theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}
	newSessionLines := widgets.RenderNewSessionEntry(0 == m.launcher.selected, m.theme, innerW, m.spinFrame, 0)
	transitionLines := 0
	if len(visibleSessions) > 0 {
		transitionLines = 2
	}
	lineIdx := len(newSessionLines) + transitionLines
	flatIdx := 1

	for _, g := range groups {
		lineIdx++ // date separator
		for range g.Sessions {
			if flatIdx == m.launcher.selected {
				return lineIdx
			}
			lineIdx++
			flatIdx++
		}
	}
	return 0
}

func (m *App) renderReferenceChips() []string {
	refs := m.input.PromptReferences()
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
			Foreground(m.theme.Colors.Accent2).
			Background(m.theme.Colors.Selection).
			Padding(0, 1)
		closeStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Error).Bold(true)
		chip := chipStyle.Render(label) + closeStyle.Render(" ×")
		chips = append(chips, chip)
	}
	if len(chips) == 0 {
		return nil
	}
	return []string{strings.Join(chips, " ")}
}


