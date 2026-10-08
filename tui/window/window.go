package window

import (
	"fmt"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
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

type ResourceKind string

const (
	ResourceDocument  ResourceKind = "document"
	ResourceAgent     ResourceKind = "agent"
	ResourceGitReview ResourceKind = "git-review"
	ResourceLocalDiff ResourceKind = "local-diff"
)

type Resource struct {
	Kind ResourceKind
	ID   string
}

type Tab struct {
	ID           TabID
	Group        GroupID
	Title        string
	DisplayTitle string
	Icon         string
	State        TabState
	Closable     bool
	Pinned       bool
	Layout       TabLayout
	Resource     Resource
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
	tabs         []Tab
	width        int
	paneRoot     *PaneNode
	focusedGroup GroupID
	groupActive  map[GroupID]TabID

	hint  string
	Hover ui.Hover
}

func New(_ *theme.Theme) *Window {
	w := &Window{Hover: ui.NewHover()}
	w.ensureGroups()
	return w
}

func (w *Window) OpenTab(title, icon string, closable bool) *Tab {
	return w.OpenTabWithLayout(title, icon, closable, TabHorizontal)
}

func (w *Window) OpenResource(resource Resource, title string, closable bool) *Tab {
	w.ensureGroups()
	for index := range w.tabs {
		if w.tabs[index].Resource == resource {
			w.SwitchTo(index)
			return &w.tabs[index]
		}
	}
	tab := newTab(title, "", closable, TabHorizontal)
	tab.Resource = resource
	return w.appendTab(tab, true)
}

// OpenTabInactive adds to the focused group without changing group focus or
// an existing selection. The first tab in an empty group becomes its selection.
func (w *Window) OpenTabInactive(title, icon string, closable bool) *Tab {
	w.ensureGroups()
	for i := range w.tabs {
		if w.tabs[i].Title == title {
			return &w.tabs[i]
		}
	}
	return w.appendTab(newTab(title, icon, closable, TabHorizontal), false)
}

func (w *Window) OpenTabWithLayout(title, icon string, closable bool, layout TabLayout) *Tab {
	w.ensureGroups()
	for i := range w.tabs {
		if w.tabs[i].Title == title {
			w.SwitchTo(i)
			return &w.tabs[i]
		}
	}
	return w.appendTab(newTab(title, icon, closable, layout), true)
}

func (w *Window) appendTab(tab Tab, activate bool) *Tab {
	tab.Group = w.focusedGroup
	w.tabs = append(w.tabs, tab)
	if activate || w.groupActive[tab.Group] == 0 {
		w.groupActive[tab.Group] = tab.ID
	}
	return &w.tabs[len(w.tabs)-1]
}

func (w *Window) CloseTab(index int) CloseAction {
	w.ensureGroups()
	if index < 0 || index >= len(w.tabs) {
		return CloseNone
	}
	tab := w.tabs[index]
	if !tab.Closable || (tab.Pinned && len(w.tabs) > 1) {
		return CloseNone
	}
	w.tabs = append(w.tabs[:index], w.tabs[index+1:]...)
	w.repairGroupSelection(tab.Group, tab.ID, index)
	w.RemoveEmptyGroup(tab.Group)
	if len(w.tabs) == 0 {
		return CloseSwitchToChat
	}
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
	return w.CloseTab(w.ActiveIndex())
}

func (w *Window) CloseAll() {
	w.tabs = nil
	w.paneRoot = nil
	w.groupActive = nil
	w.focusedGroup = 0
	w.ensureGroups()
}

func (w *Window) SwitchTo(index int) {
	w.ensureGroups()
	if index >= 0 && index < len(w.tabs) {
		tab := w.tabs[index]
		w.focusedGroup = tab.Group
		w.groupActive[tab.Group] = tab.ID
	}
}

func (w *Window) Next() { w.cycleTab(1) }
func (w *Window) Prev() { w.cycleTab(-1) }

func (w *Window) cycleTab(delta int) {
	tabs := w.GroupTabs(w.ActiveGroup())
	if len(tabs) == 0 {
		return
	}
	selected := w.groupActive[w.focusedGroup]
	for i, tab := range tabs {
		if tab.ID == selected {
			w.groupActive[w.focusedGroup] = tabs[(i+delta+len(tabs))%len(tabs)].ID
			return
		}
	}
	w.groupActive[w.focusedGroup] = tabs[0].ID
}

func (w *Window) ActiveIndex() int {
	w.ensureGroups()
	return w.IndexOf(w.groupActive[w.focusedGroup])
}

func (w *Window) ActiveTab() *Tab {
	return w.GroupActiveTab(w.ActiveGroup())
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
	if tab := w.ActiveTab(); tab != nil {
		return tab.Layout
	}
	return TabHorizontal
}

func (w *Window) SetWidth(width int) {
	w.width = max(0, width)
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
	if tab := w.ActiveTab(); tab != nil {
		if tab.Closable {
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
	if y != 0 || x < 0 || x >= w.width {
		return TabHitResult{TabIndex: -1}
	}

	tabWidths := w.computeTabWidths()
	px := 0

	for i, tw := range tabWidths {
		tabStart := px
		tabEnd := px + tw

		if x >= tabStart && x < tabEnd {
			closeStart := tabEnd - closeBtnWidth - tabPadRight
			closeFits := tw >= closeBtnWidth+tabPadRight
			if closeFits && x >= closeStart && w.tabs[i].Closable {
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

	available := max(0, w.width-tabBarPadLeft-tabBarPadRight)
	if available == 0 {
		return make([]int, n)
	}

	idealWidths := make([]int, n)
	totalIdeal := 0
	active := w.ActiveIndex()
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
		if i == active {
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

	if totalIdeal+(n-1)*tabGap <= available {
		return idealWidths
	}

	// Reserve inter-tab gaps first, then distribute only columns that really
	// exist. tabMinWidth is the preferred readable size, not permission to
	// overflow a narrow terminal.
	tabSpace := max(0, available-(n-1)*tabGap)
	perTab := tabSpace / n
	if perTab > tabMaxWidth {
		perTab = tabMaxWidth
	}

	result := make([]int, n)
	total := 0
	for i := range n {
		result[i] = perTab
		total += perTab
	}
	remaining := tabSpace - total
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
}

// OrderTabsSubset orders the named tabs within their existing slots. Other
// tabs keep their positions, and the selected tab keeps its identity even if
// its index changes. An incomplete or duplicate order leaves the bar intact.
func (w *Window) OrderTabsSubset(ids []TabID) {
	if len(ids) < 2 {
		return
	}
	wanted := make(map[TabID]int, len(ids))
	for i, id := range ids {
		if _, exists := wanted[id]; exists {
			return
		}
		wanted[id] = i
	}
	positions := make([]int, 0, len(ids))
	tabs := make([]Tab, len(ids))
	for i, tab := range w.tabs {
		if position, ok := wanted[tab.ID]; ok {
			positions = append(positions, i)
			tabs[position] = tab
		}
	}
	if len(positions) != len(ids) {
		return
	}
	for i, position := range positions {
		w.tabs[position] = tabs[i]
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

// TabAt returns one tab without copying the whole strip. Per-frame consumers
// that only need to read or refresh a single tab's state use this instead of
// Tabs() so painting stays allocation-quiet.
func (w *Window) TabAt(index int) (Tab, bool) {
	if index < 0 || index >= len(w.tabs) {
		return Tab{}, false
	}
	return w.tabs[index], true
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
