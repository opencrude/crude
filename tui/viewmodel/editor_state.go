package viewmodel

import "crude/tui/workspace"

type EditorState = workspace.EditorState

func NewEditorState() *EditorState {
	return workspace.NewEditorState()
}
