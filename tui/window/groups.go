package window

import (
	"sync/atomic"

	"crude/tui/ui"
)

// GroupID identifies a leaf independently of its tabs or position.
type GroupID uint64

// PaneNode is a recursive main-view layout. Leaves have Group; branches have
// Split and two children. Consumers may resize Split, but Window owns topology.
type PaneNode struct {
	ID            uint64
	Group         GroupID
	Split         *ui.SplitModel
	First, Second *PaneNode
}

// TabPlacement is the same operation for preview and commit. The preview
// changes a detached layout only; resources and tab identities are never copied.
type TabPlacement struct {
	Tab       TabID
	Group     GroupID
	BeforeTab TabID
	Split     bool
	Direction ui.Direction
	Before    bool
}

func (w *Window) PlaceTab(p TabPlacement) GroupID {
	if w.IndexOf(p.Tab) < 0 || w.groupNode(p.Group) == nil || p.BeforeTab != 0 && w.GroupOf(p.BeforeTab) != p.Group {
		return 0
	}
	target := p.Group
	if p.Split {
		if p.Direction != ui.Horizontal && p.Direction != ui.Vertical {
			return 0
		}
		target = w.SplitGroup(target, p.Direction, p.Before)
		p.BeforeTab = 0
	}
	if !w.MoveTabToGroup(p.Tab, target, p.BeforeTab) {
		return 0
	}
	return target
}

// PreviewTabPlacement resolves source collapse as well as the target split,
// using exactly the same operations as release, on a detached presentation copy.
func (w *Window) PreviewTabPlacement(p TabPlacement) (*PaneNode, GroupID) {
	w.ensureGroups()
	copy := *w
	copy.tabs = append([]Tab(nil), w.tabs...)
	copy.groupActive = make(map[GroupID]TabID, len(w.groupActive))
	for id, tab := range w.groupActive {
		copy.groupActive[id] = tab
	}
	var clone func(*PaneNode) *PaneNode
	clone = func(n *PaneNode) *PaneNode {
		if n == nil {
			return nil
		}
		c := *n
		c.Split = n.Split.Clone()
		c.First = clone(n.First)
		c.Second = clone(n.Second)
		return &c
	}
	copy.paneRoot = clone(w.paneRoot)
	copy.floating = append([]*PaneNode(nil), w.floating...)
	target := copy.PlaceTab(p)
	return copy.paneRoot, target
}

var nextPaneID, nextGroupID uint64

func newLeaf() *PaneNode {
	return &PaneNode{ID: atomic.AddUint64(&nextPaneID, 1), Group: GroupID(atomic.AddUint64(&nextGroupID, 1))}
}

func (w *Window) ensureGroups() {
	if w.paneRoot != nil {
		return
	}
	w.paneRoot = newLeaf()
	w.focusedGroup = w.paneRoot.Group
	w.groupActive = make(map[GroupID]TabID)
}

func (w *Window) PaneRoot() *PaneNode {
	w.ensureGroups()
	return w.paneRoot
}

func (w *Window) ActiveGroup() GroupID {
	w.ensureGroups()
	return w.focusedGroup
}

// GroupIDs returns leaves in visual first-to-second order.
func (w *Window) GroupIDs() []GroupID {
	w.ensureGroups()
	var ids []GroupID
	var visit func(*PaneNode)
	visit = func(n *PaneNode) {
		if n.Split == nil {
			ids = append(ids, n.Group)
			return
		}
		visit(n.First)
		visit(n.Second)
	}
	visit(w.paneRoot)
	return ids
}

func (w *Window) groupNode(group GroupID) *PaneNode {
	w.ensureGroups()
	for _, leaf := range w.floating {
		if leaf.Group == group {
			return leaf
		}
	}
	var find func(*PaneNode) *PaneNode
	find = func(n *PaneNode) *PaneNode {
		if n.Split == nil {
			if n.Group == group {
				return n
			}
			return nil
		}
		if leaf := find(n.First); leaf != nil {
			return leaf
		}
		return find(n.Second)
	}
	return find(w.paneRoot)
}

func (w *Window) GroupActiveTab(group GroupID) *Tab {
	w.ensureGroups()
	index := w.IndexOf(w.groupActive[group])
	if index >= 0 && w.tabs[index].Group == group {
		return &w.tabs[index]
	}
	return nil
}

// GroupTabCount is the number of tabs in group, without allocating: frame
// predicates (tab strip and launcher visibility) ask it every frame.
func (w *Window) GroupTabCount(group GroupID) int {
	w.ensureGroups()
	count := 0
	for _, tab := range w.tabs {
		if tab.Group == group {
			count++
		}
	}
	return count
}

