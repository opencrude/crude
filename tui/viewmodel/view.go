package viewmodel

import (
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/widgets"
)

func (m *App) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("")
	}

	if m.Mode == ModeLauncher && m.launcher != nil {
		v := tea.NewView(m.renderLauncherView())
		v.AltScreen = true
		v.MouseMode = tea.MouseModeAllMotion
		return v
	}

	// === Compositing Engine Pipeline ===

	// 1. Begin frame — clear back buffer, reset damage, clear root children
	m.compositor.BeginFrame()

	// 2. Update render context
	m.renderCtx.SpinFrame = m.spinFrame

	// 3. Render main content (background) into root surface
	//    This uses the existing rendering logic but writes into the cell buffer
	//    via the ANSI parser (RenderToSurface).
	mainContent := m.buildMainView()
	rendering.RenderToSurface(m.compositor.Root(), mainContent)

	// 4. Render floating windows on top
	m.windowManager.Render(m.compositor.Root(), m.renderCtx)

	// 5. Composite all surfaces into back buffer
	m.compositor.Composite()

	// 6. Generate ANSI output and present
	output := m.compositor.Present()

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
	m.win.SetWidth(m.width - 2*m.theme.Spacing.PadLR)
	m.overlay.SetWidth(m.width - 2*m.theme.Spacing.PadLR)

	pad := strings.Repeat(" ", m.theme.Spacing.PadLR)
	innerW := m.width - m.theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	dim := lipgloss.NewStyle().Foreground(m.theme.Colors.Muted)

	isSpecialMode := m.Mode != ModeChat
	showMainInputForQuestion := m.Mode == ModeQuestion && m.questionForm.IsTextQuestion(m.questionForm.Focused())

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
	} else {
		inputView = m.input.View(m.width, m.height, false, false, m.spinFrame)
		inputLineCount = len(strings.Split(inputView, "\n"))
		if inputLineCount < 1 {
			inputLineCount = 1
		}
	}

	asyncBarLines := m.statusbar.RenderAsyncBar()
	asyncLineCount := len(asyncBarLines)

	agentTabBarLine := ""
	agentTabBarVisible := false
	if m.win.IsOpen() && m.win.TabCount() >= 2 {
		agentTabBarLine = m.win.RenderTabBar()
		agentTabBarVisible = agentTabBarLine != ""
	}
	if m.tabBarFocused && agentTabBarVisible {
		focusStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent)
		agentTabBarLine = focusStyle.Render(agentTabBarLine)
	}

	overlayLines := m.renderOverlayArea()
	overlayLineCount := len(overlayLines)

	nonChat := 6 + inputLineCount + overlayLineCount
	if agentTabBarVisible {
		nonChat += 2
	}
	if asyncLineCount > 0 {
		nonChat += 2 + asyncLineCount
	}

	chatH := m.height - nonChat
	if chatH < 1 {
		chatH = 1
	}
	m.chatAreaHeight = chatH

	activeML := m.activeMessageList()
	activeAgent := m.agentForView()
	activeTyping := false
	if activeAgent != nil {
		activeTyping = activeAgent.ComputeActiveTyping()
	}

	nestingLevel := m.paneMgr.ScrollbarNestingLevel()
	paneSBW := nestingLevel*scrollbar.ScrollbarWidth + nestingLevel
	ma := m.mainAgent()
	ma.MsgList.SetPaneScrollbarWidth(paneSBW)

	if activeML != ma.MsgList {
		activeML.SetPaneScrollbarWidth(0)
	}
	activeML.SetStreaming(activeAgent != nil && activeAgent.Streaming)
	activeML.SetSpinFrame(m.spinFrame)
	activeML.SetActiveTyping(activeTyping)
	activeML.SetPhase(m.statusbar.Phase())
	activeML.SetSize(m.width, m.height)
	chatAreaTop := 3
	if agentTabBarVisible {
		chatAreaTop += 2
	}
	activeML.SetChatAreaBounds(chatAreaTop, chatH)

	mainSBVisible := activeML.ScrollbarWidth() > 0

	var totalSBW int
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

	chatVisible := activeML.Render(innerW)

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

	lines := make([]string, 0, m.height)

	lines = append(lines, "")

	headerLine := widgets.RenderHeader(m.theme, m.modelName, m.yolo, m.startTime, m.width)
	if m.activeAgentView != "" && m.activeAgentView != MainAgentID {
		agentLabel := m.activeAgentView
		if lbl, ok := m.agentLabels[m.activeAgentView]; ok && lbl != "" {
			agentLabel = lbl
		}
		sp := m.paneMgr.Get(m.activeAgentView)
		catLabel := ""
		if sp != nil {
			catLabel = string(sp.Type)
		}
		viewLabel := agentLabel
		if catLabel != "" {
			viewLabel = catLabel + ":" + agentLabel
		}
		accentStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent).Bold(true)
		headerLine += " " + accentStyle.Render("⟨"+viewLabel+"⟩")
	}
	lines = append(lines, pad+headerLine+pad)
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	if agentTabBarVisible {
		lines = append(lines, pad+agentTabBarLine+pad)
		if m.tabBarFocused {
			accentSepStyle := lipgloss.NewStyle().Foreground(m.theme.Colors.Accent)
			lines = append(lines, accentSepStyle.Render(strings.Repeat("━", m.width)))
		} else {
			lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))
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

	// reapplySubPaneBg replaces ANSI reset/selective-reset sequences within s
	// and appends subPaneBgSeq after each one, ensuring the sub-pane background
	// persists across style resets emitted by glamour, chroma, etc.
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

		lines = append(lines, line)
	}

	// Overlay area (settings, confirm, question, yolo) rendered inline.
	// TextOverlay and FilePicker remain floating windows.
	if overlayLineCount > 0 {
		for _, ol := range overlayLines {
			lines = append(lines, ol)
		}
	}

	if asyncLineCount > 0 {
		lines = append(lines, "")
		lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))
		for _, al := range asyncBarLines {
			lines = append(lines, pad+al)
		}
	}

	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	if isSpecialMode {
		lines = append(lines, pad+inputView)
	} else {
		refChips := m.renderReferenceChips()
		if len(refChips) > 0 {
			for _, rc := range refChips {
				lines = append(lines, pad+rc)
			}
		}
		for _, il := range strings.Split(inputView, "\n") {
			lines = append(lines, pad+il)
		}
	}

	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	statusView := m.statusbar.View()
	lines = append(lines, pad+statusView)

	if len(lines) > m.height {
		lines = lines[:m.height]
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

	m.overlay.SetWidth(m.width - 2*m.theme.Spacing.PadLR)

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
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))
	if m.overlay.TabCount() >= 2 {
		lines = append(lines, tabBarLine)
		lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))
	}
	lines = append(lines, contentLines...)
	return lines
}

