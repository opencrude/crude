package viewmodel

import (
	"crude/tui/editor"
	"crude/tui/window"
)

// EditorState holds the TUI's editor display state. Tabs keeps one entry per
// open document (keyed by path) whose Editor is the document's primary view;
// Views keeps every tab that shows a document (see editor_views.go). The
// focused view is ActiveTab; ActiveView is its path and Editor its editor.
//
// This is DISPLAY state. The actual buffer data (PieceTable) is owned by the
// BufferManager or the core's shared document.
type EditorState struct {
	ActiveView       string
	ActiveTab        window.TabID
	Editor           *editor.Editor
	Tabs             map[string]*EditorTab
	Views            map[window.TabID]*EditorView
	TabSnapshotCache string
}

// EditorTab retains one open document's primary editor and its close state.
// The editor is a TUI document control, so this wrapper belongs to the
// viewmodel, not the workspace domain package.
type EditorTab struct {
	Path   string
	Editor *editor.Editor

	ClosingWithDirty bool
}

func NewEditorTab(path string, ed *editor.Editor) *EditorTab {
	return &EditorTab{Path: path, Editor: ed}
}

// NewEditorState creates an EditorState with empty document and view maps.
func NewEditorState() *EditorState {
	return &EditorState{
		Tabs:  make(map[string]*EditorTab),
		Views: make(map[window.TabID]*EditorView),
	}
}