func (w *Window) GroupTabs(group GroupID) []Tab {
	w.ensureGroups()
	var tabs []Tab
	for _, tab := range w.tabs {
		if tab.Group == group {
			tabs = append(tabs, tab)
		}
	}
	return tabs
}

func (w *Window) GroupOf(id TabID) GroupID {
	if index := w.IndexOf(id); index >= 0 {
		return w.tabs[index].Group
	}
	return 0
}

func (w *Window) FocusGroup(group GroupID) bool {
	if w.groupNode(group) == nil {
		return false
	}
	w.focus(group)
	return true
}

// focus is the single writer of focusedGroup. Leaving a docked group for a
// floating one remembers where docked work continues.
func (w *Window) focus(group GroupID) {
	if w.focusedGroup != group && w.focusedGroup != 0 && !w.IsFloating(w.focusedGroup) {
		w.lastDocked = w.focusedGroup
	}
	w.focusedGroup = group
}

// HasGroup reports whether group is a docked or floating leaf.
func (w *Window) HasGroup(group GroupID) bool { return w.groupNode(group) != nil }

// IsFloating reports whether group is shown in its own window.
func (w *Window) IsFloating(group GroupID) bool {
	for _, leaf := range w.floating {
		if leaf.Group == group {
			return true
		}
	}
	return false
}

// FloatingGroupIDs returns window groups in creation order.
func (w *Window) FloatingGroupIDs() []GroupID {
	ids := make([]GroupID, 0, len(w.floating))
	for _, leaf := range w.floating {
		ids = append(ids, leaf.Group)
	}
	return ids
}

// AllGroupIDs returns docked leaves in visual order, then window groups.
func (w *Window) AllGroupIDs() []GroupID {
	return append(w.GroupIDs(), w.FloatingGroupIDs()...)
}

// DockedGroup is where docked work continues: the focused group when it is
// docked, else the docked group focused before a window took focus.
func (w *Window) DockedGroup() GroupID {
	w.ensureGroups()
	if !w.IsFloating(w.focusedGroup) && paneContains(w.paneRoot, w.focusedGroup) {
		return w.focusedGroup
	}
	if paneContains(w.paneRoot, w.lastDocked) {
		return w.lastDocked
	}
	return w.GroupIDs()[0]
}

func paneContains(n *PaneNode, group GroupID) bool {
	if n == nil || group == 0 {
		return false
	}
	if n.Split == nil {
		return n.Group == group
	}
	return paneContains(n.First, group) || paneContains(n.Second, group)
}

// DetachTab moves an existing tab into a new floating group and focuses it.
// The tab keeps its identity; an emptied docked group collapses as usual.
func (w *Window) DetachTab(id TabID) GroupID {
	index := w.IndexOf(id)
	if index < 0 {
		return 0
	}
	if w.IsFloating(w.tabs[index].Group) && len(w.GroupTabs(w.tabs[index].Group)) == 1 {
		return w.tabs[index].Group
	}
	leaf := newLeaf()
	w.floating = append(w.floating, leaf)
	if !w.MoveTabToGroup(id, leaf.Group, 0) {
		w.floating = w.floating[:len(w.floating)-1]
		return 0
	}
	return leaf.Group
}

// DockGroup returns every tab of a floating group to the docked group where
// work continues, keeping identities and the window's selection.
func (w *Window) DockGroup(group GroupID) bool {
	if !w.IsFloating(group) {
		return false
	}
	return w.MergeGroup(group, w.DockedGroup())
}

// SplitGroup inserts an empty focused leaf before or after the original leaf.
func (w *Window) SplitGroup(group GroupID, dir ui.Direction, before bool) GroupID {
	leaf := w.groupNode(group)
	if leaf == nil || w.IsFloating(group) || (dir != ui.Horizontal && dir != ui.Vertical) {
		return 0
	}
	old := *leaf
	added := newLeaf()
	*leaf = PaneNode{ID: atomic.AddUint64(&nextPaneID, 1), Split: ui.NewSplitModel(dir), First: &old, Second: added}
	if before {
		leaf.First, leaf.Second = leaf.Second, leaf.First
	}
	w.focus(added.Group)
	return added.Group
}

// repairGroupSelection prefers the preceding surviving tab, then the next.
func (w *Window) repairGroupSelection(group GroupID, removed TabID, oldIndex int) {
	if w.groupActive[group] != removed {
		return
	}
	selected := TabID(0)
	for i, tab := range w.tabs {
		if tab.Group != group {
			continue
		}
		if i < oldIndex || selected == 0 {
			selected = tab.ID
		}
	}
	w.groupActive[group] = selected
}

