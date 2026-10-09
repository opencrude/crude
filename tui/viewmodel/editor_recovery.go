package viewmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clientstate"
	"crude/tui/editor"
)

// prepareEditorRecovery captures the immutable private binding on Update; the
// returned loader runs in the file-open command, never on the UI goroutine.
func (m *App) prepareEditorRecovery(path string) func() (clientstate.EditorRecoveryRecord, error) {
	c := m.composerDraftClient
	return func() (clientstate.EditorRecoveryRecord, error) {
		if c == nil {
			return clientstate.EditorRecoveryRecord{}, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), composerDraftIOTimeout)
		defer cancel()
		r, err := c.store.LoadEditorRecovery(ctx, c.attachment, clientstate.EditorRecoveryKey{ClientID: c.attachment.ClientID, WorkspaceID: c.workspaceID, Path: path})
		if errors.Is(err, clientstate.ErrNotFound) {
			err = nil
		}
		return r, err
	}
}

// installEditorRecovery must be called only after the file-open owner/generation
// fences accept the result and before admitting edits from the new editor.
func (m *App) installEditorRecovery(ed *editor.Editor, record clientstate.EditorRecoveryRecord) error {
	if len(record.Payload) == 0 {
		return nil
	}
	c := m.composerDraftClient
	if c == nil || record.Epoch != c.attachment.Epoch || record.Key.ClientID != c.attachment.ClientID || record.Key.WorkspaceID != c.workspaceID || record.Key.Path != ed.FilePath() {
		return clientstate.ErrStaleEpoch
	}
	var recovery editor.DocumentRecovery
	if err := json.Unmarshal(record.Payload, &recovery); err != nil {
		return err
	}
	if err := ed.RestoreDocumentRecovery(recovery); err != nil {
		return err
	}
	if recovery.InFlight != nil {
		if live := m.liveEditors[ed]; live != nil {
			live.uncertain = &liveEditorResult{owner: m.ws, generation: m.workspaceSwitchGen.Load(), ed: ed, state: live, kind: "edit", requestID: recovery.InFlight.RequestID}
		}
	}
	if recovery.CommandRequestID != "" && (recovery.CommandKind == "save" || recovery.CommandKind == "format") {
		if live := m.liveEditors[ed]; live != nil {
			live.uncertain = &liveEditorResult{owner: m.ws, generation: m.workspaceSwitchGen.Load(), ed: ed, state: live, kind: recovery.CommandKind, requestID: recovery.CommandRequestID}
		}
	}
	if recovery.CommandRequestID != "" && recovery.CommandKind == "undo" {
		if intent, ok := ed.PendingDocumentUndoIntent(recovery.CommandRedo); ok {
			if live := m.liveEditors[ed]; live != nil {
				live.uncertain = &liveEditorResult{owner: m.ws, generation: m.workspaceSwitchGen.Load(), ed: ed, state: live, kind: "undo", requestID: recovery.CommandRequestID, undo: &intent}
			}
		}
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	if state.editorRecoveryRevisions == nil {
		state.editorRecoveryRevisions = make(map[string]uint64)
	}
	state.editorRecoveryRevisions[record.Key.WorkspaceID+"\x00"+record.Key.Path] = record.Revision
	state.mu.Unlock()
	if cmd := m.OpenLiveEditorConflict(ed); cmd != nil {
		m.editorCommands = append(m.editorCommands, cmd)
	}
	return nil
}

type editorRecoveryClearedMsg struct {
	client *composerDraftClient
	key    clientstate.EditorRecoveryKey
	record clientstate.EditorRecoveryRecord
	err    error
}

// Clear only the exact record observed on recovery. The discard backup remains
// independently durable; a newer private record is never consumed.
func (m *App) clearResolvedEditorRecovery(ed *editor.Editor) tea.Cmd {
	c := m.composerDraftClient
	if c == nil {
		return nil
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	known := state.editorRecoveryRevisions[c.workspaceID+"\x00"+ed.FilePath()]
	state.mu.Unlock()
	if known == 0 {
		return nil
	}
	key := clientstate.EditorRecoveryKey{ClientID: c.attachment.ClientID, WorkspaceID: c.workspaceID, Path: ed.FilePath()}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		r, err := c.store.ClearEditorRecovery(ctx, c.attachment, key, known)
		return editorRecoveryClearedMsg{client: c, key: key, record: r, err: err}
	}
}
func (m *App) applyEditorRecoveryCleared(msg editorRecoveryClearedMsg) {
	if msg.client != m.composerDraftClient {
		return
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Draft resolution completed; older private recovery retained: " + msg.err.Error())
		return
	}
	state := asyncIOStateFor(m)
	state.mu.Lock()
	state.editorRecoveryRevisions[msg.key.WorkspaceID+"\x00"+msg.key.Path] = msg.record.Revision
	state.mu.Unlock()
}

// retainEditorRecovery runs exclusively after Bubble Tea exits. Failed writes
// leave the corresponding Editor open in memory while shutdown still releases
// background resources. An unread older draft is never overwritten.
func (m *App) retainEditorRecovery(flushErr error) map[*editor.Editor]bool {
	failed := make(map[*editor.Editor]bool)
	if flushErr == nil {
		return failed
	}
	if m.ws != nil && m.ws.Logger() != nil {
		m.ws.Logger().Warn("EDITOR", "shutdown_flush_failed", "error", flushErr.Error())
	}
	c := m.composerDraftClient
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ed, live := range m.liveEditors {
		recovery, unresolved := ed.ExportDocumentRecovery()
		if live.uncertain != nil {
			recovery.CommandKind, recovery.CommandRequestID = live.uncertain.kind, live.uncertain.requestID
			if live.uncertain.undo != nil {
				recovery.CommandRedo = live.uncertain.undo.Redo
			}
			unresolved = true
		}
		if !unresolved {
			continue
		}
		err := func() error {
			if c == nil {
				return errors.New("private client-state store is not configured; copy this draft before exiting")
			}
			if err := recovery.Validate(); err != nil {
				return err
			}
			raw, err := json.Marshal(recovery)
			if err != nil {
				return err
			}
			key := clientstate.EditorRecoveryKey{ClientID: c.attachment.ClientID, WorkspaceID: c.workspaceID, Path: recovery.Path}
			old, err := c.store.LoadEditorRecovery(ctx, c.attachment, key)
			if err != nil && !errors.Is(err, clientstate.ErrNotFound) {
				return err
			}
			known := uint64(0)
			if state := m.asyncIO.Load(); state != nil {
				state.mu.Lock()
				known = state.editorRecoveryRevisions[c.workspaceID+"\x00"+recovery.Path]
				state.mu.Unlock()
			}
			if len(old.Payload) > 0 && old.Revision != known {
				if bytes.Equal(raw, old.Payload) {
					return nil
				}
				return fmt.Errorf("%w: unread recovery already exists; preserve both drafts", clientstate.ErrConflict)
			}
			_, err = c.store.SaveEditorRecovery(ctx, c.attachment, key, old.Revision, raw)
			return err
		}()
		if err != nil {
			failed[ed] = true
			if m.ws != nil && m.ws.Logger() != nil {
				m.ws.Logger().Warn("EDITOR", "private_recovery_save_failed", "path", recovery.Path, "error", err.Error(), "action", "Draft remains in memory: copy it or repair private client-state storage before exiting.")
			}
		}
	}
	return failed
}
