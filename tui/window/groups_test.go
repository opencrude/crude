package window

import (
	"slices"
	"testing"

	"crude/tui/ui"
)

func TestGroupsNestedSplitAndNarrowSizes(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	aNode := w.PaneRoot().ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	c := w.SplitGroup(b, ui.Vertical, true)
	if !slices.Equal(w.GroupIDs(), []GroupID{a, c, b}) || w.ActiveGroup() != c || w.ActiveTab() != nil || w.ActiveIndex() != -1 {
		t.Fatalf("nested split/focus: groups=%v active=%v tab=%v", w.GroupIDs(), w.ActiveGroup(), w.ActiveTab())
	}
	root := w.PaneRoot()
	if root.Split.Dir() != ui.Horizontal || root.Second.Split.Dir() != ui.Vertical || root.First.ID != aNode {
		t.Fatal("split axes or original leaf identity lost")
	}
	root.Split.SetFirstSize(100)
	for _, split := range []*ui.SplitModel{root.Split, root.Second.Split} {
		for size := 0; size < 5; size++ {
			first, divider, second := split.Resolve(size)
			if first < 0 || divider < 0 || second < 0 || first+divider+second != size {
				t.Fatalf("split exceeds narrow size %d: %d/%d/%d", size, first, divider, second)
			}
		}
	}
	if w.SplitGroup(0, ui.Horizontal, false) != 0 || w.FocusGroup(0) || w.RemoveEmptyGroup(0) {
		t.Fatal("invalid group accepted")
	}
}

