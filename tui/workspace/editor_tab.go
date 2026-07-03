package workspace

import "crude/tui/editor"

// EditorTab wraps a single *editor.Editor with the per-tab state that
// persists across tab switches. Cursor position, scroll offset, undo
// history, selection, and the file's loaded buffer are all kept inside
// the *editor.Editor — so they survive switching to another tab and
// back. The wrapper exists so the App can hold a map of open tabs and
// distinguish "this tab has unsaved edits" from the editor's own
// Modified() flag.
type EditorTab struct {
	Path   string
	Editor *editor.Editor

	// ClosingWithDirty is true while we're prompting the user to
	// confirm closing a tab with unsaved edits.
	ClosingWithDirty bool
}

// NewEditorTab creates a tab for the given path. The file is loaded by
// the caller; this is just the wrapper.
func NewEditorTab(path string, ed *editor.Editor) *EditorTab {
	return &EditorTab{
		Path:   path,
		Editor: ed,
	}
}