// MoveTabToGroup moves the existing identity and selects/focuses it in target.
// beforeTab must belong to target; zero appends after target's last tab.
func (w *Window) MoveTabToGroup(id TabID, target GroupID, beforeTab TabID) bool {
	index := w.IndexOf(id)
	if index < 0 || w.groupNode(target) == nil {
		return false
	}
	if beforeTab != 0 && w.GroupOf(beforeTab) != target {
		return false
	}
	if beforeTab == id {
		w.SwitchTo(index)
		return true
	}
	tab := w.tabs[index]
	source := tab.Group
	w.tabs = append(w.tabs[:index], w.tabs[index+1:]...)
	w.repairGroupSelection(source, id, index)
	insert := len(w.tabs)
	if beforeTab != 0 {
		insert = w.IndexOf(beforeTab)
	} else {
		for i, existing := range w.tabs {
			if existing.Group == target {
				insert = i + 1
			}
		}
	}
	tab.Group = target
	w.tabs = append(w.tabs, Tab{})
	copy(w.tabs[insert+1:], w.tabs[insert:])
	w.tabs[insert] = tab
	w.groupActive[target] = id
	w.focus(target)
	if source != target {
		w.RemoveEmptyGroup(source)
	}
	return true
}

// RemoveEmptyGroup collapses its parent, preserving the sibling subtree and
// its split sizing. Focus chooses the previous visual leaf, or next if first.
func (w *Window) RemoveEmptyGroup(group GroupID) bool {
	if w.IsFloating(group) {
		if len(w.GroupTabs(group)) != 0 {
			return false
		}
		floating := make([]*PaneNode, 0, len(w.floating)-1)
		for _, leaf := range w.floating {
			if leaf.Group != group {
				floating = append(floating, leaf)
			}
		}
		w.floating = floating
		delete(w.groupActive, group)
		if w.focusedGroup == group {
			w.focusedGroup = w.DockedGroup()
		}
		return true
	}
	ids := w.GroupIDs()
	if len(ids) == 1 || w.groupNode(group) == nil || len(w.GroupTabs(group)) != 0 {
		return false
	}
	neighbor := GroupID(0)
	for i, id := range ids {
		if id == group {
			if i > 0 {
				neighbor = ids[i-1]
			} else {
				neighbor = ids[1]
			}
			break
		}
	}
	var remove func(*PaneNode) *PaneNode
	remove = func(n *PaneNode) *PaneNode {
		if n.Split == nil {
			if n.Group == group {
				return nil
			}
			return n
		}
		n.First, n.Second = remove(n.First), remove(n.Second)
		if n.First == nil {
			return n.Second
		}
		if n.Second == nil {
			return n.First
		}
		return n
	}
	w.paneRoot = remove(w.paneRoot)
	delete(w.groupActive, group)
	if w.focusedGroup == group {
		w.focusedGroup = neighbor
	}
	return true
}

// MergeGroup appends source tabs to target without closing or recreating them.
// Target selection survives, unless focus belonged to source (or target empty).
func (w *Window) MergeGroup(source, target GroupID) bool {
	if source == target || w.groupNode(source) == nil || w.groupNode(target) == nil {
		return false
	}
	selected := w.groupActive[target]
	if w.groupActive[source] != 0 && (selected == 0 || w.focusedGroup == source) {
		selected = w.groupActive[source]
	}
	var moved, retained []Tab
	for _, tab := range w.tabs {
		if tab.Group == source {
			tab.Group = target
			moved = append(moved, tab)
		} else {
			retained = append(retained, tab)
		}
	}
	insert := len(retained)
	for i, tab := range retained {
		if tab.Group == target {
			insert = i + 1
		}
	}
	w.tabs = append(retained[:insert], append(moved, retained[insert:]...)...)
	w.groupActive[target] = selected
	if w.focusedGroup == source {
		w.focus(target)
	}
	w.RemoveEmptyGroup(source)
	return true
}

// MergeAllGroups merges the docked layout, preserving the docked focus
// group's identity and selected tab. Windows stay windows.
func (w *Window) MergeAllGroups() bool {
	ids := w.GroupIDs()
	if len(ids) < 2 {
		return false
	}
	target := w.DockedGroup()
	for _, group := range ids {
		if group != target {
			w.MergeGroup(group, target)
		}
	}
	return true
}
