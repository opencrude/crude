package window

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"crude/tui/ui"
)

// This reference tree predicts insertion and source pruning without calling any
// Window placement, preview, lookup, or collapse implementation.
type referencePane struct {
	id    uint64
	group GroupID
	dir   ui.Direction
	a, b  *referencePane
}

func referenceTree(n *PaneNode) *referencePane {
	if n == nil {
		return nil
	}
	r := &referencePane{id: n.ID, group: n.Group}
	if n.Split != nil {
		r.dir = n.Split.Dir()
		r.a, r.b = referenceTree(n.First), referenceTree(n.Second)
	}
	return r
}

func (n *referencePane) insert(id uint64, dir ui.Direction, before bool) *referencePane {
	if n.id == id {
		added := &referencePane{}
		r := &referencePane{dir: dir, a: n, b: added}
		if before {
			r.a, r.b = r.b, r.a
		}
		return r
	}
	if n.a != nil {
		n.a, n.b = n.a.insert(id, dir, before), n.b.insert(id, dir, before)
	}
	return n
}

func (n *referencePane) prune(group GroupID) *referencePane {
	if n.a == nil {
		if n.group == group {
			return nil
		}
		return n
	}
	n.a, n.b = n.a.prune(group), n.b.prune(group)
	if n.a == nil {
		return n.b
	}
	if n.b == nil {
		return n.a
	}
	return n
}

func (n *referencePane) shape(newGroup GroupID) string {
	if n.a == nil {
		if n.group == 0 || n.group == newGroup {
			return "NEW"
		}
		return fmt.Sprintf("g%d", n.group)
	}
	return fmt.Sprintf("%d(%s,%s)", n.dir, n.a.shape(newGroup), n.b.shape(newGroup))
}

func topologyNodes(n *PaneNode) []*PaneNode {
	if n == nil {
		return nil
	}
	return append(append([]*PaneNode{n}, topologyNodes(n.First)...), topologyNodes(n.Second)...)
}

func assertWindowInvariants(t *testing.T, w *Window, resources map[TabID]Tab) {
	t.Helper()
	seenNodes, groups := map[uint64]bool{}, map[GroupID]bool{}
	for _, n := range topologyNodes(w.PaneRoot()) {
		if n.ID == 0 || seenNodes[n.ID] {
			t.Fatal("missing/duplicate topology identity")
		}
		seenNodes[n.ID] = true
		if n.Split == nil {
			if n.Group == 0 || groups[n.Group] || n.First != nil || n.Second != nil {
				t.Fatal("invalid leaf")
			}
			groups[n.Group] = true
		} else if n.Group != 0 || n.First == nil || n.Second == nil {
			t.Fatal("invalid branch")
		}
	}
	for _, g := range w.FloatingGroupIDs() {
		if groups[g] {
			t.Fatal("floating group in tree")
		}
		groups[g] = true
	}
	if !groups[w.ActiveGroup()] {
		t.Fatal("focused group does not exist")
	}
	seen := map[TabID]bool{}
	for _, tab := range w.Tabs() {
		if seen[tab.ID] || !groups[tab.Group] {
			t.Fatal("duplicated/orphan resource")
		}
		seen[tab.ID] = true
		original, ok := resources[tab.ID]
		original.Group = tab.Group
		if !ok || original != tab {
			t.Fatal("resource identity or presentation changed")
		}
	}
	if len(seen) != len(resources) {
		t.Fatal("resource lost")
	}
	for group := range groups {
		tabs, active := w.GroupTabs(group), w.GroupActiveTab(group)
		if len(tabs) == 0 && active != nil || len(tabs) > 0 && (active == nil || active.Group != group) {
			t.Fatal("invalid selected resource")
		}
	}
}

