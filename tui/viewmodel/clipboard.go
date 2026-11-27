package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	"errors"
	"time"

	"crude/tui/editor"
)

type clipboardTerminalWrite struct {
	generation uint64
	text       string
}

// FilterTerminalClipboard runs on Bubble Tea's loop, before it emits OSC 52.
// A delayed command cannot overwrite a copy requested after it.
func (m *App) FilterTerminalClipboard(_ tea.Model, msg tea.Msg) tea.Msg {
	if read, ok := msg.(*terminalPasteQuery); ok {
		state := m.asyncIO.Load()
		if state == nil {
			return nil
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.clipboardClosed || state.terminalQuery != read || read.sent {
			return nil
		}
		if read.workspace != m.workspaceSwitchGen.Load() {
			state.terminalQuery = nil
			close(read.answer)
			return nil
		}
		read.sent = true
		return tea.ReadClipboard()
	}
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

// bindClipboardOwners routes the composer, the editor and every component of
// the root host through the shell's ordered clipboard worker, never a separate
// writer. The composer and the editor keep their own paste metadata; a copy
// from any other surface clears both.
func (m *App) bindClipboardOwners() {
	m.Input.SetClipboardCopy(m.composerCopyCommand)
	if m.EditorState.Editor != nil {
		m.bindEditorClipboard(m.EditorState.Editor)
	}
	m.rootHost.SetClipboardCopy(m.clipboardCopyCommand)
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

// OSC 52 responses carry a selection, but no request ID. Keep one outstanding
// query in the ordered worker. A timed-out query that reached the terminal
// must drain its response before a later paste can claim a different one.
type terminalPasteQuery struct {
	workspace uint64
	answer    chan string
	sent      bool // guarded by the clipboard worker's mutex
}

func (s *appAsyncIOState) readTerminalPaste(job clipboardAsyncJob, r *clipboardAsyncResultMsg) {
	r.operationErr = errors.New("cannot read the clipboard. Use the terminal's Paste command, or allow its clipboard prompt")
	s.mu.Lock()
	if s.clipboardClosed || s.terminalQuery != nil {
		s.mu.Unlock()
		return
	}
	q := &terminalPasteQuery{workspace: job.request.workspaceGeneration, answer: make(chan string, 1)}
	s.terminalQuery = q
	s.mu.Unlock()
	// The command waiting for this job wakes the shell, which sends the read
	// and resumes waiting for the same job. Shutdown releases either wait.
	job.result <- clipboardAsyncResultMsg{request: job.request, terminalQuery: q}
	select {
	case text, ok := <-q.answer:
		if ok {
			r.content, r.readable, r.operationErr = ui.PastedLineBreaks(text), true, nil
		}
	case <-s.clipboardStop:
	case <-time.After(15 * time.Second): // Ghostty may ask the user to allow the read.
	}
	s.mu.Lock()
	if s.terminalQuery == q && !q.sent {
		s.terminalQuery = nil
	}
	s.mu.Unlock()
}

func (m *App) terminalPasteAnswer(msg tea.ClipboardMsg) {
	if msg.Selection != 'c' {
		return
	}
	s := m.asyncIO.Load()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if q := s.terminalQuery; q != nil && q.sent && !s.clipboardClosed {
		s.terminalQuery = nil
		q.answer <- msg.Content
	}
}
