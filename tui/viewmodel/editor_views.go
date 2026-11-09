package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/window"
)

// Documents and views.
//
// EditorState.Tabs holds one entry per open document (path). Its Editor is
// the document's primary view: the editor registered with the live-document
// coordinator, save, recovery and LSP synchronization. EditorState.Views holds
// every tab that shows a document; a file may be open in any number of tabs,
// panes and windows at once. The first view of a document uses the primary
// editor; every further view is an editor.NewLinkedView of it, so all views
// share one buffer and show every edit in the same frame.

// EditorView is one window tab showing a document.
type EditorView struct {
	Tab    window.TabID
	Path   string
	Editor *editor.Editor
}

// editorView returns the view shown by tab id, or nil.
func (m *App) editorView(id window.TabID) *EditorView {
	if m.EditorState == nil {
		return nil
	}
	return m.EditorState.Views[id]
}

// editorForTab resolves the editor behind a window tab, or nil.
func (m *App) editorForTab(id window.TabID) *editor.Editor {
	if v := m.editorView(id); v != nil {
		return v.Editor
	}
	return nil
}

// documentViews returns every view of path in window order.
func (m *App) documentViews(path string) []*EditorView {
	if m.Win == nil || m.EditorState == nil {
		return nil
	}
	var views []*EditorView
	for i, count := 0, m.Win.TabCount(); i < count; i++ {
		tab, _ := m.Win.TabAt(i)
		if v := m.EditorState.Views[tab.ID]; v != nil && v.Path == path {
			views = append(views, v)
		}
	}
	return views
}

// groupEditorView returns group's view of path, or nil.
func (m *App) groupEditorView(group window.GroupID, path string) *EditorView {
	if m.Win == nil {
		return nil
	}
	if tab := m.Win.GroupTabByTitle(group, m.editorTabTitle(path)); tab != nil {
		return m.editorView(tab.ID)
	}
	return nil
}

// editorViewOf returns the view that shows ed, or nil.
func (m *App) editorViewOf(ed *editor.Editor) *EditorView {
	if m.EditorState == nil || ed == nil {
		return nil
	}
	for _, v := range m.EditorState.Views {
		if v.Editor == ed {
			return v
		}
	}
	return nil
}

// showDocument shows an open document in group: it selects group's view of
// the document, or adds one. The first view uses the primary editor; others
// link to the view the user is looking at, so a new view opens at that
// caret and scroll. It returns nil when the document is not open.
func (m *App) showDocument(path string, group window.GroupID, activate bool) *EditorView {
	doc := m.EditorState.Tabs[path]
	if doc == nil || doc.Editor == nil || !doc.Editor.IsOpen() || m.Win == nil || !m.Win.HasGroup(group) {
		return nil
	}
	if v := m.groupEditorView(group, path); v != nil {
		if activate {
			m.Win.SwitchTo(m.Win.IndexOf(v.Tab))
			m.handleTabSwitch()
		}
		return v
	}
	ed := doc.Editor
	if views := m.documentViews(path); len(views) > 0 {
		source := views[0].Editor
		if active := m.EditorState.Editor; active != nil && active.Primary() == doc.Editor {
			source = active
		}
		ed = source.NewLinkedView()
		ed.SetOnContentChanged(func(fp, text string) { m.onEditorContentChanged(ed.Primary(), fp, text) })
		ed.SetOnTyping(func(key tea.KeyPressMsg) { m.onEditorTyping(ed, key) })
	}
	tab := m.Win.AddTab(group, window.Resource{Kind: window.ResourceDocument, ID: path}, m.editorTabTitle(path), activate)
	if tab == nil {
		if ed != doc.Editor {
			ed.Close()
		}
		return nil
	}
	tab.Icon, tab.DisplayTitle = "✎", ed.BaseTitle()
	v := &EditorView{Tab: tab.ID, Path: path, Editor: ed}
	m.EditorState.Views[tab.ID] = v
	if activate {
		m.handleTabSwitch()
	}
	return v
}

// revealEditor brings an open file forward where it already is: the focused
// pane's view, else its first view in any pane. A file that is not open opens
// in the focused pane.
func (m *App) revealEditor(path string) {
	if v := m.groupEditorView(m.Win.ActiveGroup(), path); v == nil {
		if views := m.documentViews(path); len(views) > 0 {
			m.Win.SwitchTo(m.Win.IndexOf(views[0].Tab))
			m.handleTabSwitch()
			return
		}
	}
	if _, err := m.openEditorTab(path); err != nil {
		m.Feedback.NotifyError("Cannot open " + path)
	}
}

// closeLinkedView closes one view of a document that other views still show.
// No prompt is needed: the document, its unsaved text and its history stay
// open. When the closing view holds the primary editor, the primary moves
// into a sibling's tab and takes that sibling's caret and scroll, so the
// coordinator, saves and asynchronous results keep their exact editor.
func (m *App) closeLinkedView(v *EditorView) {
	doc := m.EditorState.Tabs[v.Path]
	closing := v.Editor
	if doc != nil && closing == doc.Editor {
		for _, sibling := range m.documentViews(v.Path) {
			if sibling == v {
				continue
			}
			doc.Editor.AdoptView(sibling.Editor)
			closing, sibling.Editor = sibling.Editor, doc.Editor
			break
		}
	}
	if m.EditorState.Editor == closing || m.EditorState.ActiveTab == v.Tab {
		m.closeEditorViewPopups()
	}
	delete(m.inlayViews, closing)
	delete(m.EditorState.Views, v.Tab)
	if m.Win.IndexOf(v.Tab) >= 0 {
		m.Win.CloseTabByID(v.Tab)
	}
	closing.Close()
	m.handleTabSwitch()
}

// releaseAbsorbedViews closes the views of tabs the window absorbed because a
// move or merge brought them into a pane that already shows their file. The
// document stays open in that pane's view, so nothing is lost or prompted.
func (m *App) releaseAbsorbedViews() {
	if m.Win == nil {
		return
	}
	for _, id := range m.Win.TakeAbsorbedTabs() {
		if v := m.EditorState.Views[id]; v != nil {
			m.closeLinkedView(v)
		}
	}
}

// closeEditorViewPopups closes popups bound to the focused editor view.
func (m *App) closeEditorViewPopups() {
	m.closeFindReplace()
	m.closeNavigation()
	m.closeCompletionPopup()
	m.closeOutlinePopup()
	m.clearDiagHoverPopup()
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayDiagContext)
	}
}