func TestPanePlacementRandomMixedSubtrees(t *testing.T) {
	for seed := int64(0); seed < 8; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			w := newTestWindow()
			kinds := []ResourceKind{ResourceDocument, ResourceAgent, ResourcePanel, ResourceGitReview, ResourceLocalDiff}
			resources := map[TabID]Tab{}
			add := func() {
				i := len(resources)
				tab := w.OpenResource(Resource{Kind: kinds[i%len(kinds)], ID: fmt.Sprint(i)}, fmt.Sprint(i), true)
				resources[tab.ID] = *tab
			}
			add()
			add()
			for i := 0; i < 30; i++ {
				groups := w.GroupIDs()
				w.SplitGroup(groups[rng.Intn(len(groups))], ui.Direction(i%2), i%3 == 0)
				add()
				if i%2 == 0 {
					add()
				}
			}
			for step := 0; step < 160; step++ {
				nodes := topologyNodes(w.PaneRoot())
				n := nodes[rng.Intn(len(nodes))]
				tabs := w.Tabs()
				tab := tabs[rng.Intn(len(tabs))]
				if n.Split == nil && n.Group == tab.Group && w.GroupTabCount(tab.Group) == 1 {
					continue
				}
				p := TabPlacement{Tab: tab.ID, Node: n.ID, Split: true, Direction: ui.Direction(rng.Intn(2)), Before: rng.Intn(2) == 0}
				expected := referenceTree(w.PaneRoot()).insert(n.ID, p.Direction, p.Before)
				if w.GroupTabCount(tab.Group) == 1 {
					expected = expected.prune(tab.Group)
				}
				beforeTabs, beforeFocus := w.Tabs(), w.ActiveGroup()
				selections := map[GroupID]TabID{}
				models := map[uint64]*ui.SplitModel{}
				for _, g := range w.AllGroupIDs() {
					if active := w.GroupActiveTab(g); active != nil {
						selections[g] = active.ID
					}
				}
				for _, node := range nodes {
					if node.Split != nil {
						models[node.ID] = node.Split
						node.Split.SetFirstSize(int(node.ID%19) + 24)
					}
				}
				preview, target := w.PreviewTabPlacement(p)
				if target == 0 || referenceTree(preview).shape(target) != expected.shape(0) {
					t.Fatalf("step %d: preview does not match reference", step)
				}
				if !reflect.DeepEqual(beforeTabs, w.Tabs()) || beforeFocus != w.ActiveGroup() {
					t.Fatal("preview changed resource/focus")
				}
				for _, node := range topologyNodes(preview) {
					if model := models[node.ID]; model != nil && node.Split == model {
						t.Fatal("preview shares mutable split")
					}
				}
				actual := w.PlaceTab(p)
				if actual == 0 || referenceTree(w.PaneRoot()).shape(actual) != expected.shape(0) {
					t.Fatalf("step %d: commit does not match independent reference", step)
				}
				for _, node := range topologyNodes(w.PaneRoot()) {
					if model := models[node.ID]; model != nil && (node.Split != model || node.Split.FirstSize() != int(node.ID%19)+24) {
						t.Fatal("surviving split preference/identity lost")
					}
				}
				for g, id := range selections {
					if g != tab.Group && w.HasGroup(g) && w.GroupActiveTab(g).ID != id {
						t.Fatal("unrelated selection changed")
					}
				}
				if w.HasGroup(tab.Group) && w.GroupTabCount(tab.Group) == 0 {
					t.Fatal("sole-tab source not collapsed")
				}
				if w.ActiveGroup() != actual || w.ActiveTab().ID != tab.ID {
					t.Fatal("moved identity not focused")
				}
				assertWindowInvariants(t, w, resources)
			}
		})
	}
}

func TestPanePlacementInvalidIsAtomic(t *testing.T) {
	w := newTestWindow()
	a := w.ActiveGroup()
	x := w.OpenTab("x", "", true).ID
	b := w.SplitGroup(a, ui.Horizontal, false)
	y := w.OpenTab("y", "", true).ID
	stale := w.PaneRoot().ID
	w.MoveTabToGroup(x, b, 0)
	floating := w.DetachTab(y)
	for _, p := range []TabPlacement{
		{Tab: x, Node: stale, Split: true, Direction: ui.Horizontal},
		{Tab: x, Node: w.PaneRoot().ID, Split: true, Direction: ui.Direction(99)},
		{Tab: x, Group: w.GroupOf(x), Split: true, Direction: ui.Vertical},
		{Tab: x, Group: floating, Split: true, Direction: ui.Vertical},
		{Tab: x, Group: w.GroupOf(x), BeforeTab: y},
		{Tab: TabID(999999999), Node: w.PaneRoot().ID, Split: true},
	} {
		before, tree, focus := w.Tabs(), referenceTree(w.PaneRoot()).shape(0), w.ActiveGroup()
		if w.PlaceTab(p) != 0 || !slices.Equal(before, w.Tabs()) || tree != referenceTree(w.PaneRoot()).shape(0) || focus != w.ActiveGroup() {
			t.Fatalf("invalid placement mutated state: %+v", p)
		}
	}
}
