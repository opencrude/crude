package window

import (
	"fmt"
	"image/color"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
)

var nextTabID uint64

func allocTabID() TabID {
	return TabID(atomic.AddUint64(&nextTabID, 1))
}

type TabID uint64

type TabState int

const (
	TabReady TabState = iota
	TabLoading
	TabModified
	TabError
)

type TabLayout int

const (
	TabHorizontal TabLayout = iota
	TabVertical
)

type Tab struct {
	ID           TabID
	Title        string
	DisplayTitle string
	Icon         string
	State        TabState
	Closable     bool
	Pinned       bool
	Layout       TabLayout
}

func newTab(title, icon string, closable bool, layout TabLayout) Tab {
	return Tab{
		ID:       allocTabID(),
		Title:    title,
		Icon:     icon,
		State:    TabReady,
		Closable: closable,
		Layout:   layout,
	}
}

type CloseAction int

const (
	CloseNone CloseAction = iota
	CloseCancel
	CloseSwitchToChat
)

type Window struct {
	theme  *theme.Theme
	tabs   []Tab
	active int
	width  int
	layout TabLayout

	hint string
}

func New(th *theme.Theme) *Window {
	return &Window{
		theme: th,
	}
}

func (w *Window) OpenTab(title, icon string, closable bool) *Tab {
	return w.OpenTabWithLayout(title, icon, closable, TabHorizontal)
}

func (w *Window) OpenTabWithLayout(title, icon string, closable bool, layout TabLayout) *Tab {
	for i := range w.tabs {
		if w.tabs[i].Title == title {
			w.active = i
			return &w.tabs[i]
		}
	}
	t := newTab(title, icon, closable, layout)
	w.tabs = append(w.tabs, t)
	w.active = len(w.tabs) - 1
	w.layout = layout
	return &w.tabs[w.active]
}

func (w *Window) CloseTab(index int) CloseAction {
	if index < 0 || index >= len(w.tabs) {
		return CloseNone
	}
	tab := w.tabs[index]
	if !tab.Closable {
		return CloseNone
	}
	if tab.Pinned && len(w.tabs) > 1 {
		return CloseNone
	}

	w.tabs = append(w.tabs[:index], w.tabs[index+1:]...)

	if len(w.tabs) == 0 {
		w.active = -1
		w.layout = TabHorizontal
		return CloseSwitchToChat
	}

	if index <= w.active {
		w.active--
		if w.active < 0 {
			w.active = 0
		}
	}
	if w.active >= len(w.tabs) {
		w.active = len(w.tabs) - 1
	}

	w.layout = w.tabs[w.active].Layout

	return CloseCancel
}

func (w *Window) CloseTabByID(id TabID) CloseAction {
	for i, t := range w.tabs {
		if t.ID == id {
			return w.CloseTab(i)
		}
	}
	return CloseNone
}

func (w *Window) CloseActiveTab() CloseAction {
	if w.active >= 0 && w.active < len(w.tabs) {
		return w.CloseTab(w.active)
	}
	return CloseNone
}

func (w *Window) CloseAll() {
	w.tabs = nil
	w.active = -1
	w.layout = TabHorizontal
}

func (w *Window) SwitchTo(index int) {
	if index >= 0 && index < len(w.tabs) {
		w.active = index
		w.layout = w.tabs[index].Layout
	}
}

func (w *Window) Next() {
	if len(w.tabs) == 0 {
		return
	}
	w.active = (w.active + 1) % len(w.tabs)
	w.layout = w.tabs[w.active].Layout
}

func (w *Window) Prev() {
	if len(w.tabs) == 0 {
		return
	}
	w.active--
	if w.active < 0 {
		w.active = len(w.tabs) - 1
	}
	w.layout = w.tabs[w.active].Layout
}

func (w *Window) ActiveIndex() int {
	return w.active
}

func (w *Window) ActiveTab() *Tab {
	if w.active >= 0 && w.active < len(w.tabs) {
		return &w.tabs[w.active]
	}
	return nil
}

