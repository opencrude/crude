package window

import (
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/widget"
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
	tabs   []Tab
	active int
	width  int
	layout TabLayout

	hint  string
	Hover widget.Hover
}

func New(_ *theme.Theme) *Window {
	return &Window{
		Hover: widget.NewHover(),
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
	closeBtnWidth  = 2
	tabPadLeft     = 1
	tabPadRight    = 1
	tabMinWidth    = 6
	tabMaxWidth    = 30
	tabGap         = 1
	tabBarPadLeft  = 0
	tabBarPadRight = 0
)

type TabHitResult struct {
	TabIndex int
	IsClose  bool
}

func (w *Window) HitTest(x, y int) TabHitResult {
	if !w.IsOpen() {
		return TabHitResult{TabIndex: -1}
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
		titleW := rendering.VisibleWidth(titleForWidth)
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
					rw := rendering.VisibleWidth(disambig)
					if rw > maxLen {
						disambig = rendering.TruncateVisual(disambig, maxLen-1, "…")
					}
					result[idx] = disambig
				}
			}
		}
	}

	for i, s := range result {
		rw := rendering.VisibleWidth(s)
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
