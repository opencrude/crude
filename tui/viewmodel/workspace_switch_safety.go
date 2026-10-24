package viewmodel

import (
	"context"
	"errors"
	"sort"
	"sync"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/input"
	"crude/tui/ui"
	"crude/workspace"
)

// A switch is one shell-owned intent, not a dirty-tab close followed by a new
// navigation request. Decisions retain their exact editor lifetime and revision.
// Nothing is closed until destination preparation and all preconditions succeed.
type remoteWorkspaceSwitchIntent struct {
	owner          shellport.Workspace
	sequence       uint64
	root, resumeID string
	attaching      bool
	prompt         *editor.Editor
	saving         bool
	keepShared     map[*editor.Editor]workspaceSwitchEditorSeal
	editors        map[*editor.Editor]workspaceSwitchEditorSeal
	drafts         map[*workspace.Session]workspaceSwitchDraftSeal
	session        *workspace.Session
	draftEpoch     uint64
	preparation    *remoteWorkspacePreparation
	// A backend switch supplies a different SDK attachment factory but shares
	// every editor/draft decision and preparation lease with workspace navigation.
	prepare   func(context.Context) (shellport.Workspace, error)
	backendID string
}

// A prepared attachment remains command-owned until Update explicitly claims
// it. Cancellation/timeout releases it even if the ready message is never
// delivered during shell shutdown. The callback captures no mutable App state.
type remoteWorkspacePreparation struct {
	mu        sync.Mutex
	ctx       context.Context
	owner     shellport.Workspace
	next      shellport.Workspace
	abandoned bool
	claimed   bool
	released  chan struct{}
	done      chan struct{}
	cancel    context.CancelFunc
	start     tea.Cmd
}

func (p *remoteWorkspacePreparation) publish(next shellport.Workspace) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.abandoned || p.ctx.Err() != nil {
		return false
	}
	p.next = next
	return true
}

func (p *remoteWorkspacePreparation) claim(next shellport.Workspace) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.abandoned || p.claimed || p.ctx.Err() != nil || p.next != next {
		return false
	}
	p.claimed, p.next = true, nil
	close(p.released)
	return true
}

// abandon is called only from an async command or context cancellation worker.
// It never closes an adopted/origin attachment and releases a candidate once.
func (p *remoteWorkspacePreparation) abandon() {
	p.mu.Lock()
	if p.claimed || p.abandoned {
		p.mu.Unlock()
		return
	}
	p.abandoned = true
	next := p.next
	p.next = nil
	p.mu.Unlock()
	defer close(p.released)
	if close := closePreparedWorkspace(next, p.owner, p.owner); close != nil {
		close()
	}
}

// stopRemoteWorkspaceSwitch is the exclusive post-program shutdown barrier.
// Cancellation is cheap on Update; only teardown joins prepared-owner cleanup.
func (m *App) stopRemoteWorkspaceSwitch() {
	m.cancelRemoteWorkspaceSwitch(false)
	for preparation := range m.workspaceSwitchPreparations {
		preparation.cancel()
	}
	for preparation := range m.workspaceSwitchPreparations {
		// Observe the original exact command even if Bubble Tea queued it but
		// never began it. Once admission joins an active call, never replays it.
		preparation.start()
		preparation.abandon()
		<-preparation.done
		<-preparation.released
		delete(m.workspaceSwitchPreparations, preparation)
	}
}

func (m *App) pruneRemoteWorkspacePreparations() {
	for preparation := range m.workspaceSwitchPreparations {
		select {
		case <-preparation.done:
			select {
			case <-preparation.released:
				delete(m.workspaceSwitchPreparations, preparation)
			default:
			}
		default:
		}
	}
}

type workspaceSwitchEditorSeal struct {
	path, bufferID            string
	revision, canonical, disk uint64
	modified                  bool
}

type workspaceSwitchDraftSeal struct {
	owner           *composerDraftOwner
	revision, epoch uint64
}

func switchEditorSeal(ed *editor.Editor) workspaceSwitchEditorSeal {
	return workspaceSwitchEditorSeal{path: ed.FilePath(), bufferID: ed.BufferID(), revision: ed.Revision(), canonical: ed.DocumentState().Revision, disk: ed.DiskGeneration(), modified: ed.Modified()}
}

