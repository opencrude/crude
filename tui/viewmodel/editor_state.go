package viewmodel

import (
	"crude/tui/editor"
)

type EditorState struct {
	ActiveView       string
	Editor           *editor.Editor
	Tabs             map[string]*EditorTab
	TabSnapshotCache string
}

func NewEditorState() *EditorState {
	return &EditorState{
		Tabs: make(map[string]*EditorTab),
	}
}
