package viewmodel

import (
	"crude/tui/editor"
	"crude/tui/workspace"
)

// EditorState holds the per-editor-tab display state for the TUI. The active
// editor is pointed at by ActiveView (the file path). The Tabs map keeps all
// open editors alive across tab switches.
//
// This is DISPLAY state — it tracks which editor view is visible right now.
// The actual buffer data (PieceTable) is owned by the BufferManager in
// the workspace package.
type EditorState struct {
	ActiveView       string
	Editor           *editor.Editor
	Tabs             map[string]*workspace.EditorTab
	TabSnapshotCache string
}

// NewEditorState creates an EditorState with an empty tabs map.
func NewEditorState() *EditorState {
	return &EditorState{
		Tabs: make(map[string]*workspace.EditorTab),
	}
}
