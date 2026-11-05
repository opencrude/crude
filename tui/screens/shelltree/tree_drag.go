package shelltree

import (
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

// File-tree pointer gesture.
//
// A press selects the row. Release without movement activates it exactly as
// a click always did (open the file, toggle the folder). Movement past the
// drag threshold turns the press into a drag: the folder that would receive
// the entry is highlighted and named on the bottom row, and release emits
// tree.drop. The tree never changes files; the shell owns every mutation and
// its confirmation, so pointer, keyboard and menu actions share one path.

// Tree action IDs. Payloads are TreeDrop for tree.drop and the selected path
// for the keyboard file actions.
const (
	ActionDrop      = "tree.drop"
	ActionRename    = "tree.rename"
	ActionDelete    = "tree.delete"
	ActionCut       = "tree.cut"
	ActionCopy      = "tree.copy"
	ActionPaste     = "tree.paste"
	ActionDuplicate = "tree.duplicate"
	ActionUndo      = "tree.undo"
)

// TreeDrop is a completed drag. InTree reports a release over the tree;
// TargetDir is then the folder that receives Source. Otherwise X and Y are
// the screen release point, for drops on other surfaces such as panes.
type TreeDrop struct {
	Source    string
	TargetDir string
	InTree    bool
	X, Y      int
}

type treePress struct {
	path           string
	screenX        int
	screenY        int
	localX, localY int
	dragging       bool
	inTree         bool
	target         string
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
func (s *Screen) Dragging() bool { return s.press != nil && s.press.dragging }

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
		_, path, _ := v.rowNode(localY)
		if path == "" {
			s.press = nil
			return ui.EventResult{}, false
		}
		s.press = &treePress{path: path, screenX: ctx.ScreenX, screenY: ctx.ScreenY, localX: ctx.LocalX, localY: localY}
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
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}, true
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
		drop := TreeDrop{Source: p.path, TargetDir: p.target, InTree: p.inTree, X: ctx.ScreenX, Y: ctx.ScreenY}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionDrop, Payload: drop}, Invalidate: ui.InvalidatePaint}, true
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
	p.inTree = ctx.LocalX >= 0 && ctx.LocalX < r.W && ctx.LocalY >= 0 && ctx.LocalY < r.H
	p.target = ""
	if !p.inTree {
		s.clearHover()
		return
	}
	index, path, isDir := v.rowNode(localY)
	switch {
	case path == "":
		p.target = model.RootPath()
		index = -1
	case isDir:
		p.target = path
	default:
		p.target = filepath.Dir(path)
		index = -1
		for i, node := range model.VisibleNodes() {
			if node.Path == p.target {
				index = i
			}
		}
	}
	if !ValidMoveTarget(p.path, p.target) {
		index = -1
	}
	if index >= 0 {
		model.Hover.Set(index)
	} else {
		model.Hover.Clear()
	}
	s.tree.tree.SyncHover(model.Hover.Index)
}

// dragLabel states what release does, on the tree's bottom row.
func (s *Screen) dragLabel() string {
	p := s.press
	if p == nil || !p.dragging {
		return ""
	}
	if !p.inTree {
		if s.displayModel().IsDir(p.path) {
			return "Drop on a folder to move"
		}
		return "Drop on a pane to open"
	}
	if !ValidMoveTarget(p.path, p.target) {
		return "Drop on another folder"
	}
	dir := filepath.Base(p.target)
	if p.target == s.displayModel().RootPath() {
		dir = "the workspace root"
	}
	return "Move to " + dir
}

func (v *fileTreeView) paintDragLabel(buf *surface.CellBuffer, ox, oy int) {
	label := v.owner.dragLabel()
	r := v.Bounds()
	if label == "" || r.Empty() {
		return
	}
	tokens := v.owner.tokens
	bg := tokens.Color(ui.ColorHover)
	text := rendering.TruncateVisual(" "+label, r.W, "…")
	text += strings.Repeat(" ", max(0, r.W-rendering.VisibleWidth(text)))
	buf.WriteString(ox+r.X, oy+r.Y+r.H-1, text, tokens.Color(ui.ColorAccent), bg, 0)
}

func treeClick(x, y int) filetree.TreeMsg {
	return filetree.TreeMouseMsg{Mouse: tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}}
}
