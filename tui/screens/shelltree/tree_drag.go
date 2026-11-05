package shelltree

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/surface"
	"crude/tui/ui"
)

// File-tree pointer gesture.
//
// A press selects the row. Release without movement activates it exactly as
// a click always did (open the file, toggle the folder). Movement past the
// drag threshold turns the press into a drag: the dragged row dims, the folder
// that would receive the entry is tinted with its visible contents, every
// motion emits tree.drag and release emits tree.drop. The shell paints the
// pointer ghost and pane targets from DragState. The tree never changes files;
// the shell owns every mutation and its confirmation, so pointer, keyboard and
// menu actions share one path.

// Tree action IDs. Payloads are TreeDrop for tree.drop and the selected path
// for the keyboard file actions.
const (
	ActionDrag      = "tree.drag"
	ActionDrop      = "tree.drop"
	ActionRename    = "tree.rename"
	ActionDelete    = "tree.delete"
	ActionCut       = "tree.cut"
	ActionCopy      = "tree.copy"
	ActionPaste     = "tree.paste"
	ActionDuplicate = "tree.duplicate"
	ActionUndo      = "tree.undo"
)

// TreeDrop is a drag in progress (tree.drag, DragState) or completed
// (tree.drop). InTree reports the pointer over the tree; TargetDir is then the
// folder that receives Source, or empty when no folder can. X and Y are the
// pointer in screen cells, for drops on other surfaces such as panes.
type TreeDrop struct {
	Source    string
	IsDir     bool
	TargetDir string
	InTree    bool
	X, Y      int
}

type treePress struct {
	path           string
	isDir          bool
	screenX        int
	screenY        int
	localX, localY int
	dragging       bool
	inTree         bool
	target         string
	pointerX       int
	pointerY       int
}

func (p *treePress) state() TreeDrop {
	target := p.target
	if !ValidMoveTarget(p.path, target) {
		target = ""
	}
	return TreeDrop{Source: p.path, IsDir: p.isDir, TargetDir: target, InTree: p.inTree, X: p.pointerX, Y: p.pointerY}
}

// fileKeyAction maps the explorer's file-action keys.
func fileKeyAction(key tea.KeyPressMsg) string {
	switch key.String() {
	case "f2":
		return ActionRename
	case "delete":
		return ActionDelete
	case "ctrl+x":
		return ActionCut
	case "ctrl+c":
		return ActionCopy
	case "ctrl+v":
		return ActionPaste
	case "ctrl+d":
		return ActionDuplicate
	case "ctrl+z":
		return ActionUndo
	}
	return ""
}

// ValidMoveTarget reports whether moving source into dir changes anything and
// keeps a folder out of itself.
func ValidMoveTarget(source, dir string) bool {
	if source == "" || dir == "" || filepath.Dir(source) == dir || dir == source {
		return false
	}
	rel, err := filepath.Rel(source, dir)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Dragging reports an active tree drag.
func (s *Screen) Dragging() bool { return s != nil && s.press != nil && s.press.dragging }

// DragState is the drag in progress, if any.
func (s *Screen) DragState() (TreeDrop, bool) {
	if !s.Dragging() {
		return TreeDrop{}, false
	}
	return s.press.state(), true
}

// DragIcon is the tree's own icon for the dragged entry.
func (s *Screen) DragIcon() filetree.IconResult {
	if s == nil || s.press == nil || s.icons == nil {
		return filetree.IconResult{}
	}
	return s.icons.ResolveIcon(filepath.Base(s.press.path), s.press.isDir, false, false)
}

// CancelDrag ends a press or drag without any action.
func (s *Screen) CancelDrag() {
	if s.press != nil {
		s.press = nil
		s.clearHover()
	}
}

func (v *fileTreeView) rowNode(localY int) (index int, path string, isDir bool) {
	model := v.owner.displayModel()
	nodes := model.VisibleNodes()
	index = localY + model.ScrollOffset()
	if localY < 0 || index < 0 || index >= len(nodes) {
		return -1, "", false
	}
	return index, nodes[index].Path, nodes[index].IsDir
}

// handlePointer owns press, drag and release. It reports false for events
// that are not part of a press, which keep their ordinary handling.
func (v *fileTreeView) handlePointer(ctx ui.EventContext, evt ui.Event, localY int) (ui.EventResult, bool) {
	s := v.owner
	model := s.displayModel()
	switch evt.Kind {
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			s.press = nil
			return ui.EventResult{}, false
		}
		_, path, isDir := v.rowNode(localY)
		if path == "" {
			s.press = nil
			return ui.EventResult{}, false
		}
		s.press = &treePress{path: path, isDir: isDir, screenX: ctx.ScreenX, screenY: ctx.ScreenY, localX: ctx.LocalX, localY: localY}
		model.Select(path)
		s.syncFromModel()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}, true
	case ui.EventMouseMotion:
		p := s.press
		if p == nil {
			return ui.EventResult{}, false
		}
		dx, dy := ctx.ScreenX-p.screenX, ctx.ScreenY-p.screenY
		if !p.dragging && dx < 2 && dx > -2 && dy == 0 {
			return ui.EventResult{Handled: true}, true
		}
		p.dragging = true
		v.updateDropTarget(p, ctx, localY)
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionDrag, Payload: p.state()}, Invalidate: ui.InvalidatePaint}, true
	case ui.EventMouseRelease:
		p := s.press
		if p == nil || evt.MouseRelease.Button != tea.MouseLeft {
			return ui.EventResult{}, false
		}
		s.press = nil
		if !p.dragging {
			// A click: the model's ordinary activation for the pressed row.
			cmd := model.Update(treeClick(p.localX, p.localY))
			s.syncFromModel()
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}, true
		}
		v.updateDropTarget(p, ctx, localY)
		s.clearHover()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionDrop, Payload: p.state()}, Invalidate: ui.InvalidatePaint}, true
	}
	return ui.EventResult{}, false
}