func (w *Window) TabByTitle(title string) *Tab {
	for i := range w.tabs {
		if w.tabs[i].Title == title {
			return &w.tabs[i]
		}
	}
	return nil
}

func (w *Window) SetTabTitleByIndex(idx int, title string) {
	if idx >= 0 && idx < len(w.tabs) {
		w.tabs[idx].Title = title
	}
}

func (w *Window) SetTabIconByIndex(idx int, icon string) {
	if idx >= 0 && idx < len(w.tabs) {
		w.tabs[idx].Icon = icon
	}
}

func (w *Window) SetTabDisplayTitleByIndex(index int, displayTitle string) {
	if index < 0 || index >= len(w.tabs) {
		return
	}
	w.tabs[index].DisplayTitle = displayTitle
}

func (w *Window) IsOpen() bool {
	return len(w.tabs) > 0
}

func (w *Window) TabCount() int {
	return len(w.tabs)
}

func (w *Window) Layout() TabLayout {
	return w.layout
}

func (w *Window) SetWidth(width int) {
	w.width = width
}

func (w *Window) SetHint(h string) {
	w.hint = h
}

func (w *Window) Hint() string {
	if w.hint != "" {
		if !strings.HasPrefix(w.hint, "[") {
			return "[ " + w.hint + " ]"
		}
		return w.hint
	}
	if w.active >= 0 && w.active < len(w.tabs) {
		if w.tabs[w.active].Closable {
			return "[ ← → switch tabs · click × or esc to close ]"
		}
	}
	return ""
}

func (w *Window) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}

	switch keyMsg.String() {
	case "ctrl+]":
		w.Next()
		return nil
	case "ctrl+[":
		w.Prev()
		return nil
	}

	return nil
}

const (
	closeBtnWidth  = 3
	vCloseBtnWidth = 1
	tabPadLeft     = 1
	tabPadRight    = 1
	tabMinWidth    = 6
	tabMaxWidth    = 30
	tabGap         = 1
	tabBarPadLeft  = 0
	tabBarPadRight = 0

	// vTabBarMinWidth is the floor for the computed vertical tab sidebar width.
	vTabBarMinWidth = 12
	// vTabBarMaxAbsolute caps the sidebar so it never dominates the screen.
	vTabBarMaxAbsolute = 35
	// vTabBarPctOfWidth is the max percentage of terminal width the sidebar may use.
	vTabBarPctOfWidth = 25
	// vMinContentWidth is the minimum columns reserved for the content area.
	vMinContentWidth = 30
)

// VTabBarWidth returns the computed width for the vertical tab sidebar.
// It adapts to terminal width and actual tab title lengths so that tabs
// show as much information as possible while maintaining sane proportions.
// Returns 0 when no sidebar is needed (horizontal layout or ≤1 tab).
func (w *Window) VTabBarWidth() int {
	if w.layout != TabVertical || len(w.tabs) <= 1 {
		return 0
	}

	// Find the widest tab item (title + optional icon).
	maxItemW := 0
	for _, t := range w.tabs {
		titleForWidth := t.DisplayTitle
		if titleForWidth == "" {
			titleForWidth = t.Title
		}
		itemW := rendering.StringWidth(titleForWidth)
		if t.Icon != "" {
			itemW += rendering.StringWidth(t.Icon) + 1 // icon + trailing space
		}
		if itemW > maxItemW {
			maxItemW = itemW
		}
	}

	// Per-line overhead: │(1) + marker/indent(2) + close btn(1) + gap(1) = 5
	const overhead = 5
	idealW := overhead + maxItemW

	// Upper bound: percentage of terminal width, with absolute cap.
	maxW := w.width * vTabBarPctOfWidth / 100
	if maxW > vTabBarMaxAbsolute {
		maxW = vTabBarMaxAbsolute
	}
	// Ensure enough room for content.
	if maxFromContent := w.width - vMinContentWidth; maxFromContent > vTabBarMinWidth && maxFromContent < maxW {
		maxW = maxFromContent
	}
	if maxW < vTabBarMinWidth {
		maxW = vTabBarMinWidth
	}

	// Clamp ideal to [min, max].
	if idealW < vTabBarMinWidth {
		idealW = vTabBarMinWidth
	}
	if idealW > maxW {
		idealW = maxW
	}
	return idealW
}