func (m *App) remoteWorkspaceSwitchStatus() string {
	intent := m.workspaceSwitchIntent
	if intent == nil {
		return ""
	}
	if intent.prompt != nil {
		return "Resolve unsaved changes to switch workspace · Esc cancels"
	}
	if intent.attaching {
		return "Opening workspace… · Esc to cancel"
	}
	return "Preparing workspace switch… · Esc to cancel"
}

func (m *App) handleWorkspaceSwitchInput(msg tea.Msg) (bool, tea.Cmd) {
	intent := m.workspaceSwitchIntent
	if intent == nil {
		return false, nil
	}
	// Only the switch's own decision dialog may consume input while preparing.
	// An unrelated dirty-close dialog is never adopted or cancelled by a switch.
	if intent.prompt != nil && m.DirtyPromptPath == intent.prompt.FilePath() {
		return false, nil
	}
	switch event := msg.(type) {
	case tea.KeyPressMsg:
		if event.Code == tea.KeyEsc {
			m.cancelRemoteWorkspaceSwitch(true)
		}
		return true, nil
	case tea.PasteMsg, tea.MouseClickMsg, tea.MouseMotionMsg, tea.MouseReleaseMsg, tea.MouseWheelMsg:
		return true, nil
	}
	return false, nil
}

func (m *App) cancelRemoteWorkspaceSwitch(notify bool) {
	intent := m.workspaceSwitchIntent
	if m.workspaceAttachCancel != nil {
		m.workspaceAttachCancel()
	}
	// Cancellation is a monotonic boundary even if AttachWorkspace ignores ctx.
	// A late result can only release its own destination connection off Update.
	m.workspaceAttachSequence++
	m.workspaceAttachCancel, m.workspaceAttachPending = nil, false
	m.workspaceSwitchIntent = nil
	if intent != nil && intent.prompt != nil && m.DirtyPromptPath == intent.prompt.FilePath() {
		path := m.DirtyPromptPath
		m.overlayHost.Close(ui.OverlayDirtyClose)
		m.rearmAfterDelayAutosave(path)
	}
	if notify && intent != nil && m.Feedback != nil {
		m.Feedback.NotifyInfo("Workspace switch cancelled; current workspace retained")
	}
}

// workspaceSwitchEditorPending deliberately checks the real coordinator, not
// just Modified: Undo may make a buffer byte-clean with unpublished work queued.
func (m *App) workspaceSwitchEditorPending(ed *editor.Editor) (bool, error) {
	if state := m.editorSaves[ed.BufferID()]; state != nil {
		if state.inFlight || state.pending {
			return true, nil
		}
		if state.err != nil {
			return false, state.err
		}
	}
	ds := ed.DocumentState()
	if ds.Conflict != nil && !errors.Is(ds.Conflict, editor.ErrDocumentPending) {
		return false, ds.Conflict
	}
	if state := m.liveEditors[ed]; state != nil {
		if state.uncertain != nil {
			return false, errors.New("a shared document outcome is uncertain; resolve it before switching")
		}
		if state.err != nil {
			return false, state.err
		}
		if live, ok := m.ws.(shellport.LiveDocuments); ok && state.owner != live.DocumentOwner() {
			state.sync = true
		}
		if state.busy || state.sync || !state.topicReady || state.save || state.undo != nil || state.closeView || state.closeAfter {
			return true, nil
		}
	} else if ed.Modified() {
		return false, errors.New("an unsaved editor is not bound to the shared document owner; save it before switching")
	}
	return ds.PendingEdits > 0 || ds.InFlight || ds.UndoPending || ds.HistoryQueued, nil
}

func (m *App) failRemoteWorkspaceSwitch(detail string) {
	m.cancelRemoteWorkspaceSwitch(false)
	m.showOperationFailure("Workspace switch was not applied", detail+" Your current workspace and drafts are retained.")
}