// updateDropTarget resolves the receiving folder under the pointer: a folder
// row receives into itself, a file row into its folder, empty space below the
// rows into the workspace root.
func (v *fileTreeView) updateDropTarget(p *treePress, ctx ui.EventContext, localY int) {
	s := v.owner
	model := s.displayModel()
	r := v.Bounds()
	p.pointerX, p.pointerY = ctx.ScreenX, ctx.ScreenY
	p.inTree = ctx.LocalX >= 0 && ctx.LocalX < r.W && ctx.LocalY >= 0 && ctx.LocalY < r.H
	p.target = ""
	// paintDrag marks the receiving folder; row hover would compete with it.
	s.clearHover()
	if !p.inTree {
		return
	}
	_, path, isDir := v.rowNode(localY)
	switch {
	case path == "":
		p.target = model.RootPath()
	case isDir:
		p.target = path
	default:
		p.target = filepath.Dir(path)
	}
}

// paintDrag marks the drag in the tree itself: the dragged row is dimmed and
// the receiving folder is tinted together with its visible contents, the way
// a desktop file manager shows where an entry lands.
func (v *fileTreeView) paintDrag(buf *surface.CellBuffer, ox, oy int) {
	p := v.owner.press
	r := v.Bounds()
	if p == nil || !p.dragging || r.Empty() || buf == nil {
		return
	}
	model := v.owner.displayModel()
	nodes := model.VisibleNodes()
	first := model.ScrollOffset()
	top := r.Y
	if v.owner.breadcrumb != "" {
		top++
	}
	rowY := func(index int) int { return oy + top + index - first }
	tokens := v.owner.tokens
	tint := func(index int, apply func(*cell.Cell)) {
		y := rowY(index)
		if index < first || y >= oy+r.Y+r.H {
			return
		}
		for x := ox + r.X; x < ox+r.X+r.W; x++ {
			c := buf.Get(x, y)
			apply(&c)
			buf.Set(x, y, c)
		}
	}
	muted := tokens.Color(ui.ColorMuted)
	for index, node := range nodes {
		if node.Path == p.path {
			tint(index, func(c *cell.Cell) { c.FG = muted })
		}
	}
	if !p.inTree || !ValidMoveTarget(p.path, p.target) {
		return
	}
	hover, accent := tokens.Color(ui.ColorHover), tokens.Color(ui.ColorAccent)
	root := p.target == model.RootPath()
	prefix := p.target + string(filepath.Separator)
	for index, node := range nodes {
		inside := root || node.Path == p.target || strings.HasPrefix(node.Path, prefix)
		if !inside {
			continue
		}
		tint(index, func(c *cell.Cell) { c.BG = hover })
		if node.Path == p.target {
			if y := rowY(index); index >= first && y < oy+r.Y+r.H {
				buf.Set(ox+r.X, y, cell.NewCell('▎', accent, hover, 0))
			}
		}
	}
}

func treeClick(x, y int) filetree.TreeMsg {
	return filetree.TreeMouseMsg{Mouse: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}}
}