type TabHitResult struct {
	TabIndex int
	IsClose  bool
}

func (w *Window) HitTest(x, y int) TabHitResult {
	if !w.IsOpen() {
		return TabHitResult{TabIndex: -1}
	}

	if w.layout == TabVertical {
		return w.hitTestVertical(x, y)
	}
	return w.hitTestHorizontal(x, y)
}

func (w *Window) hitTestHorizontal(x, y int) TabHitResult {
	if y != 0 {
		return TabHitResult{TabIndex: -1}
	}

	tabWidths := w.computeTabWidths()
	px := 0

	for i, tw := range tabWidths {
		tabStart := px
		tabEnd := px + tw

		if x >= tabStart && x < tabEnd {
			closeStart := tabEnd - closeBtnWidth - tabPadRight
			if x >= closeStart && w.tabs[i].Closable {
				return TabHitResult{TabIndex: i, IsClose: true}
			}
			return TabHitResult{TabIndex: i, IsClose: false}
		}
		px = tabEnd + tabGap
	}

	return TabHitResult{TabIndex: -1}
}

func (w *Window) hitTestVertical(x, y int) TabHitResult {
	if len(w.tabs) <= 1 {
		return TabHitResult{TabIndex: -1}
	}

	tabBarW := w.VTabBarWidth()
	contentWidth := w.width - tabBarW
	if x < contentWidth {
		return TabHitResult{TabIndex: -1}
	}

	tabX := x - contentWidth
	if tabX < 0 || tabX >= tabBarW {
		return TabHitResult{TabIndex: -1}
	}

	tabIndex := y / 2
	if y%2 == 1 {
		return TabHitResult{TabIndex: tabIndex, IsClose: false}
	}

	if tabIndex >= 0 && tabIndex < len(w.tabs) {
		if tabX == 0 {
			return TabHitResult{TabIndex: tabIndex, IsClose: false}
		}
		closeStart := tabBarW - vCloseBtnWidth
		if tabX >= closeStart && w.tabs[tabIndex].Closable {
			return TabHitResult{TabIndex: tabIndex, IsClose: true}
		}
		return TabHitResult{TabIndex: tabIndex, IsClose: false}
	}

	return TabHitResult{TabIndex: -1}
}

func (w *Window) computeTabWidths() []int {
	n := len(w.tabs)
	if n == 0 {
		return nil
	}

	available := w.width - tabBarPadLeft - tabBarPadRight
	if available < 1 {
		available = 1
	}

	idealWidths := make([]int, n)
	totalIdeal := 0
	for i, t := range w.tabs {
		titleForWidth := t.DisplayTitle
		if titleForWidth == "" {
			titleForWidth = t.Title
		}
		titleW := rendering.StringWidth(titleForWidth)
		closeW := 0
		if t.Closable {
			closeW = closeBtnWidth
		}
		ideal := tabPadLeft + titleW + closeW + tabPadRight
		if i == w.active {
			ideal += 2
		}
		if ideal < tabMinWidth {
			ideal = tabMinWidth
		}
		if ideal > tabMaxWidth {
			ideal = tabMaxWidth
		}
		idealWidths[i] = ideal
		totalIdeal += ideal
	}

	if totalIdeal+n*tabGap-tabGap <= available {
		return idealWidths
	}

	perTab := available / n
	if perTab > tabMaxWidth {
		perTab = tabMaxWidth
	}
	if perTab < tabMinWidth {
		perTab = tabMinWidth
	}

	result := make([]int, n)
	total := 0
	for i := range n {
		result[i] = perTab
		total += perTab
	}
	remaining := available - total - (n-1)*tabGap
	for remaining > 0 {
		for i := range n {
			if remaining <= 0 {
				break
			}
			if result[i] < tabMaxWidth {
				result[i]++
				remaining--
			}
		}
	}

	return result
}

