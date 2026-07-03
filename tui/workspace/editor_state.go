package workspace

import "crude/tui/editor"

// EditorState holds the per-editor-tab state for the TUI. The active
// editor is pointed at by ActiveView (the file path). The Tabs map
// keeps all open editors alive across tab switches.
type EditorState struct {
	ActiveView       string
	Editor           *editor.Editor
	Tabs             map[string]*EditorTab
	TabSnapshotCache string
}

// NewEditorState creates an EditorState with an empty tabs map.
func NewEditorState() *EditorState {
	return &EditorState{
		Tabs: make(map[string]*EditorTab),
	}
}