func TestGroupsOpenInactiveAndGroupLocalCycle(t *testing.T) {
	var w Window
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	y := w.OpenTab("y", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	w.OpenTabInactive("x", "", true)
	if w.ActiveGroup() != b || w.ActiveTab() != nil {
		t.Fatal("inactive existing tab stole empty group focus")
	}
	z := w.OpenTabInactive("z", "", true).ID
	w.OpenTabInactive("background", "", true)
	if w.ActiveGroup() != b || w.ActiveTab().ID != z {
		t.Fatal("inactive new tab stole selection")
	}
	w.OpenTab("x", "", true)
	if w.ActiveGroup() != a || w.ActiveTab().ID != x {
		t.Fatal("existing tab did not activate its own group")
	}
	w.Next()
	if w.ActiveTab().ID != y {
		t.Fatal("next did not stay in group")
	}
	w.Next()
	if w.ActiveTab().ID != x {
		t.Fatal("next did not wrap group")
	}
	w.Prev()
	if w.ActiveTab().ID != y {
		t.Fatal("prev did not wrap group")
	}
	w.FocusGroup(b)
	if w.ActiveTab().ID != z {
		t.Fatal("group selection lost across focus")
	}
	r := Resource{Kind: ResourceDocument, ID: "file"}
	resourceID := w.OpenResource(r, "file", true).ID
	w.FocusGroup(a)
	if w.OpenResource(r, "renamed", true).ID != resourceID || w.ActiveGroup() != b {
		t.Fatal("existing resource identity or group lost")
	}
}

func TestGroupsMoveCloseCollapseAndKeepExplicitEmpty(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	y := w.OpenTab("y", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	z := w.OpenTab("z", "", true).ID
	c := w.SplitGroup(b, ui.Vertical, false)
	w.FocusGroup(a)
	if !w.MoveTabToGroup(y, b, z) || w.ActiveGroup() != b || w.GroupOf(y) != b || w.GroupActiveTab(a).ID != x {
		t.Fatal("move membership/focus/source selection wrong")
	}
	if got := w.GroupTabs(b); got[0].ID != y || got[1].ID != z {
		t.Fatal("move-before order wrong")
	}
	before := w.Tabs()
	if w.MoveTabToGroup(x, b, x) || w.MoveTabToGroup(x, 0, 0) || !slices.Equal(before, w.Tabs()) {
		t.Fatal("invalid moves mutated registry")
	}
	w.CloseTabByID(x)
	if !slices.Equal(w.GroupIDs(), []GroupID{b, c}) || w.ActiveGroup() != b {
		t.Fatal("closing background source did not collapse only source")
	}
	w.FocusGroup(c)
	if !w.RemoveEmptyGroup(c) || w.ActiveGroup() != b || w.PaneRoot().Split != nil {
		t.Fatal("empty group removal/focus wrong")
	}
	w.CloseTabByID(y)
	if w.ActiveTab().ID != z {
		t.Fatal("close did not select surviving group tab")
	}
	w.CloseTabByID(z)
	if w.ActiveTab() != nil || len(w.GroupIDs()) != 1 || w.RemoveEmptyGroup(b) {
		t.Fatal("last empty group must survive")
	}
}

func TestGroupsMoveLastTabAndNestedSiblingSizing(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	c := w.SplitGroup(b, ui.Vertical, false)
	model := w.PaneRoot().Second.Split
	model.SetFirstSize(7)
	if !w.MoveTabToGroup(x, c, 0) || !slices.Equal(w.GroupIDs(), []GroupID{b, c}) || w.PaneRoot().Split != model || model.FirstSize() != 7 {
		t.Fatal("move-last collapse lost sibling split sizing")
	}
	w.CloseTabByID(x)
	if w.ActiveGroup() != b || w.ActiveTab() != nil {
		t.Fatal("close-focused group did not pick visual neighbor")
	}
}

func TestGroupsReorderMergeAndIdentityPreservation(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	y := w.OpenTab("y", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	z := w.OpenTab("z", "", true).ID
	c := w.SplitGroup(b, ui.Vertical, false)
	q := w.OpenTab("q", "", true).ID
	w.OrderTabsSubset([]TabID{q, z, y, x})
	if w.GroupOf(x) != a || w.GroupOf(y) != a || w.GroupOf(z) != b || w.GroupOf(q) != c || w.GroupActiveTab(a).ID != y || w.GroupActiveTab(b).ID != z || w.ActiveTab().ID != q {
		t.Fatal("subset reorder changed membership or per-group selection")
	}
	w.MoveTab(w.IndexOf(q), w.IndexOf(x))
	if w.ActiveTab().ID != q || w.GroupOf(q) != c {
		t.Fatal("legacy move lost identity")
	}
	if !w.MergeGroup(c, b) || w.ActiveGroup() != b || w.ActiveTab().ID != q || w.GroupOf(q) != b {
		t.Fatal("merge lost source focus/selection")
	}
	w.FocusGroup(a)
	if !w.MergeAllGroups() || w.MergeAllGroups() || !slices.Equal(w.GroupIDs(), []GroupID{a}) || w.ActiveTab().ID != y {
		t.Fatal("merge all lost focus/selection")
	}
	ids := w.TabIDs()
	slices.Sort(ids)
	want := []TabID{x, y, z, q}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatal("non-destructive operations allocated/lost tab identities")
	}
	w.CloseAll()
	if w.TabCount() != 0 || w.ActiveIndex() != -1 || w.PaneRoot().Split != nil || len(w.GroupIDs()) != 1 {
		t.Fatal("CloseAll did not reset topology")
	}
}

func TestGroupsMergeFocusedEmptyKeepsTargetSelection(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	if !w.MergeGroup(b, a) || w.ActiveGroup() != a || w.ActiveTab().ID != x {
		t.Fatal("merging empty focused group cleared target selection")
	}
}

func TestGroupsBackgroundPresentationUpdatesPreserveEmptyFocus(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	i := w.IndexOf(x)
	w.SetTabState(i, TabModified)
	w.SetTabDisplayTitleByIndex(i, "changed")
	w.SetTabTitleByIndex(i, "renamed")
	w.SetTabIconByIndex(i, "icon")
	w.TogglePin(i)
	if w.ActiveGroup() != b || w.ActiveIndex() != -1 || w.ActiveTab() != nil || w.GroupActiveTab(a).ID != x {
		t.Fatal("background presentation update changed group focus/selection")
	}
	if w.CloseActiveTab() != CloseNone || w.RemoveEmptyGroup(a) || w.MergeGroup(a, a) {
		t.Fatal("invalid destructive group/tab operation accepted")
	}
	w.Next()
	w.Prev()
	if w.ActiveGroup() != b || w.ActiveTab() != nil {
		t.Fatal("cycling an empty group escaped it")
	}
	if tab, ok := w.TabAt(i); !ok || tab.ID != x || tab.Group != a || tab.State != TabModified || tab.DisplayTitle != "changed" {
		t.Fatal("whole-window registry no longer exposes background tab")
	}
}

func TestFloatingGroupDetachFocusAndDock(t *testing.T) {
	var w Window
	docked := w.ActiveGroup()
	a := w.OpenTab("a", "", true).ID
	b := w.OpenTab("b", "", true).ID
	right := w.SplitGroup(docked, ui.Horizontal, false)
	c := w.OpenTab("c", "", true).ID
	w.FocusGroup(docked)

	float := w.DetachTab(b)
	if float == 0 || !w.IsFloating(float) || w.ActiveGroup() != float || w.ActiveTab().ID != b {
		t.Fatalf("detach: group=%v floating=%v active=%v", float, w.IsFloating(float), w.ActiveGroup())
	}
	if !slices.Equal(w.GroupIDs(), []GroupID{docked, right}) || !slices.Equal(w.FloatingGroupIDs(), []GroupID{float}) {
		t.Fatalf("window group joined the split tree: docked=%v floating=%v", w.GroupIDs(), w.FloatingGroupIDs())
	}
	if w.DockedGroup() != docked || w.GroupActiveTab(docked).ID != a {
		t.Fatalf("docked focus not remembered: %v", w.DockedGroup())
	}
	if w.SplitGroup(float, ui.Horizontal, false) != 0 || w.PlaceTab(TabPlacement{Tab: c, Group: float, Split: true, Direction: ui.Vertical}) != 0 {
		t.Fatal("a window group was split")
	}
	if w.DetachTab(b) != float {
		t.Fatal("detaching a window's only tab created another window")
	}
	// Opening while a window is focused targets that window, like a group.
	d := w.OpenTab("d", "", true).ID
	if w.GroupOf(d) != float {
		t.Fatal("new tab skipped the focused window group")
	}
	// Merging the docked layout never swallows windows.
	w.MergeAllGroups()
	if !slices.Equal(w.GroupIDs(), []GroupID{docked}) || !w.IsFloating(float) || w.GroupOf(c) != docked {
		t.Fatalf("merge all: docked=%v floating=%v", w.GroupIDs(), w.FloatingGroupIDs())
	}
	w.FocusGroup(float)
	if !w.DockGroup(float) || w.IsFloating(float) || w.HasGroup(float) {
		t.Fatal("dock left the window group behind")
	}
	if w.ActiveGroup() != docked || w.GroupOf(b) != docked || w.GroupOf(d) != docked || w.ActiveTab().ID != w.GroupActiveTab(docked).ID {
		t.Fatalf("dock did not keep tab identities: active=%v", w.ActiveGroup())
	}
}

func TestFloatingGroupClosesWithLastTab(t *testing.T) {
	var w Window
	docked := w.ActiveGroup()
	w.OpenTab("a", "", true)
	b := w.OpenTab("b", "", true).ID
	float := w.DetachTab(b)
	if w.CloseTabByID(b) != CloseCancel || w.HasGroup(float) || w.ActiveGroup() != docked {
		t.Fatalf("closing the window's last tab: has=%v active=%v", w.HasGroup(float), w.ActiveGroup())
	}
	// The only docked group may be emptied; it stays as the docked target.
	only := w.Tabs()[0].ID
	float = w.DetachTab(only)
	if !w.IsFloating(float) || len(w.GroupIDs()) != 1 || w.DockedGroup() != docked {
		t.Fatal("detaching the last docked tab lost the docked group")
	}
	w.CloseAll()
	if len(w.FloatingGroupIDs()) != 0 || w.HasGroup(float) {
		t.Fatal("close all kept window groups")
	}
}