// continueRemoteWorkspaceSwitch runs only on Update. Existing asynchronous
// editor commands publish local edits and settle saves/history; this method
// observes their results and never runs an I/O barrier on the UI goroutine.
func (m *App) continueRemoteWorkspaceSwitch() tea.Cmd {
	m.pruneRemoteWorkspacePreparations()
	intent := m.workspaceSwitchIntent
	if intent == nil || intent.attaching {
		return nil
	}
	if intent.owner != m.ws || intent.sequence != m.workspaceAttachSequence || m.asyncIOStopped.Load() {
		m.cancelRemoteWorkspaceSwitch(false)
		return nil
	}
	paths := make([]string, 0, len(m.EditorState.Tabs))
	for path := range m.EditorState.Tabs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		tab := m.EditorState.Tabs[path]
		if tab == nil || tab.Editor == nil || !tab.Editor.IsOpen() {
			continue
		}
		ed := tab.Editor
		pending, err := m.workspaceSwitchEditorPending(ed)
		if err != nil {
			m.failRemoteWorkspaceSwitch(err.Error())
			return nil
		}
		if pending {
			return nil
		}
		if intent.prompt == ed {
			if !intent.saving {
				return nil
			}
			intent.saving = false
			if ed.Modified() {
				// A peer/new local revision was not part of that Save decision.
				return nil
			}
			m.overlayHost.Close(ui.OverlayDirtyClose)
			intent.prompt = nil
		}
		if ed.Modified() {
			if seal, approved := intent.keepShared[ed]; approved && seal == switchEditorSeal(ed) {
				continue
			}
			if m.DirtyPromptPath != "" {
				return nil
			}
			intent.prompt = ed
			m.showDirtyClosePrompt(path)
			return nil
		}
	}
	if intent.prompt != nil {
		// A pending external close/retarget cannot authorize another editor.
		m.failRemoteWorkspaceSwitch("The editor awaiting a decision changed.")
		return nil
	}
	if len(m.pendingInputAdmissions) != 0 || m.sessionLifecyclePending {
		m.failRemoteWorkspaceSwitch("Wait for the pending session or input acknowledgement before switching.")
		return nil
	}
	if m.ActiveSession == nil && switchComposerHasWork(m.Input) {
		m.failRemoteWorkspaceSwitch("The composer has no owning session. Select an agent or copy this draft before switching.")
		return nil
	}
	if m.composerDraftClient == nil {
		unsupported := switchComposerHasWork(m.Input)
		for _, owner := range m.composerDraftOwners {
			unsupported = unsupported || owner.input != nil && switchComposerHasWork(*owner.input)
		}
		if unsupported {
			m.failRemoteWorkspaceSwitch("The private composer store is unavailable. Copy or save this draft before switching.")
			return nil
		}
	}
	if m.composerDraftErr != nil {
		m.failRemoteWorkspaceSwitch("The composer draft could not be saved: " + m.composerDraftErr.Error())
		return nil
	}
	for _, owner := range m.composerDraftOwners {
		if len(owner.inFlight) != 0 || len(owner.queued) != 0 {
			// Await the existing ordered writer before the final immutable flush;
			// an older command must never overwrite that flush after detachment.
			return nil
		}
	}
	return m.prepareRemoteWorkspaceAttachment(intent)
}

func switchComposerHasWork(model input.Model) bool {
	return model.Value() != "" || len(model.SnapshotReferenceIDs()) != 0
}

func (m *App) workspaceSwitchPrompt() (*remoteWorkspaceSwitchIntent, *editor.Editor) {
	intent := m.workspaceSwitchIntent
	if intent == nil || intent.owner != m.ws || intent.sequence != m.workspaceAttachSequence || intent.prompt == nil {
		return nil, nil
	}
	if tab := m.EditorState.Tabs[m.DirtyPromptPath]; tab != nil && tab.Editor == intent.prompt {
		return intent, tab.Editor
	}
	return nil, nil
}

func (m *App) remoteWorkspaceSwitchStillSafe(intent *remoteWorkspaceSwitchIntent) bool {
	if m.historyOperation != nil {
		return false
	}
	if intent == nil || m.workspaceSwitchIntent != intent || intent.owner != m.ws || intent.sequence != m.workspaceAttachSequence || !m.workspaceAttachPending || m.asyncIOStopped.Load() || intent.session != m.ActiveSession || intent.draftEpoch != m.composerDraftEpoch || len(intent.drafts) != len(m.composerDraftOwners) {
		return false
	}
	for session, seal := range intent.drafts {
		owner := m.composerDraftOwners[session]
		if owner != seal.owner || owner == nil {
			return false
		}
		if owner.input != nil && (seal.revision != owner.input.DraftRevision() || seal.epoch != owner.input.DraftEpoch()) {
			return false
		}
	}
	count := 0
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil || !tab.Editor.IsOpen() {
			continue
		}
		count++
		seal, ok := intent.editors[tab.Editor]
		pending, err := m.workspaceSwitchEditorPending(tab.Editor)
		if !ok || pending || err != nil || seal != switchEditorSeal(tab.Editor) {
			return false
		}
	}
	return count == len(intent.editors) && len(m.pendingInputAdmissions) == 0 && !m.sessionLifecyclePending
}
