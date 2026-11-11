package viewmodel

import (
	"crude/syntax"
	"crude/tui/editor"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/tui/ui"
	"crude/tui/window"
)

// Saved pane work is deliberately bounded: a corrupt or hand-edited profile
// must not make the shell build an unbounded layout or open an unbounded list
// of documents.
const (
	maxRememberedPanes     = 16
	maxRememberedPaneFiles = 64
	maxRememberedPaneDepth = 12
	maxRememberedPanePath  = 4096
)

// captureViewPanes records the docked pane tree and each pane's open documents.
// Agent, terminal and panel tabs stay with their own owners; this memory keeps
// the editor work a client expects to see again.
func (m *App) captureViewPanes() ([]viewMemoryPane, *viewMemoryPaneNode, int, bool) {
	if m.Win == nil {
		return nil, nil, 0, false
	}
	groups := m.Win.GroupIDs()
	if len(groups) == 0 || len(groups) > maxRememberedPanes {
		return nil, nil, 0, false
	}
	index := make(map[window.GroupID]int, len(groups))
	for i, group := range groups {
		index[group] = i
	}
	panes := make([]viewMemoryPane, len(groups))
	hasFiles := false
	for i, group := range groups {
		for _, tab := range m.Win.GroupTabs(group) {
			if tab.Resource.Kind != window.ResourceDocument || tab.Resource.ID == "" {
				continue
			}
			panes[i].Files = append(panes[i].Files, tab.Resource.ID)
		}
		if len(panes[i].Files) > maxRememberedPaneFiles {
			panes[i].Files = panes[i].Files[:maxRememberedPaneFiles]
		}
		if active := m.Win.GroupActiveTab(group); active != nil && active.Resource.Kind == window.ResourceDocument {
			panes[i].Active = active.Resource.ID
		}
		hasFiles = hasFiles || len(panes[i].Files) > 0
	}
	if !hasFiles {
		return nil, nil, 0, false
	}
	layout := capturePaneNode(m.Win.PaneRoot(), index, 0)
	active := index[m.Win.DockedGroup()]
	return panes, layout, active, layout != nil
}

func capturePaneNode(node *window.PaneNode, index map[window.GroupID]int, depth int) *viewMemoryPaneNode {
	if node == nil || depth > maxRememberedPaneDepth {
		return nil
	}
	if node.Split == nil {
		leaf, ok := index[node.Group]
		if !ok {
			return nil
		}
		return &viewMemoryPaneNode{Leaf: leaf}
	}
	direction := "row"
	if node.Split.Dir() == ui.Vertical {
		direction = "column"
	}
	first := capturePaneNode(node.First, index, depth+1)
	second := capturePaneNode(node.Second, index, depth+1)
	if first == nil || second == nil {
		return nil
	}
	return &viewMemoryPaneNode{
		Split: true, Direction: direction, Fraction: node.Split.Fraction(),
		First: first, Second: second,
	}
}

// validViewPanes accepts only a complete tree that names every remembered pane
// exactly once. A partial record would restore a layout with a lost pane.
func validViewPanes(memory viewMemory) bool {
	if len(memory.Panes) == 0 {
		return memory.PaneLayout == nil
	}
	if len(memory.Panes) > maxRememberedPanes || memory.PaneLayout == nil {
		return false
	}
	seen := make([]bool, len(memory.Panes))
	totalFiles := 0
	for _, pane := range memory.Panes {
		if len(pane.Files) > maxRememberedPaneFiles {
			return false
		}
		if len(pane.Folds) > len(pane.Files) {
			return false
		}
		for path, folds := range pane.Folds {
			if !slices.Contains(pane.Files, path) || folds == nil || len(folds.Ranges) > syntax.MaxStructure {
				return false
			}
		}
		totalFiles += len(pane.Files)
		if totalFiles > maxRememberedPanes*maxRememberedPaneFiles {
			return false
		}
		for _, path := range append(append([]string(nil), pane.Files...), pane.Active) {
			if len(path) > maxRememberedPanePath {
				return false
			}
		}
	}
	var visit func(*viewMemoryPaneNode, int) bool
	visit = func(node *viewMemoryPaneNode, depth int) bool {
		if node == nil || depth > maxRememberedPaneDepth {
			return false
		}
		if !node.Split {
			if node.Leaf < 0 || node.Leaf >= len(memory.Panes) || seen[node.Leaf] {
				return false
			}
			seen[node.Leaf] = true
			return true
		}
		if node.Direction != "row" && node.Direction != "column" {
			return false
		}
		if node.Fraction < 0 || node.Fraction > 1 {
			return false
		}
		return visit(node.First, depth+1) && visit(node.Second, depth+1)
	}
	if !visit(memory.PaneLayout, 0) {
		return false
	}
	for _, ok := range seen {
		if !ok {
			return false
		}
	}
	return true
}

