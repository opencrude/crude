package workspace

import "crude/tui/filetree"

// TreeState holds the file tree model, renderer, and layout state.
type TreeState struct {
	Model            *filetree.FileTreeModel
	Renderer         *filetree.TreeRenderer
	Visible          bool
	Width            int
	Resizing         bool
	ResizeStartX     int
	ResizeStartWidth int
	YOffset          int
}

// NewTreeState creates a TreeState with the given model, renderer and default width.
func NewTreeState(model *filetree.FileTreeModel, renderer *filetree.TreeRenderer, defaultWidth int) *TreeState {
	return &TreeState{
		Model:    model,
		Renderer: renderer,
		Visible:  true,
		Width:    defaultWidth,
	}
}
