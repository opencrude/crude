package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"crude/sessiondb"
	"crude/workspace"
	"strconv"
	"strings"
)

type attachmentOpenedMsg struct {
	owner *workspace.Session
	err   error
}

func (m *App) openHistoricalAttachment(key string) tea.Cmd {
	id, rawIndex, ok := strings.Cut(key, ":")
	index, err := strconv.Atoi(rawIndex)
	if !ok || err != nil || index < 0 || m.Prompts == nil {
		return nil
	}
	for _, entry := range m.Prompts.Entries() {
		if entry.ID != id || index >= len(entry.Attachments) {
			continue
		}
		attachment := entry.Attachments[index]
		if attachment.Unavailable || attachment.Reference.FilePath == "" {
			return nil
		}
		owner := m.ActiveSession
		store := m.RefStore()
		if store == nil {
			m.Feedback.NotifyError("Open an agent session to preview attachments")
			return nil
		}
		open := m.clientDesktopOpen()
		return func() tea.Msg {
			ref, err := store.ImportReference(attachment.Reference)
			if err == nil {
				err = open(ref.FilePath, false)
			}
			return attachmentOpenedMsg{owner: owner, err: err}
		}
	}
	return nil
}

func (m *App) openAttachment(id string, reveal bool) tea.Cmd {
	store := m.RefStore()
	if store == nil {
		return nil
	}
	ref, ok := store.GetReference(id)
	if !ok || !ref.Active() {
		return nil
	}
	if !reveal && (ref.FilePath == "" || ref.Kind == sessiondb.KindPastedTextFile) {
		m.openReferenceOverlay(id)
		return nil
	}
	owner := m.ActiveSession
	open := m.clientDesktopOpen()
	return func() tea.Msg {
		return attachmentOpenedMsg{owner: owner, err: open(ref.FilePath, reveal)}
	}
}
