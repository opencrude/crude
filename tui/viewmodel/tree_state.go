package viewmodel

import (
	"crude/tui/filetree"
)

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

func NewTreeState(model *filetree.FileTreeModel, renderer *filetree.TreeRenderer, defaultWidth int) *TreeState {
	return &TreeState{
		Model:    model,
		Renderer: renderer,
		Visible:  true,
		Width:    defaultWidth,
	}
}
