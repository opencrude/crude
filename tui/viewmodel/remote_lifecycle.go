package viewmodel

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
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
	intent        *remoteWorkspaceSwitchIntent
}

// attachRemoteWorkspace validates and prepares the destination without
// re-rooting or tearing down shared state. Until acknowledgement the old
// attachment remains intact. One exact intent survives asynchronous editor
// publication and each dirty decision without closing any existing views.
func (m *App) attachRemoteWorkspace(root, resumeID string) tea.Cmd {
	m.pruneRemoteWorkspacePreparations()
	if m.workspaceSwitchIntent != nil || m.asyncIOStopped.Load() {
		return nil
	}
	if sameWorkspaceRoot(m.ws.RootPath(), root) {
		if resumeID != "" {
			return m.remoteSessionCommand(resumeID)
		}
		return nil
	}
	_, ok := m.ws.(shellport.Connection)
	if !ok || m.workspaceAttachPending {
		return nil
	}
	if m.DirtyPromptPath != "" {
		m.showOperationFailure("Workspace could not be opened", "Finish the existing editor close decision before switching.")
		return nil
	}
	if len(m.workspaceSwitchPreparations) >= 4 {
		m.showOperationFailure("Workspace could not be opened", "Previous cancelled attachments are still finishing. Wait for their cleanup before switching again.")
		return nil
	}
	m.workspaceAttachSequence++
	m.workspaceSwitchIntent = &remoteWorkspaceSwitchIntent{owner: m.ws, sequence: m.workspaceAttachSequence, root: root, resumeID: resumeID, keepShared: make(map[*editor.Editor]workspaceSwitchEditorSeal)}
	m.workspaceAttachPending = true
	if m.rootHost != nil {
		m.rootHost.CancelPointerCapture()
	}
	return m.continueRemoteWorkspaceSwitch()
}

func (m *App) prepareRemoteWorkspaceAttachment(intent *remoteWorkspaceSwitchIntent) tea.Cmd {
	flush, err := m.prepareRemoteDraftFlush()
	if err != nil {
		m.failRemoteWorkspaceSwitch(err.Error())
		return nil
	}
	intent.editors = make(map[*editor.Editor]workspaceSwitchEditorSeal)
	for _, tab := range m.EditorState.Tabs {
		if tab != nil && tab.Editor != nil && tab.Editor.IsOpen() {
			intent.editors[tab.Editor] = switchEditorSeal(tab.Editor)
		}
	}
	intent.session, intent.draftEpoch = m.ActiveSession, m.composerDraftEpoch
	intent.drafts = make(map[*workspace.Session]workspaceSwitchDraftSeal)
	for session, owner := range m.composerDraftOwners {
		seal := workspaceSwitchDraftSeal{owner: owner}
		if owner.input != nil {
			seal.revision, seal.epoch = owner.input.DraftRevision(), owner.input.DraftEpoch()
		}
		intent.drafts[session] = seal
	}
	intent.attaching = true
	sequence, owner := intent.sequence, intent.owner
	connection := owner.(shellport.Connection)
	root, resumeID := intent.root, intent.resumeID
	draftRevision, draftEpoch := m.Input.DraftRevision(), m.Input.DraftEpoch()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	preparation := &remoteWorkspacePreparation{ctx: ctx, owner: owner, cancel: cancel, released: make(chan struct{}), done: make(chan struct{})}
	intent.preparation = preparation
	if m.workspaceSwitchPreparations == nil {
		m.workspaceSwitchPreparations = make(map[*remoteWorkspacePreparation]struct{})
	}
	m.workspaceSwitchPreparations[preparation] = struct{}{}
	context.AfterFunc(ctx, preparation.abandon)
	m.workspaceAttachCancel, m.workspaceAttachPending = cancel, true
	run := func() remoteWorkspaceReadyMsg {
		defer close(preparation.done)
		var next shellport.Workspace
		err := ctx.Err()
		if err == nil {
			next, err = connection.AttachWorkspace(ctx, root)
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = flush()
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && m.asyncIOStopped.Load() {
			err = context.Canceled
		}
		if err == nil && !preparation.publish(next) {
			err = context.Canceled
		}
		if err != nil {
			cancel()
			if close := closePreparedWorkspace(next, owner, owner); close != nil {
				close()
			}
			next = nil
		}
		return remoteWorkspaceReadyMsg{owner: owner, sequence: sequence, next: next, resumeID: resumeID, err: err, draftRevision: draftRevision, draftEpoch: draftEpoch, intent: intent}
	}
	var once sync.Once
	var result remoteWorkspaceReadyMsg
	preparation.start = func() tea.Msg {
		once.Do(func() { result = run() })
		return result
	}
	return preparation.start
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
	if msg.owner != m.ws || msg.sequence != m.workspaceAttachSequence || msg.intent != m.workspaceSwitchIntent || !m.workspaceAttachPending {
		return m.closeRemoteWorkspaceReady(msg)
	}
	if msg.err != nil {
		m.cancelRemoteWorkspaceSwitch(false)
		m.showOperationFailure("Workspace could not be opened", msg.err.Error())
		return m.closeRemoteWorkspaceReady(msg)
	}
	if msg.draftRevision != m.Input.DraftRevision() || msg.draftEpoch != m.Input.DraftEpoch() || !m.remoteWorkspaceSwitchStillSafe(msg.intent) {
		m.failRemoteWorkspaceSwitch("An editor, pending operation, or composer changed while the destination was opening. Try the switch again.")
		return m.closeRemoteWorkspaceReady(msg)
	}
	connection, ok := msg.next.(shellport.Connection)
	_, replica := msg.next.(shellport.Replica)
	if !ok || !replica || msg.next == msg.owner {
		m.failRemoteWorkspaceSwitch("Destination did not provide the requested independent attachment.")
		return m.closeRemoteWorkspaceReady(msg)
	}
	if msg.intent.preparation == nil || !msg.intent.preparation.claim(msg.next) {
		m.failRemoteWorkspaceSwitch("Destination preparation expired or was cancelled.")
		return m.closeRemoteWorkspaceReady(msg)
	}
	m.cancelRemoteWorkspaceSwitch(false)
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
	m.liveEditors, m.liveOpenRefs, m.editorCommands = nil, nil, nil
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

func (m *App) closeRemoteWorkspaceReady(msg remoteWorkspaceReadyMsg) tea.Cmd {
	if msg.intent != nil && msg.intent.preparation != nil {
		preparation := msg.intent.preparation
		return func() tea.Msg { preparation.abandon(); return nil }
	}
	return closePreparedWorkspace(msg.next, msg.owner, m.ws)
}

// The prepared attachment is the command's only resource. Never close a live
// current/origin owner, and never perform connection teardown on Update.
func closePreparedWorkspace(next, origin, current shellport.Workspace) tea.Cmd {
	if next == nil || next == origin || next == current {
		return nil
	}
	if connection, ok := next.(shellport.Connection); ok {
		return func() tea.Msg { _ = connection.Close(); return nil }
	}
	return nil
}
