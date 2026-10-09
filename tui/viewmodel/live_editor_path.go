package viewmodel

import (
	"crude/tui/editor"
	"errors"
)

// retargetLiveEditor projects the core's stable document identity and latest
// path into private tab indexes; no shell watcher or LSP writer is created.
func (m *App) retargetLiveEditor(ed *editor.Editor) error {
	next := ed.DocumentSnapshot().Path
	old := ed.FilePath()
	if next == "" || next == old {
		return nil
	}
	if tab := m.EditorState.Tabs[next]; tab != nil && tab.Editor != ed {
		return errors.New("destination editor already open; shared path retained for review")
	}
	tab := m.EditorState.Tabs[old]
	if tab == nil || tab.Editor != ed {
		return errors.New("shared editor path owner changed")
	}
	if m.BufferMgr != nil {
		if err := m.BufferMgr.Retarget(old, next); err != nil {
			return err
		}
	}
	if err := ed.RetargetDocumentPath(next); err != nil {
		return err
	}
	delete(m.EditorState.Tabs, old)
	tab.Path = next
	m.EditorState.Tabs[next] = tab
	if m.EditorState.ActiveView == old {
		m.EditorState.ActiveView = next
	}
	m.retitleEditorWindowTab(old, next, ed)
	m.markEditorGitStaleNow()
	return nil
}
