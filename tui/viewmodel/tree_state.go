package viewmodel

import (
	"crude/tui/filetree"
	"crude/tui/workspace"
)

type TreeState = workspace.TreeState

func NewTreeState(model *filetree.FileTreeModel, renderer *filetree.TreeRenderer, defaultWidth int) *TreeState {
	return workspace.NewTreeState(model, renderer, defaultWidth)
}