func (w *Window) RenderTabBar() string {
	if !w.IsOpen() {
		return ""
	}
	if w.layout == TabVertical {
		return ""
	}
	return w.renderHorizontalTabBar()
}

func (w *Window) renderHorizontalTabBar() string {
	tabWidths := w.computeTabWidths()
	var b strings.Builder

	b.WriteString(strings.Repeat(" ", tabBarPadLeft))

	for i, t := range w.tabs {
		tw := tabWidths[i]
		isActive := i == w.active

		tabStr := w.renderHorizontalTabItem(t, tw, isActive)

		b.WriteString(tabStr)

		if i < len(w.tabs)-1 {
			b.WriteString(strings.Repeat(" ", tabGap))
		}
	}

	result := b.String()
	resultW := rendering.VisibleWidth(result)
	padEnd := w.width - resultW - tabBarPadLeft - tabBarPadRight
	if padEnd > 0 {
		b.WriteString(strings.Repeat(" ", padEnd))
	}

	return b.String()
}

func (w *Window) renderHorizontalTabItem(t Tab, tw int, isActive bool) string {
	var b strings.Builder

	titleText := t.DisplayTitle
	if titleText == "" {
		titleText = t.Title
	}
	availForTitle := tw - tabPadLeft - tabPadRight
	if isActive {
		availForTitle -= 2
	}
	if t.Closable {
		availForTitle -= closeBtnWidth
	}
	if availForTitle < 1 {
		availForTitle = 1
	}

	titleRW := rendering.StringWidth(titleText)
	if titleRW > availForTitle {
		titleText = rendering.TruncateVisual(titleText, availForTitle-1, "…")
	}

	if isActive {
		markerStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Accent).Bold(true)
		b.WriteString(markerStyle.Render("❯ "))
		titleStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Fg).Bold(true)
		b.WriteString(titleStyle.Render(titleText))
	} else {
		titleStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
		b.WriteString(titleStyle.Render(titleText))
	}

	if t.Closable {
		closeStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
		b.WriteString(closeStyle.Render(" ×"))
	}

	rendered := b.String()
	renderedW := rendering.VisibleWidth(rendered)
	padTotal := tw - renderedW
	if padTotal < 0 {
		padTotal = 0
	}
	padL := padTotal / 2
	padR := padTotal - padL

	return strings.Repeat(" ", padL) + rendered + strings.Repeat(" ", padR)
}

func (w *Window) RenderSeparator() string {
	if !w.IsOpen() {
		return ""
	}
	return lipgloss.NewStyle().Foreground(w.theme.Colors.Border).
		Render(strings.Repeat("─", w.width))
}

func (w *Window) RenderVerticalSidebar(contentLines []string) []string {
	if !w.IsOpen() || w.layout != TabVertical || len(w.tabs) <= 1 {
		return nil
	}

	tabBarW := w.VTabBarWidth()
	contentW := w.width - tabBarW
	if contentW < 10 {
		contentW = 10
	}

	contentCount := len(contentLines)
	tabCount := len(w.tabs)
	tabLinesCount := tabCount
	if tabCount > 1 {
		tabLinesCount += tabCount - 1
	}
	totalLines := contentCount
	if tabLinesCount > totalLines {
		totalLines = tabLinesCount
	}

	result := make([]string, totalLines)

	sepStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Border)
	sep := sepStyle.Render("│")

	tabLineIdx := 0
	for i := 0; i < totalLines; i++ {
		var contentLine string
		if i < len(contentLines) {
			contentLine = contentLines[i]
		}

		contentVisW := rendering.VisibleWidth(contentLine)
		contentPad := contentW - contentVisW
		if contentPad < 0 {
			contentPad = 0
		}

		paddedContent := contentLine + strings.Repeat(" ", contentPad)

		var tabLine string
		if tabLineIdx < tabCount {
			tabLine = w.renderVerticalTabItem(w.tabs[tabLineIdx], tabLineIdx == w.active, tabBarW)
			tabLineIdx++
			if tabLineIdx < tabCount && i+1 < totalLines {
				result[i] = paddedContent + tabLine
				i++

				var nextContent string
				if i < len(contentLines) {
					nextContent = contentLines[i]
				}
				nextVisW := rendering.VisibleWidth(nextContent)
				nextPad := contentW - nextVisW
				if nextPad < 0 {
					nextPad = 0
				}

				tabSep := w.renderVerticalTabSeparator(tabBarW)
				result[i] = nextContent + strings.Repeat(" ", nextPad) + tabSep
				continue
			}
		} else {
			tabLine = sep + strings.Repeat(" ", tabBarW-1)
		}

		result[i] = paddedContent + tabLine
	}

	return result
}

