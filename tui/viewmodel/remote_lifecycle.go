package viewmodel

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/workspace"
)

type remoteSessionReadyMsg struct {
	owner    shellport.Workspace
	sequence uint64
	result   *workspace.NewSessionResult
}

func (m *App) remoteSessionCommand(sessionID string) tea.Cmd {
	if m.sessionLifecyclePending || m.workspaceAttachPending {
		return nil
	}
	m.sessionLifecycleSequence++
	sequence, owner := m.sessionLifecycleSequence, m.ws
	m.sessionLifecyclePending = true
	return func() tea.Msg {
		var result *workspace.NewSessionResult
		if sessionID == "" {
			result = owner.NewAgentSession()
		} else {
			result = owner.OpenAgentSession(sessionID)
		}
		return remoteSessionReadyMsg{owner: owner, sequence: sequence, result: result}
	}
}

func (m *App) applyRemoteSession(msg remoteSessionReadyMsg) tea.Cmd {
	if msg.owner != m.ws || msg.sequence != m.sessionLifecycleSequence {
		return nil
	}
	m.sessionLifecyclePending = false
	if msg.result == nil || msg.result.Error != "" {
		detail := "Core returned no session result."
		if msg.result != nil {
			detail = msg.result.Error
		}
		m.showOperationFailure("Session could not be opened", detail)
		return nil
	}
	m.AddOrchestrator(msg.result.OrchID)
	return nil
}

type remoteWorkspaceReadyMsg struct {
	owner         shellport.Workspace
	sequence      uint64
	next          shellport.Workspace
	resumeID      string
	err           error
	draftRevision uint64
	draftEpoch    uint64
}

// attachRemoteWorkspace validates and prepares the destination without
// re-rooting or tearing down shared state. Until acknowledgement the old
// attachment remains intact. Frozen user input protects the draft snapshot.
func (m *App) attachRemoteWorkspace(root, resumeID string) tea.Cmd {
	if sameWorkspaceRoot(m.ws.RootPath(), root) {
		if resumeID != "" {
			return m.remoteSessionCommand(resumeID)
		}
		return nil
	}
	connection, ok := m.ws.(shellport.Connection)
	if !ok || m.workspaceAttachPending {
		return nil
	}
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil {
			continue
		}
		if state := m.editorSaves[tab.Editor.BufferID()]; state != nil && state.inFlight {
			m.showOperationFailure("Workspace could not be opened", "Wait for the file save to finish before switching.")
			return nil
		}
		if tab.Editor.Modified() {
			m.showDirtyClosePrompt(tab.Path)
			return nil
		}
	}
	flush, err := m.prepareRemoteDraftFlush()
	if err != nil {
		m.showOperationFailure("Draft could not be saved", err.Error())
		return nil
	}
	m.workspaceAttachSequence++
	sequence, owner := m.workspaceAttachSequence, m.ws
	draftRevision, draftEpoch := m.Input.DraftRevision(), m.Input.DraftEpoch()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	m.workspaceAttachCancel, m.workspaceAttachPending = cancel, true
	return func() tea.Msg {
		defer cancel()
		next, err := connection.AttachWorkspace(ctx, root)
		if err == nil {
			err = flush()
		}
		if err != nil && next != nil {
			_ = next.(shellport.Connection).Close()
			next = nil
		}
		if m.asyncIOStopped.Load() && next != nil {
			_ = next.(shellport.Connection).Close()
			return nil
		}
		return remoteWorkspaceReadyMsg{owner: owner, sequence: sequence, next: next, resumeID: resumeID, err: err, draftRevision: draftRevision, draftEpoch: draftEpoch}
	}
}

// prepareRemoteDraftFlush captures all values on Update. Background work sees
// immutable drafts and per-record locks, not mutable App/Input/session maps.
func (m *App) prepareRemoteDraftFlush() (func() error, error) {
	client := m.composerDraftClient
	if client == nil {
		return func() error { return nil }, nil
	}
	type write struct {
		record *composerDraftRecord
		raw    []byte
	}
	var writes []write
	if m.ActiveSession != nil {
		m.draftOwner(m.ActiveSession)
	}
	for sess, owner := range m.composerDraftOwners {
		model := owner.input
		if sess == m.ActiveSession {
			model = &m.Input
		}
		if model == nil || owner.loading && model.DraftRevision() == owner.loadRevision {
			continue
		}
		raw, err := marshalComposerDraft(*model)
		if err != nil {
			return nil, err
		}
		writes = append(writes, write{record: owner.record, raw: raw})
	}
	return func() error {
		for _, write := range writes {
			if err := write.record.save(client, write.raw); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func (m *App) applyRemoteWorkspace(msg remoteWorkspaceReadyMsg) tea.Cmd {
	if msg.owner != m.ws || msg.sequence != m.workspaceAttachSequence {
		if msg.next != nil {
			next := msg.next.(shellport.Connection)
			return func() tea.Msg { _ = next.Close(); return nil }
		}
		return nil
	}
	m.workspaceAttachPending, m.workspaceAttachCancel = false, nil
	if msg.err != nil {
		m.showOperationFailure("Workspace could not be opened", msg.err.Error())
		return nil
	}
	if msg.draftRevision != m.Input.DraftRevision() || msg.draftEpoch != m.Input.DraftEpoch() {
		m.showOperationFailure("Workspace switch was not applied", "The composer changed while the destination was opening. Your latest draft is kept; try the switch again.")
		next := msg.next.(shellport.Connection)
		return func() tea.Msg { _ = next.Close(); return nil }
	}
	connection, ok := msg.next.(shellport.Connection)
	if !ok {
		m.showOperationFailure("Workspace could not be opened", "Destination has no attachment owner.")
		return nil
	}
	state := m.beginWorkspaceSwitch()
	m.closeWorkspaceBoundOverlays()
	m.discardTerminalUI()
	old := m.ws.(shellport.Connection)
	m.ws = msg.next
	if client := m.composerDraftClient; client != nil {
		id := connection.WorkspaceID()
		if namespaced, ok := msg.next.(interface{ DraftWorkspaceID() string }); ok {
			id = namespaced.DraftWorkspaceID()
		}
		m.composerDraftClient = &composerDraftClient{store: client.store, attachment: client.attachment, workspaceID: id}
	}
	m.pendingOrchestrators = nil
	m.sessionLifecycleSequence++
	m.sessionLifecyclePending = false
	m.ws.SetEventBus(m.eventBus)
	m.applyThemePreset(themePresetFromSettings(m.ws.LoadAppSettings()))
	m.finishWorkspaceSwitch(state)
	m.editorSaves = make(map[string]*editorSaveState)
	m.applyEditorSettings(m.ws.LoadAppSettings().Editor)
	m.ws.(shellport.Replica).StartReplica()
	for _, issue := range m.ws.TakeStartupIssues() {
		m.presentOperationIssue(issue)
	}
	closeOld := func() tea.Msg { _ = old.Close(); return nil }
	if msg.resumeID != "" {
		return tea.Batch(closeOld, m.remoteSessionCommand(msg.resumeID))
	}
	return closeOld
}
