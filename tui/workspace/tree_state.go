package workspace

import "crude/tui/filetree"

// TreeState holds the file tree model, renderer, and layout state.
// The model and renderer are data (file tree contents); the layout
// fields (Visible, Width, Resizing, YOffset) are display state that
// the viewmodel manipulates.
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
		Model:   model,
		Renderer: renderer,
		Visible: true,
		Width:   defaultWidth,
	}
}