func (w *Window) renderVerticalTabItem(t Tab, isActive bool, width int) string {
	sepStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Border)
	sep := sepStyle.Render("│")

	var b strings.Builder

	b.WriteString(sep)

	if isActive {
		markerStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Accent).Bold(true)
		b.WriteString(markerStyle.Render("❯ "))
		usedW := 3 // │(1) + ❯(1) + space(1)
		closeW := 0
		if t.Closable {
			closeW = vCloseBtnWidth
		}
		iconW := 0
		iconStr := ""
		if t.Icon != "" {
			iconStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Success)
			iconStr = iconStyle.Render(t.Icon) + " "
			iconW = rendering.StringWidth(t.Icon) + 1
		}
		availForTitle := width - usedW - closeW - iconW
		if availForTitle < 1 {
			availForTitle = 1
		}
		titleText := t.DisplayTitle
		if titleText == "" {
			titleText = t.Title
		}
		titleRW := rendering.StringWidth(titleText)
		if titleRW > availForTitle {
			titleText = rendering.TruncateVisual(titleText, availForTitle-1, "…")
		}
		titleStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Fg).Bold(true)
		b.WriteString(iconStr + titleStyle.Render(titleText))
		rendered := b.String()
		renderedW := rendering.VisibleWidth(rendered)
		flexSpace := width - renderedW - closeW
		if flexSpace < 0 {
			flexSpace = 0
		}
		b.WriteString(strings.Repeat(" ", flexSpace))
		if t.Closable {
			closeStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
			b.WriteString(closeStyle.Render("×"))
		}
	} else {
		b.WriteString("  ")
		usedW := 3
		closeW := 0
		if t.Closable {
			closeW = vCloseBtnWidth
		}
		iconW := 0
		iconStr := ""
		if t.Icon != "" {
			iconStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
			iconStr = iconStyle.Render(t.Icon) + " "
			iconW = rendering.StringWidth(t.Icon) + 1
		}
		availForTitle := width - usedW - closeW - iconW
		if availForTitle < 1 {
			availForTitle = 1
		}
		titleText := t.DisplayTitle
		if titleText == "" {
			titleText = t.Title
		}
		titleRW := rendering.StringWidth(titleText)
		if titleRW > availForTitle {
			titleText = rendering.TruncateVisual(titleText, availForTitle-1, "…")
		}
		titleStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
		b.WriteString(iconStr + titleStyle.Render(titleText))
		rendered := b.String()
		renderedW := rendering.VisibleWidth(rendered)
		flexSpace := width - renderedW - closeW
		if flexSpace < 0 {
			flexSpace = 0
		}
		b.WriteString(strings.Repeat(" ", flexSpace))
		if t.Closable {
			closeStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
			b.WriteString(closeStyle.Render("×"))
		}
	}

	return b.String()
}

func (w *Window) renderVerticalTabSeparator(width int) string {
	sepStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)
	sep := sepStyle.Render("│")
	dashes := strings.Repeat("─", width-1)
	return sep + sepStyle.Render(dashes)
}

type AsyncItem struct {
	Handle   string
	Command  string
	Duration string
	Status   string
}

