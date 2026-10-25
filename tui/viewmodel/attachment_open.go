package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/sessiondb"
	"crude/tui/ui"
	"crude/workspace"
	"strconv"
	"strings"
	"time"
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
		if attachment.Unavailable {
			return nil
		}
		store := m.RefStore()
		if store == nil {
			m.Feedback.NotifyError("Open an agent session to preview attachments")
			return nil
		}
		screen := m.sessionTextOverlay()
		if screen == nil {
			return nil
		}
		m.overlayHost.Close(ui.OverlayPromptHistory)
		m.overlayHost.Close(ui.OverlayTextOverlay)
		screen.OpenReadOnly(attachment.Reference.ID, attachment.Reference.DisplayName(), "Loading attachment…")
		m.setMode(ModeTextOverlay)
		state := &referenceOverlayState{session: m.ActiveSession, store: store, screen: screen, ref: attachment.Reference, readOnly: true, generation: m.workspaceSwitchGen.Load(), agentID: m.activeAgentTabID(), epoch: screen.OpenEpoch()}
		state.ctx, state.cancel = context.WithTimeout(context.Background(), 2*time.Minute)
		m.referenceOverlay = state
		return m.clientIOCommand(state.ctx, func(ctx context.Context) tea.Msg {
			if err := state.ctx.Err(); err != nil {
				return referenceContentLoadedMsg{owner: state, err: err}
			}
			ref, err := store.ImportReference(attachment.Reference)
			if err != nil {
				return referenceContentLoadedMsg{owner: state, err: err}
			}
			loading := *state
			loading.ref = ref
			loading.ctx = ctx
			result := loadReferencePreview(&loading)
			result.owner, result.imported = state, ref
			return result
		})
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
	if !reveal && (ref.FilePath == "" || ref.Kind == sessiondb.KindPastedTextFile || ref.Kind == sessiondb.KindPastedImage) {
		m.openReferenceOverlay(id)
		return nil
	}
	owner := m.ActiveSession
	open := m.clientDesktopOpen()
	return func() tea.Msg {
		return attachmentOpenedMsg{owner: owner, err: open(ref.FilePath, reveal)}
	}
}
