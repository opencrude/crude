package viewmodel

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
)

type clipboardTerminalWrite struct {
	generation uint64
	text       string
}

// FilterTerminalClipboard runs on Bubble Tea's loop, before it emits OSC 52.
// A delayed command cannot overwrite a copy requested after it.
func (m *App) FilterTerminalClipboard(_ tea.Model, msg tea.Msg) tea.Msg {
	write, ok := msg.(clipboardTerminalWrite)
	if !ok {
		return msg
	}
	state := m.asyncIO.Load()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	current := write.generation == state.lastClipboardWrite
	state.mu.Unlock()
	if !current {
		return nil
	}
	return tea.SetClipboard(write.text)()
}

func (m *App) bindEditorClipboard(ed *editor.Editor) {
	ed.SetClipboard(&m.editorClipboard, func(text string) tea.Cmd {
		m.Input.PromptClipboard().Clear()
		return m.writeClipboardCommand(text)
	})
}

func (m *App) composerCopyCommand(text string) tea.Cmd {
	m.editorClipboard.Clear()
	return m.writeClipboardCommand(text)
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
	m.clearClipboardMetadata()
	m.enqueueClipboardAsyncRequest(clipboardAsyncRequestMsg{
		operation: clipboardAsyncWriteText,
		text:      s,
	})
}

// clipboardCopyCommand keeps release-copy in the same ordered worker as other
// shell copies. Admission records owner/generation before any async work starts.
func (m *App) clipboardCopyCommand(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	m.clearClipboardMetadata()
	return m.writeClipboardCommand(text)
}

func (m *App) clearClipboardMetadata() {
	m.editorClipboard.Clear()
	m.Input.PromptClipboard().Clear()
}

func (m *App) writeClipboardCommand(text string) tea.Cmd {
	if text == "" {
		return nil
	}
	request := m.prepareClipboardAsyncRequest(clipboardAsyncRequestMsg{operation: clipboardAsyncWriteText, text: text})
	return m.clipboardAsyncCommand(request)
}