// restoreViewPanes rebuilds the saved split tree and queues each pane's files.
// It runs before the first frame and on a workspace switch, when the window is
// empty. Existing work always wins.
func (m *App) restoreViewPanes(memory viewMemory) {
	if m.Win == nil || m.Win.TabCount() > 0 || len(memory.Panes) == 0 || memory.PaneLayout == nil {
		return
	}
	groups := m.Win.GroupIDs()
	if len(groups) == 0 {
		return
	}
	panes := make([]viewMemoryPane, len(memory.Panes))
	for i, pane := range memory.Panes {
		for _, path := range pane.Files {
			if clean, ok := m.rememberedEditorPath(path); ok {
				panes[i].Files = append(panes[i].Files, clean)
				if fold := pane.Folds[path]; fold != nil {
					if panes[i].Folds == nil {
						panes[i].Folds = make(map[string]*editor.FoldMemory)
					}
					panes[i].Folds[clean] = fold
				}
			}
		}
		if clean, ok := m.rememberedEditorPath(pane.Active); ok {
			panes[i].Active = clean
		}
	}
	restored := make([]window.GroupID, len(panes))
	if !m.buildRememberedPaneTree(memory.PaneLayout, groups[0], restored) {
		return
	}
	active := groups[0]
	if memory.ActivePane >= 0 && memory.ActivePane < len(restored) && restored[memory.ActivePane] != 0 {
		active = restored[memory.ActivePane]
	}
	m.Win.FocusGroup(active)
	queued := false
	for i, pane := range panes {
		if len(pane.Files) == 0 || restored[i] == 0 {
			continue
		}
		cmd := m.openPaneFiles(pane.Files, restored[i])
		if cmd == nil {
			continue
		}
		activePath := pane.Active
		restoreForeground := restored[i] == active
		queued = true
		m.editorCommands = append(m.editorCommands, func() tea.Msg {
			msg := cmd().(paneFilesOpenedMsg)
			msg.activePath = activePath
			msg.restoring = true
			msg.folds = pane.Folds
			msg.restoreForeground = restoreForeground
			return msg
		})
	}
	if queued {
		m.paneRestoreCeiling = m.paneFileOpenSeq
	}
}

// buildRememberedPaneTree recreates the exact split tree. SplitPane keeps the
// existing leaf first, so the saved first child stays in group and the saved
// second child takes the added group.
func (m *App) buildRememberedPaneTree(node *viewMemoryPaneNode, group window.GroupID, groups []window.GroupID) bool {
	if node == nil {
		return false
	}
	if !node.Split {
		if node.Leaf < 0 || node.Leaf >= len(groups) {
			return false
		}
		groups[node.Leaf] = group
		return true
	}
	direction := ui.Horizontal
	if node.Direction == "column" {
		direction = ui.Vertical
	}
	added := m.Win.SplitGroup(group, direction, false)
	if added == 0 {
		return false
	}
	// SplitGroup focuses the added leaf; the saved fraction belongs to the
	// branch that now contains both children.
	if scopes := m.Win.PaneScopes(added); len(scopes) > 1 && scopes[1].Split != nil && node.Fraction > 0 {
		scopes[1].Split.SetFraction(node.Fraction)
	}
	return m.buildRememberedPaneTree(node.First, group, groups) &&
		m.buildRememberedPaneTree(node.Second, added, groups)
}

// rememberedEditorPath keeps restoration inside this workspace. A stale path
// from another project or a corrupted profile is ignored, never opened.
func (m *App) rememberedEditorPath(path string) (string, bool) {
	if document.IsUntitledID(path) && m.ws != nil {
		return path, true
	}
	if path == "" || len(path) > maxRememberedPanePath || !filepath.IsAbs(path) || m.ws == nil {
		return "", false
	}
	root := m.ws.RootPath()
	if root == "" {
		return "", false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return filepath.Clean(path), true
}

// paneMemoryMark changes when the remembered editor panes change, so the one
// save trigger notices tab and split changes without mutation-site bookkeeping.
func (m *App) paneMemoryMark() uint64 {
	panes, layout, active, ok := m.captureViewPanes()
	if !ok {
		return 0
	}
	h := fnv.New64a()
	groups := m.Win.GroupIDs()
	for i, pane := range panes {
		fmt.Fprintf(h, "%d:", i)
		for _, path := range pane.Files {
			fmt.Fprintf(h, "%s|", path)
			if i < len(groups) {
				if view := m.groupEditorView(groups[i], path); view != nil {
					fmt.Fprintf(h, "F%d", view.Editor.FoldGeneration())
				}
			}

		}
		fmt.Fprintf(h, ">%s;", pane.Active)
	}
	var visit func(*viewMemoryPaneNode)
	visit = func(node *viewMemoryPaneNode) {
		if node == nil {
			return
		}
		if !node.Split {
			fmt.Fprintf(h, "L%d", node.Leaf)
			return
		}
		fmt.Fprintf(h, "S%s%.2f", node.Direction, node.Fraction)
		visit(node.First)
		visit(node.Second)
	}
	visit(layout)
	fmt.Fprintf(h, "A%d", active)
	return h.Sum64()
}

func (m *App) captureEditorFolds(panes []viewMemoryPane) {
	if m.Win == nil {
		return
	}
	groups := m.Win.GroupIDs()
	for i := range panes {
		if i >= len(groups) {
			break
		}
		for _, path := range panes[i].Files {
			if view := m.groupEditorView(groups[i], path); view != nil {
				if fold := view.Editor.ExportFolds(); fold != nil {
					if panes[i].Folds == nil {
						panes[i].Folds = make(map[string]*editor.FoldMemory)
					}
					panes[i].Folds[path] = fold
				}
			}
		}
	}
}
