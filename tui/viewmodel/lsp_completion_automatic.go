package viewmodel

import (
	"context"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"crude/tui/editor"
	"crude/types"
)

// Typing intent is view-local, not a completion over speculative source. One
// cancellable debounce survives its exact edit's acknowledgement. A later edit,
// input owner or source lifetime replaces it; there is no retry queue.
type completionIntent struct {
	editor               *editor.Editor
	source               types.InsightSource
	revision, generation uint64
	feature              uint64
	line, col            int
	due, deadline        time.Time
	cancel               context.CancelFunc
}

type completionWakeMsg struct{}

func (m *App) cancelCompletionIntent() {
	if intent := m.completionIntent; intent != nil {
		intent.cancel()
		m.completionIntent = nil
	}
}

func (m *App) onEditorTyping(ed *editor.Editor, key tea.KeyPressMsg) {
	if !m.keepCompletionWhileTyping(ed) {
		m.closeCompletionPopup()
	}
	if key.Text == "(" || key.Text == "," {
		m.queueAutomaticSignatureHelp(ed)
		return
	}
	if !m.signatureEditorOwnsInput(ed) || editorRetainsCompletionKeys(ed) || m.ws.LSPMgr() == nil {
		return
	}
	service := m.ws.LSPMgr()
	settings := service.Settings()
	enabled, feature := service.CompletionsState()
	if !settings.AutoComplete || !enabled {
		return
	}
	raw := ed.LineText(ed.CursorLine())
	off := editor.VisualColToByteOffset(raw, ed.CursorCol(), editor.DefaultTabWidth)
	if off <= 0 {
		return
	}
	r, _ := utf8.DecodeLastRuneInString(raw[:off])
	if r != '.' && r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
		return
	}
	source, _ := ed.CurrentInsightSource()
	if source.DocumentID == "" || source.Lifetime == "" || source.WorkspaceGeneration == "" {
		return
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	line, col := ed.LSPCursorPosition()
	due := time.Now().Add(time.Duration(settings.CompletionDelay()) * time.Millisecond)
	m.completionIntent = &completionIntent{editor: ed, source: source, revision: ed.Revision(),
		generation: generation, feature: feature, line: line, col: col, due: due,
		deadline: due.Add(10 * time.Second), cancel: cancel}
	m.editorCommands = append(m.editorCommands, func() tea.Msg {
		timer := time.NewTimer(time.Until(due))
		defer timer.Stop()
		select {
		case <-timer.C:
			return completionWakeMsg{}
		case <-ctx.Done():
			return nil
		}
	})
}

// Drain on ordinary Update messages, including the debounce wake and canonical
// publication. Reaching the timer before the edit receipt must not lose intent.
func (m *App) autoCompletionCmd(now time.Time) tea.Cmd {
	intent := m.completionIntent
	if intent == nil {
		return nil
	}
	ed := intent.editor
	source, current := ed.CurrentInsightSource()
	line, col := ed.LSPCursorPosition()
	if !m.signatureEditorOwnsInput(ed) || editorRetainsCompletionKeys(ed) ||
		!m.acceptsLSPDocument(intent.generation, intent.source.Path, ed, intent.revision) ||
		line != intent.line || col != intent.col || now.After(intent.deadline) ||
		source.WorkspaceGeneration != intent.source.WorkspaceGeneration ||
		source.DocumentID != intent.source.DocumentID || source.Lifetime != intent.source.Lifetime ||
		m.ws.LSPMgr() == nil {
		m.cancelCompletionIntent()
		return nil
	}
	service := m.ws.LSPMgr()
	enabled, feature := service.CompletionsState()
	if !enabled || feature != intent.feature || !service.Settings().AutoComplete {
		m.cancelCompletionIntent()
		return nil
	}
	if now.Before(intent.due) || !current || !service.ServerReady() {
		return nil
	}
	owner, generation := service.ContextCursor()
	if owner != source.WorkspaceGeneration || generation == 0 {
		return nil
	}
	m.cancelCompletionIntent()
	return m.requestCompletion(true)
}
