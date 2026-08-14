package viewmodel

func (m *App) copyEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
}

func (m *App) cutEditorSelection() {
	if m.EditorState.Editor == nil || !m.EditorState.Editor.HasSelection() {
		return
	}
	m.copyToClipboard(m.EditorState.Editor.SelectedText())
	m.EditorState.Editor.DeleteSelection()
}

func (m *App) pasteIntoEditor() {
	if m.EditorState.Editor == nil {
		return
	}
	m.enqueueClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteEditor,
		editor:    m.EditorState.Editor,
	})
}

func (m *App) pasteIntoInput() {
	if m.pasteClipboardImage() {
		return
	}
	m.enqueueClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteInputText,
		session:   m.ActiveSession,
	})
}

func (m *App) pasteClipboardImage() bool {
	if m.ActiveSession == nil || m.ActiveSession.RefStore == nil {
		return false
	}
	m.enqueueClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncPasteInputImageFirst,
		session:   m.ActiveSession,
	})
	return true
}

func (m *App) copyToClipboard(s string) {
	if s == "" {
		return
	}
	m.enqueueClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncWriteText,
		text:      s,
	})
}
