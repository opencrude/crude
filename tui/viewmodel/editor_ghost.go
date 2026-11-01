package viewmodel

import "crude/tui/editor"

// Only the active private editor can retain candidate key ownership. The editor
// owns the candidate bytes/anchor; this tracks the shell lifetime, not a second
// source or prediction authority.
type editorGhostView struct {
	editor     *editor.Editor
	generation uint64
}

func editorRetainsCompletionKeys(ed *editor.Editor) bool {
	if ed == nil {
		return false
	}
	_, _, candidate := ed.GhostText()
	return ed.SnippetActive() || candidate
}

func (m *App) closeEditorGhost() {
	if state := m.editorGhost; state != nil {
		state.editor.DismissGhostText()
		m.editorGhost = nil
	}
	// A result can be offered between shell events, before adoption on Update.
	if m.EditorState != nil && m.EditorState.Editor != nil {
		m.EditorState.Editor.DismissGhostText()
	}
}

// Reconcile the one active view before and after input, never by scanning tabs,
// exporting source, doing I/O or mutating state during paint.
func (m *App) syncEditorGhost() {
	var ed *editor.Editor
	if m.EditorState != nil {
		ed = m.EditorState.Editor
	}
	owns := ed != nil && ed.IsOpen() && m.focusTarget() == FocusEditor &&
		m.focusedPopup() == nil && !m.termDockOwnsKeys() &&
		!m.sessionDeckCapturesInput() && !m.sessionDeckModal()
	if state := m.editorGhost; state != nil &&
		(state.editor != ed || state.generation != m.workspaceSwitchGen.Load() || !owns) {
		m.closeEditorGhost()
	}
	if ed == nil {
		return
	}
	if !owns {
		ed.DismissGhostText()
		return
	}
	if _, _, present := ed.GhostText(); !present {
		m.editorGhost = nil
	} else if m.editorGhost == nil {
		m.editorGhost = &editorGhostView{editor: ed, generation: m.workspaceSwitchGen.Load()}
	}
}