func (w *Window) RenderVerticalAsyncRow(item AsyncItem, spinFrame int) string {
	sepStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Border)
	sep := sepStyle.Render("│")

	var dot string
	var dotColor color.Color
	switch item.Status {
	case "running":
		if components.BlinkOn(spinFrame) {
			dot = "●"
		} else {
			dot = "○"
		}
		dotColor = w.theme.Colors.Accent2
	case "completed":
		dot = "✓"
		dotColor = w.theme.Colors.Success
	case "failed":
		dot = "✗"
		dotColor = w.theme.Colors.Error
	case "killed":
		dot = "!"
		dotColor = w.theme.Colors.Warning
	default:
		dot = "?"
		dotColor = w.theme.Colors.Muted
	}

	cmd := item.Command
	if rendering.VisibleWidth(cmd) > 20 {
		cmd = rendering.TruncateVisual(cmd, 17, "…")
	}

	dotStyle := lipgloss.NewStyle().Foreground(dotColor)
	handleStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Accent2)
	dimStyle := lipgloss.NewStyle().Foreground(w.theme.Colors.Muted)

	result := sep + " " +
		dotStyle.Render(dot+" ") +
		handleStyle.Render("["+item.Handle+"]") + " " +
		dimStyle.Render(cmd) + "  " +
		dimStyle.Render(item.Duration)

	return result
}

func (w *Window) VerticalContentWidth() int {
	if w.layout != TabVertical || len(w.tabs) <= 1 {
		return w.width
	}
	contentW := w.width - w.VTabBarWidth()
	if contentW < 10 {
		contentW = 10
	}
	return contentW
}

func (w *Window) SetTabState(index int, state TabState) {
	if index >= 0 && index < len(w.tabs) {
		w.tabs[index].State = state
	}
}

func (w *Window) TogglePin(index int) {
	if index >= 0 && index < len(w.tabs) {
		w.tabs[index].Pinned = !w.tabs[index].Pinned
	}
}

func (w *Window) MoveTab(from, to int) {
	if from < 0 || from >= len(w.tabs) || to < 0 || to >= len(w.tabs) || from == to {
		return
	}

	t := w.tabs[from]
	w.tabs = append(w.tabs[:from], w.tabs[from+1:]...)
	w.tabs = append(w.tabs[:to], append([]Tab{t}, w.tabs[to:]...)...)

	if w.active == from {
		w.active = to
	} else if from < w.active && to >= w.active {
		w.active--
	} else if from > w.active && to <= w.active {
		w.active++
	}
}

func (w *Window) DisplayTitles(maxLen int) []string {
	titles := make([]string, len(w.tabs))
	for i, t := range w.tabs {
		titles[i] = t.DisplayTitle
		if titles[i] == "" {
			titles[i] = t.Title
		}
	}

	counts := make(map[string][]int)
	for i, t := range w.tabs {
		displayTitle := t.DisplayTitle
		if displayTitle == "" {
			displayTitle = t.Title
		}
		counts[displayTitle] = append(counts[displayTitle], i)
	}

	result := make([]string, len(w.tabs))
	copy(result, titles)

	for _, indices := range counts {
		if len(indices) > 1 {
			for _, idx := range indices {
				if w.tabs[idx].Icon != "" {
					disambig := fmt.Sprintf("%s — %s", titles[idx], w.tabs[idx].Icon)
					rw := rendering.StringWidth(disambig)
					if rw > maxLen {
						disambig = rendering.TruncateVisual(disambig, maxLen-1, "…")
					}
					result[idx] = disambig
				}
			}
		}
	}

	for i, s := range result {
		rw := rendering.StringWidth(s)
		if rw > maxLen {
			result[i] = rendering.TruncateVisual(s, maxLen-1, "…")
		}
	}

	return result
}

func (w *Window) TabIDs() []TabID {
	ids := make([]TabID, len(w.tabs))
	for i, t := range w.tabs {
		ids[i] = t.ID
	}
	return ids
}

func (w *Window) Tabs() []Tab {
	result := make([]Tab, len(w.tabs))
	copy(result, w.tabs)
	return result
}

func (w *Window) HasClosableTabs() bool {
	for _, t := range w.tabs {
		if t.Closable {
			return true
		}
	}
	return false
}

func (w *Window) IndexOf(id TabID) int {
	for i, t := range w.tabs {
		if t.ID == id {
			return i
		}
	}
	return -1
}
