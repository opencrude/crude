package viewmodel

import "crude/tui/editor"

// EditorState holds the per-editor-tab display state for the TUI. The active
// editor is pointed at by ActiveView (the file path). The Tabs map keeps all
// open editors alive across tab switches.
//
// This is DISPLAY state: it tracks which editor view is visible right now.
// The actual buffer data (PieceTable) is owned by the BufferManager in
// the workspace package.
type EditorState struct {
	ActiveView       string
	Editor           *editor.Editor
	Tabs             map[string]*EditorTab
	TabSnapshotCache string
}

// EditorTab retains one editor projection and its per-tab close state. The
// editor is a TUI document control, so this wrapper belongs to the viewmodel,
// not the workspace domain package.
type EditorTab struct {
	Path   string
	Editor *editor.Editor

	ClosingWithDirty bool
}

func NewEditorTab(path string, ed *editor.Editor) *EditorTab {
	return &EditorTab{Path: path, Editor: ed}
}

// NewEditorState creates an EditorState with an empty tabs map.
func NewEditorState() *EditorState {
	return &EditorState{
		Tabs: make(map[string]*EditorTab),
	}
}
