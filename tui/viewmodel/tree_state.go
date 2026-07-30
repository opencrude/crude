package viewmodel

import (
	"crude/tui/filetree"
	"crude/workspace"
)

// TreeState is the retained TUI projection for the workspace file-tree data.
type TreeState struct {
	Model            *filetree.FileTreeModel
	Visible          bool
	Width            int
	Resizing         bool
	ResizeStartX     int
	ResizeStartWidth int
	YOffset          int
}

// ViewData converts the retained file-tree control state into neutral row
// data for tests and future frontends.
func (t *TreeState) ViewData() workspace.TreeView {
	view := workspace.TreeView{}
	if t == nil || t.Model == nil {
		return view
	}
	nodes := t.Model.VisibleNodes()
	selected := t.Model.SelectedNode()
	view.Rows = make([]workspace.TreeRowView, 0, len(nodes))
	view.CursorIndex = t.Model.CursorIndex()
	view.ScrollOffset = t.Model.ScrollOffset()
	view.Filter = t.Model.Filter()
	view.FilterActive = t.Model.FilterActive()
	view.Width = t.Width
	view.VisibleHeight = t.Model.VisibleHeight()
	if selected != nil {
		view.SelectedPath = selected.Path
	}
	for index, node := range nodes {
		if node == nil {
			continue
		}
		view.Rows = append(view.Rows, workspace.TreeRowView{
			Path:               node.Path,
			Name:               node.Name,
			Depth:              node.Depth,
			IsDir:              node.IsDir,
			IsSymlink:          node.IsSymlink,
			Expanded:           node.IsExpanded,
			Loading:            node.Loading,
			Hidden:             node.Hidden,
			GitState:           int(node.GitStatus),
			AggregateGitState:  int(node.AggregateStatus),
			DiagnosticCount:    node.DiagnosticCount,
			DiagnosticErrors:   node.DiagErrors,
			DiagnosticWarnings: node.DiagWarnings,
			DiagnosticHints:    node.DiagHints,
			Selected:           node.Path == view.SelectedPath,
			Cursor:             index == view.CursorIndex,
			Hovered:            index == t.Model.Hover.Index,
		})
	}
	return view
}

func NewTreeState(model *filetree.FileTreeModel, defaultWidth int) *TreeState {
	return &TreeState{
		Model:   model,
		Visible: true,
		Width:   defaultWidth,
	}
}
