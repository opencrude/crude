package viewmodel

import (
	"path/filepath"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/tui/window"
)

// dragGhost describes the drag in progress for the pointer ghost. It is
// derived each frame from the gesture owners (the file tree press, the tab
// strip) and the proposed pane drop, so a finished or cancelled drag never
// leaves a ghost behind.
func (m *App) dragGhost() ui.DragGhost {
	if state, ok := m.sidebarScreen.DragState(); ok {
		return m.treeDragGhost(state)
	}
	p := m.mainPanes
	if p == nil {
		return ui.DragGhost{}
	}
	for _, g := range p.groups {
		item, ok := g.tabs.TabsControl().DraggedItem()
		if !ok {
			continue
		}
		ghost := ui.DragGhost{Label: item.Label, X: p.dragAt[0], Y: p.dragAt[1]}
		ghost.Icon, ghost.IconColor = item.Icon, item.IconColor
		switch d := p.drop; {
		case d == nil:
			ghost.Intent, ghost.Blocked = "Drop on a pane", true
		case d.edge == "":
			ghost.Intent = "Move tab here"
		case d.scope != "":
			ghost.Intent = "Split " + d.edge + " · " + d.scope
		default:
			ghost.Intent = "Split " + d.edge
		}
		return ghost
	}
	return ui.DragGhost{}
}

// tabIcon gives a file tab (text or image) the icon the file tree and Find in
// Files show for that file. Other tabs, and terminals without Nerd Font icons,
// keep a label-only tab: the fallback glyph is the unsaved-changes dot.
func (m *App) tabIcon(tab window.Tab) (string, cell.Color) {
	if tab.Resource.Kind != window.ResourceDocument || tab.Resource.ID == "" || m.Tree == nil || m.Tree.Model == nil {
		return "", cell.NoColor
	}
	resolver := m.Tree.Model.IconResolver()
	if resolver == nil || !resolver.UseNerdFont {
		return "", cell.NoColor
	}
	icon := resolver.ResolveIcon(filepath.Base(tab.Resource.ID), false, false, false)
	return icon.Icon, rendering.ThemeColorToCell(icon.Color)
}

func (m *App) treeDragGhost(state shelltree.TreeDrop) ui.DragGhost {
	icon := m.sidebarScreen.DragIcon()
	ghost := ui.DragGhost{
		Icon: icon.Icon, IconColor: rendering.ThemeColorToCell(icon.Color),
		Label: filepath.Base(state.Source), X: state.X, Y: state.Y,
	}
	switch {
	case state.InTree && state.TargetDir != "":
		folder := filepath.Base(state.TargetDir)
		if state.TargetDir == m.Tree.Model.RootPath() {
			folder = "the workspace root"
		}
		ghost.Intent = "Move to " + folder
	case state.InTree:
		ghost.Intent, ghost.Blocked = "Drop on another folder", true
	case state.IsDir:
		ghost.Intent, ghost.Blocked = "Drop on a folder to move", true
	default:
		d := m.treeFileDrop(state.Source)
		if d == nil {
			ghost.Intent, ghost.Blocked = "Drop on a pane to open", true
			break
		}
		ghost.Intent = fileDropIntents[d.edge]
	}
	return ghost
}

// fileDropIntents names what a file drop does for each pane zone.
var fileDropIntents = map[string]string{
	"": "Open here", "left": "Open to the left", "right": "Open to the right",
	"top": "Open above", "bottom": "Open below",
}

// treeFileDrop is the pane drop proposed for a file dragged from the tree.
func (m *App) treeFileDrop(path string) *paneDrop {
	if p := m.mainPanes; p != nil && p.drop != nil && p.drop.path == path {
		return p.drop
	}
	return nil
}

// trackTreeDrag follows a tree drag over the main area: a file proposes the
// pane it would open in, which paintPaneDropPreview shows.
func (m *App) trackTreeDrag(state shelltree.TreeDrop) {
	p := m.ensureMainPanes()
	p.drop = nil
	if !state.InTree && !state.IsDir {
		p.drop = m.fileDropAt(state.Source, state.X, state.Y)
	}
}

// paintDragGhost draws the ghost on the composited frame, above every
// surface and floating window. It reports whether a ghost was drawn.
func (m *App) paintDragGhost(ghost ui.DragGhost) bool {
	if !ghost.Active() || m.compositor == nil || m.rootHost == nil {
		return false
	}
	ui.PaintDragGhost(m.compositor.Composited(), ghost, m.rootHost.Tokens())
	return true
}