func (m *App) getOverlayContentLines(tabTitle string) []string {
	var rawLines []string

	contentW := m.width - 2*m.theme.Spacing.PadLR
	if contentW < 20 {
		contentW = 20
	}

	switch {
	case tabTitle == "Settings":
		settingsView := m.settings.View(m.spinFrame)
		for _, sl := range strings.Split(settingsView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(sl, contentW))
		}
	case tabTitle == "Confirm":
		m.confirm.SetWidth(contentW)
		confirmView := m.confirm.View(m.spinFrame)
		for _, cl := range strings.Split(confirmView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(cl, contentW))
		}
	case tabTitle == "Question":
		questionView := m.questionForm.View(m.spinFrame, contentW)
		for _, ql := range strings.Split(questionView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(ql, contentW))
		}
	case tabTitle == "YOLO":
		m.yoloDlg.SetWidth(contentW)
		yoloView := m.yoloDlg.View()
		for _, yl := range strings.Split(yoloView, "\n") {
			rawLines = append(rawLines, rendering.TruncateLine(yl, contentW))
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

// renderLauncherView renders the full-screen session launcher.
func (m *App) renderLauncherView() string {
	pad := strings.Repeat(" ", m.theme.Spacing.PadLR)
	innerW := m.width - m.theme.Spacing.PadLR*2
	if innerW < 20 {
		innerW = 20
	}

	lines := make([]string, 0, m.height)

	// Line 1: blank
	lines = append(lines, "")

	// Line 2: header
	headerLine := widgets.RenderHeader(m.theme, m.modelName, m.yolo, m.startTime, m.width)
	lines = append(lines, pad+headerLine+pad)

	// Line 3: separator
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	// Lines 4..n-5: session list
	idleSeconds := time.Since(m.lastInteractionTime).Seconds()
	visibleSessions := m.launcher.visibleSessions()
	sessionLines := widgets.RenderLauncher(
		visibleSessions,
		m.launcher.selected,
		m.launcher.filterActive,
		m.launcher.filterText,
		m.theme,
		m.width,
		m.height,
		m.spinFrame,
		idleSeconds,
	)

	// Calculate available space for session list
	// Layout: blank + header + sep + [sessions] + sep + hints + sep + statusbar = 7 fixed lines
	fixedLines := 7
	availableH := m.height - fixedLines
	if availableH < 3 {
		availableH = 3
	}

	// Apply scroll offset for long lists
	totalSessionLines := len(sessionLines)
	scrollOffset := m.launcher.scrollOffset

	// Auto-scroll to keep selection visible
	// We need to figure out which line the selected item is on
	selectedLineIdx := m.launcherSelectedLineIndex(sessionLines)
	if selectedLineIdx >= scrollOffset+availableH {
		scrollOffset = selectedLineIdx - availableH + 1
	}
	if selectedLineIdx < scrollOffset {
		scrollOffset = selectedLineIdx
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	m.launcher.scrollOffset = scrollOffset

	// Slice visible portion
	endIdx := scrollOffset + availableH
	if endIdx > totalSessionLines {
		endIdx = totalSessionLines
	}
	visibleLines := sessionLines
	if scrollOffset < totalSessionLines {
		visibleLines = sessionLines[scrollOffset:endIdx]
	} else {
		visibleLines = nil
	}

	for _, line := range visibleLines {
		visW := rendering.VisibleWidth(line)
		rightPad := m.width - 2*m.theme.Spacing.PadLR - visW
		if rightPad < 0 {
			rightPad = 0
		}
		lines = append(lines, pad+line+strings.Repeat(" ", rightPad)+pad)
	}

	// Fill remaining space with empty lines
	targetBeforeSep := 3 + availableH // blank + header + sep + session area
	for len(lines) < targetBeforeSep {
		lines = append(lines, strings.Repeat(" ", m.width))
	}

	// Separator before combined hints bar
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	// Combined preview + hints bar
	previewLines := m.launcherPreviewLines()
	previewText := ""
	if len(previewLines) > 0 {
		previewText = previewLines[0]
	}
	hintsLine := widgets.RenderLauncherPreviewHints(previewText, m.launcher.filterActive, m.launcher.filterText, m.theme, m.width)
	visW := rendering.VisibleWidth(hintsLine)
	rightPad := m.width - 2*m.theme.Spacing.PadLR - visW
	if rightPad < 0 {
		rightPad = 0
	}
	lines = append(lines, pad+hintsLine+strings.Repeat(" ", rightPad)+pad)

	// Separator before statusbar
	lines = append(lines, widgets.RenderSep(m.width, m.theme.Colors.Border))

	// Status bar (same as main view)
	statusView := m.statusbar.View()
	lines = append(lines, pad+statusView)

	if len(lines) > m.height {
		lines = lines[:m.height]
	}

	return strings.Join(lines, "\n")
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


