package viewmodel

import "crude/tui/workspace"

type FocusStack = workspace.FocusStack

func NewFocusStack() *FocusStack {
	return workspace.NewFocusStack()
}
