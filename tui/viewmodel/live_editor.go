package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"math/rand/v2"

	"crude/coreprotocol"
	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// All mutable coordinator and editor state is owned by Update. Commands capture
// immutable requests; at most one operation per document is outstanding.
type liveEditorState struct {
	busy, sync, save, closeAfter, closeView bool
	reason                                  workspace.DocumentSaveReason
	undo                                    *editor.DocumentUndoIntent
	due                                     time.Time
	err                                     error
	format                                  bool
	uncertain                               *liveEditorResult
	recoverDue                              time.Time
	refs                                    *liveDocumentRefs
	owner                                   document.Owner
	done                                    chan liveEditorResult
	start                                   tea.Cmd
	topic                                   *shellport.LiveDocumentsChangedMsg
	topicReady                              bool
	pushedRevision                          uint64
}

// Core view registration is owner/document keyed, not counted. Serialize opens
// and final releases and retain a shell-local count so stale opens cannot revoke
// a newer same-document view.
type liveDocumentRefs struct {
	mu     sync.Mutex
	owner  shellport.Workspace
	live   shellport.LiveDocuments
	counts map[string]int
}

func (r *liveDocumentRefs) open(ctx context.Context, path string) (document.Snapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.live.OpenDocument(ctx, path)
	if err == nil {
		r.counts[s.ID]++
	}
	return s, err
}

func (r *liveDocumentRefs) releaseCmd(id string) tea.Cmd {
	return func() tea.Msg {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.counts[id] > 1 {
			r.counts[id]--
			return nil
		}
		delete(r.counts, id)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.live.CloseDocument(ctx, id)
		return nil
	}
}

type liveEditorResult struct {
	owner      shellport.Workspace
	generation uint64
	ed         *editor.Editor
	state      *liveEditorState
	kind       string
	receipt    document.Receipt
	sync       document.SyncResult
	snapshot   document.Snapshot
	undo       *editor.DocumentUndoIntent
	err        error
	requestID  string
	settled    bool
}

func (m *App) installLiveEditor(s document.Snapshot) error {
	if tab := m.EditorState.Tabs[s.Path]; tab != nil && tab.Editor.IsOpen() {
		return nil
	}
	live := m.ws.(shellport.LiveDocuments)
	ed := editor.New(m.Theme)
	if err := ed.InstallDocument(s); err != nil {
		return err
	}
	if err := ed.BindDocumentOwner(live.DocumentOwner()); err != nil {
		ed.Close()
		return err
	}
	if m.liveEditors == nil {
		m.liveEditors = make(map[*editor.Editor]*liveEditorState)
	}
	state := &liveEditorState{owner: live.DocumentOwner()}
	m.liveEditors[ed] = state
	ed.SetOnDocumentUndo(func(intent editor.DocumentUndoIntent) { state.undo = &intent })
	ed.SetOnDocumentEdited(func() { m.onEditorContentChanged(ed, s.Path, "") })
	ed.SetOnContentChanged(func(path, text string) { m.onEditorContentChanged(ed, path, text) })
	m.BufferMgr.InstallPrepared(ed.DocumentBuffer())
	m.EditorState.Tabs[s.Path] = NewEditorTab(s.Path, ed)
	if msg, ok := m.pendingDocumentTopics[s.ID]; ok {
		delete(m.pendingDocumentTopics, s.ID)
		m.applyDocumentTopic(msg)
	}
	if lsp := m.ws.LSPMgr(); lsp != nil {
		ed.SetDiagnostics(lsp.FileDiagnostics(s.Path))
	}
	return nil
}

func (m *App) drainLiveEditorCommands() tea.Cmd {
	// A stale asynchronous open can close its registration before mounting.
	// Release retained initial publications when that exact token expires.
	for id, msg := range m.pendingDocumentTopics {
		if msg.Owner != m.ws || msg.Valid != nil && !msg.Valid() {
			if msg.Done != nil {
				msg.Done(document.ErrOwner)
			}
			delete(m.pendingDocumentTopics, id)
		}
	}
	commands := m.editorCommands
	m.editorCommands = nil
	live, connected := m.ws.(shellport.LiveDocuments)
	if !connected {
		return tea.Batch(commands...)
	}
	for ed, state := range m.liveEditors {
		if state.busy || !ed.IsOpen() {
			continue
		}
		if previous := state.uncertain; previous != nil {
			lookup, ok := m.ws.(shellport.LiveDocumentReceipts)
			if previous.kind == "save" || previous.kind == "format" {
				_, ok = m.ws.(shellport.LiveDocumentCommands)
			}
			if !ok || time.Now().Before(state.recoverDue) {
				continue
			}
			state.busy = true
			state.done = make(chan liveEditorResult, 1)
			done := state.done
			state.recoverDue = time.Now().Add(time.Second)
			captured := *previous
			snapshotCommands, _ := m.ws.(shellport.LiveDocumentCommands)
			var once sync.Once
			state.start = func() tea.Msg {
				once.Do(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if captured.kind == "save" || captured.kind == "format" {
						captured.snapshot, captured.settled, captured.err = snapshotCommands.DocumentSnapshotReceipt(ctx, captured.requestID)
					} else {
						captured.receipt, captured.settled, captured.err = lookup.DocumentReceipt(ctx, captured.requestID)
					}
					captured.kind = "receipt/" + previous.kind
					done <- captured
				})
				return captured
			}
			commands = append(commands, state.start)
			continue
		}
		if state.owner != live.DocumentOwner() {
			state.sync = true
			state.err = nil
		}
		if state.sync && state.undo != nil {
			// A gesture may arrive after its preceding edit receipt but before
			// that edit's publication. It has not been submitted (busy/uncertain
			// were handled above), so return it to the ordered input queue rather
			// than letting UndoPending block the very publication it awaits.
			if err := ed.DeferDocumentUndoUntilSync(*state.undo); err != nil {
				m.liveEditorFailure(state, err)
				continue
			}
			state.undo = nil
			if pending := state.topic; pending != nil {
				state.topic = nil
				m.applyDocumentTopic(*pending)
			}
		}
		if state.sync || !state.topicReady && state.undo == nil {
			continue
		}
		if state.err != nil {
			continue
		}
		if !state.sync {
			if err := ed.BindDocumentOwner(live.DocumentOwner()); err != nil {
				m.liveEditorFailure(state, err)
				continue
			}
		}
		if !state.sync {
			ed.DrainDocumentUndo()
		}
		ds := ed.DocumentState()
		if ds.Conflict != nil && !errors.Is(ds.Conflict, editor.ErrDocumentPending) {
			continue
		}
		if m.editorSettings.AutoSave == types.AutoSaveAfterDelay && !state.due.IsZero() && !time.Now().Before(state.due) && m.DirtyPromptPath != ed.FilePath() {
			state.save, state.reason = true, workspace.DocumentSaveAfterDelay
			state.due = time.Time{}
		}
		msg := liveEditorResult{owner: m.ws, generation: m.workspaceSwitchGen.Load(), ed: ed, state: state}
		var operation func(context.Context)
		switch {
		case ds.PendingEdits > 0 && !ds.InFlight && !ds.UndoPending:
			id := fmt.Sprintf("edit-%x", rand.Uint64())
			msg.requestID = id
			change, err := ed.CaptureDocumentEdits(live.DocumentOwner(), id, id)
			if err != nil {
				m.liveEditorFailure(state, err)
				continue
			}
			if change.DocumentID == "" {
				continue
			}
			msg.kind = "edit"
			operation = func(ctx context.Context) { msg.receipt, msg.err = live.EditDocument(ctx, change) }
		case state.undo != nil:
			intent := *state.undo
			state.undo = nil
			msg.kind, msg.undo = "undo", &intent
			id := fmt.Sprintf("undo-%x", rand.Uint64())
			msg.requestID = id
			operation = func(ctx context.Context) {
				msg.receipt, msg.err = live.UndoDocument(ctx, ds.DocumentID, id, intent.ExpectedRevision, intent.Redo)
			}
		case state.save && state.format && !ds.InFlight && !ds.UndoPending && !ds.HistoryQueued:
			formatter, ok := m.ws.(shellport.LiveDocumentFormatter)
			if !ok {
				m.liveEditorFailure(state, errors.New("core formatter unavailable"))
				continue
			}
			state.format = false
			msg.kind = "format"
			msg.requestID = fmt.Sprintf("format-%x", rand.Uint64())
			snapshotCommands, _ := msg.owner.(shellport.LiveDocumentCommands)
			operation = func(ctx context.Context) {
				if snapshotCommands != nil {
					msg.snapshot, msg.err = snapshotCommands.FormatDocumentRequest(ctx, ds.DocumentID, ds.Revision, msg.requestID)
				} else {
					msg.snapshot, msg.err = formatter.FormatDocument(ctx, ds.DocumentID, ds.Revision)
				}
			}
		case state.save && !ds.InFlight && !ds.UndoPending && !ds.HistoryQueued:
			state.save = false
			msg.kind = "save"
			msg.requestID = fmt.Sprintf("save-%x", rand.Uint64())
			reason := state.reason
			snapshotCommands, _ := msg.owner.(shellport.LiveDocumentCommands)
			operation = func(ctx context.Context) {
				if snapshotCommands != nil {
					msg.snapshot, msg.err = snapshotCommands.SaveDocumentRequest(ctx, ds.DocumentID, ds.Revision, reason, msg.requestID)
				} else {
					msg.snapshot, msg.err = live.SaveDocument(ctx, ds.DocumentID, ds.Revision, reason)
				}
			}
		case state.closeView && !ds.InFlight && !ds.UndoPending && !ds.HistoryQueued:
			m.closeEditorTabForce(ed.FilePath())
			m.continuePendingTabClose()
			continue
		}
		if operation != nil {
			state.busy = true
			state.done = make(chan liveEditorResult, 1)
			done := state.done
			var once sync.Once
			state.start = func() tea.Msg {
				once.Do(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					operation(ctx)
					done <- msg
				})
				return msg
			}
			commands = append(commands, state.start)
		}
	}
	return tea.Batch(commands...)
}

// applyDocumentTopic commits on Update and only then releases the network ACK.
// Batches arriving before their command receipts remain unacknowledged, not
// projected over an unknown in-flight edit or undo request.
func (m *App) applyDocumentTopic(msg shellport.LiveDocumentsChangedMsg) tea.Cmd {
	finish := func(err error) {
		if msg.Done != nil {
			msg.Done(err)
		}
	}
	if msg.Owner != m.ws || msg.Valid != nil && !msg.Valid() {
		finish(document.ErrOwner)
		return nil
	}
	var targets []*editor.Editor
	for ed, state := range m.liveEditors {
		if !ed.IsOpen() || ed.DocumentState().DocumentID != msg.DocumentID {
			continue
		}
		targets = append(targets, ed)
		ds := ed.DocumentState()
		if ds.InFlight || ds.UndoPending {
			state.topic = &msg
			return nil
		}
	}
	if len(targets) == 0 {
		if m.pendingDocumentTopics == nil {
			m.pendingDocumentTopics = make(map[string]shellport.LiveDocumentsChangedMsg)
		}
		m.pendingDocumentTopics[msg.DocumentID] = msg
		return nil
	}
	u := msg.Update
	owner := m.ws.(shellport.LiveDocuments).DocumentOwner()
	plans := make([]*editor.PreparedDocumentUpdate, len(targets))
	var failure error
	if u.Fault != nil {
		failure = u.Fault
	}
	if msg.Header.Seq == 0 || msg.Header.Reset != (u.Snapshot != nil) || u.State.ID != msg.DocumentID {
		failure = document.ErrRevision
	}
	// All owner/path/canonical/optimistic preconditions precede every mutation.
	for i, ed := range targets {
		if failure != nil {
			break
		}
		if failure = m.preflightLiveEditorPath(ed, u.State.Path); failure != nil {
			break
		}
		plans[i], failure = ed.PrepareDocumentUpdate(owner, u.Base, u.Changes, u.State, u.Snapshot, u.Savepoint)
	}
	if failure != nil {
		for _, ed := range targets {
			m.liveEditorFailure(m.liveEditors[ed], failure)
		}
		finish(failure)
		return nil
	}
	for i, ed := range targets {
		state := m.liveEditors[ed]
		if failure = ed.CommitDocumentUpdate(plans[i]); failure != nil {
			m.liveEditorFailure(state, failure)
			finish(failure)
			return nil
		}
		state.topic = nil
		state.topicReady, state.pushedRevision = true, max(state.pushedRevision, u.State.Revision)
		state.sync = state.pushedRevision < ed.DocumentState().Revision
		state.owner = owner
		// Both operations are preflighted, and Update owns all indexes throughout.
		if failure = m.retargetLiveEditor(ed); failure != nil {
			m.liveEditorFailure(state, failure)
			finish(failure)
			return nil
		}
		if !state.sync {
			ed.CompleteDocumentHistorySync()
		}
		if state.closeAfter && !ed.Modified() {
			m.overlayHost.Close(ui.OverlayDirtyClose)
			m.closeEditorTabForce(ed.FilePath())
			m.continuePendingTabClose()
		}
	}
	finish(nil)
	return nil
}

func (m *App) liveEditorFailure(state *liveEditorState, err error) {
	state.err = err
	for ed, candidate := range m.liveEditors {
		if candidate == state {
			if cmd := m.OpenLiveEditorConflict(ed); cmd != nil {
				m.editorCommands = append(m.editorCommands, cmd)
			}
			return
		}
	}
	m.presentOperationIssue(types.OperationIssue{Title: "Shared document needs attention", Summary: "Your local draft is retained and remains editable and copyable.", Detail: err.Error(), Guidance: "Do not overwrite shared text. Copy the local draft before resolving an overlapping change. Closing this view is deferred while edits are unresolved."})
}

func (m *App) applyLiveEditorResult(msg liveEditorResult) {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || m.liveEditors[msg.ed] != msg.state || !msg.ed.IsOpen() {
		return
	}
	ed, state := msg.ed, msg.state
	state.busy = false
	state.done = nil
	state.start = nil
	if strings.HasPrefix(msg.kind, "receipt/") {
		if !msg.settled {
			return
		}
		msg.kind = strings.TrimPrefix(msg.kind, "receipt/")
		// Fault receipts carry no domain payload; reconstruct the exact context
		// from the retained request, never from today's editor state.
		if msg.receipt.DocumentID == "" {
			msg.receipt.DocumentID = ed.DocumentState().DocumentID
		}
		state.uncertain, state.err = nil, nil
	}
	if msg.err != nil {
		// An edit/undo may have committed even though its response was lost.
		// Keep the exact outstanding projection; never replay under a fresh ID.
		var fault *coreprotocol.Fault
		definitive := (errors.As(msg.err, &fault) && fault.Code != "outcome_uncertain") || errors.Is(msg.err, document.ErrOwner) || errors.Is(msg.err, document.ErrRequest)
		if !definitive && (msg.kind == "save" || msg.kind == "format") && !msg.settled {
			retained := msg
			state.uncertain = &retained
			state.recoverDue = time.Now().Add(time.Second)
			m.liveEditorFailure(state, msg.err)
			return
		}
		if msg.kind == "edit" || msg.kind == "undo" {
			if !definitive {
				retained := msg
				state.uncertain = &retained
				state.recoverDue = time.Now().Add(time.Second)
				m.liveEditorFailure(state, msg.err)
				return
			}
			msg.receipt = document.Receipt{DocumentID: ed.DocumentState().DocumentID, RequestID: msg.requestID, Error: msg.err.Error()}
		} else {
			m.liveEditorFailure(state, msg.err)
			return
		}
	}
	switch msg.kind {
	case "edit":
		err := ed.AcceptDocumentReceipt(msg.receipt)
		if err != nil {
			if !msg.receipt.Accepted && strings.Contains(msg.receipt.Error, document.ErrRevision.Error()) {
				ed.ReconcileDocumentRevision()
				state.sync = state.pushedRevision < max(msg.receipt.Revision, ed.DocumentState().Revision)
			} else {
				m.liveEditorFailure(state, err)
			}
		}
		if msg.receipt.Accepted {
			state.sync = state.pushedRevision < msg.receipt.Revision
		}
	case "undo":
		var err error
		if !msg.receipt.Accepted {
			err = errors.New(msg.receipt.Error)
		}
		if complete := ed.CompleteDocumentUndoReceipt(*msg.undo, msg.receipt, err); complete != nil {
			err = complete
		}
		if err != nil {
			m.Feedback.NotifyError("Shared undo refused; other authors' work retained: " + err.Error())
		}
		state.sync = true
		if state.pushedRevision >= msg.receipt.Revision {
			state.sync = false
			ed.CompleteDocumentHistorySync()
		}
	case "format":
		// Fetch the authoritative change group and rebase any typing that
		// arrived while formatting. A snapshot replacement would lose it.
		state.sync = true
		if state.pushedRevision >= msg.snapshot.Revision {
			state.sync = false
		}
	case "sync":
		state.owner = m.ws.(shellport.LiveDocuments).DocumentOwner()
		if err := ed.BindDocumentOwner(state.owner); err != nil {
			m.liveEditorFailure(state, err)
			return
		}
		var err error
		if msg.sync.Reset {
			err = ed.ResetDocument(msg.sync.Snapshot)
		} else {
			for _, change := range msg.sync.Changes {
				s := ed.DocumentSnapshot()
				s.Text, _, err = document.Apply(s.Text, change.Edits)
				if err != nil {
					break
				}
				s.Revision = change.BaseRevision + 1
				err = ed.ApplyDocumentDelta(change, s)
				if err != nil {
					break
				}
			}
			if err == nil {
				err = ed.AcceptDocumentSnapshot(msg.sync.Snapshot)
			}
		}
		if err != nil {
			m.liveEditorFailure(state, err)
		} else {
			ed.CompleteDocumentHistorySync()
			if err := m.retargetLiveEditor(ed); err != nil {
				m.liveEditorFailure(state, err)
			}
		}
	case "save":
		if msg.snapshot.Revision < ed.DocumentState().Revision {
			break
		}
		if err := ed.AcceptDocumentSnapshot(msg.snapshot); err != nil {
			m.liveEditorFailure(state, err)
			return
		}
		if err := m.retargetLiveEditor(ed); err != nil {
			m.liveEditorFailure(state, err)
			return
		}
		if state.closeAfter && !ed.Modified() {
			m.overlayHost.Close(ui.OverlayDirtyClose)
			m.closeEditorTabForce(ed.FilePath())
			m.continuePendingTabClose()
		} else if state.closeAfter {
			// Typing after the save capture belongs to a later revision.
			state.save = true
		}
	}
	if pending := state.topic; pending != nil {
		state.topic = nil
		m.applyDocumentTopic(*pending)
	}
}

// FlushLiveEditors is the post-program shutdown barrier, never an Update
// callback. Call before invalidating the workspace generation or closing editor
// projections/attachment. The caller must have exclusive App ownership after
// Bubble Tea exits. Already admitted commands are observed, not replayed.
func (m *App) FlushLiveEditors() error {
	if _, ok := m.ws.(shellport.LiveDocuments); !ok {
		return nil
	}
	deadline := time.Now().Add(35 * time.Second)
	var execute func(tea.Cmd)
	execute = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				execute(child)
			}
		case liveEditorResult:
			m.applyLiveEditorResult(msg)
		case paneFilesOpenedMsg:
			// An open not painted before detach has no mounted consumer.
			execute(releasePaneDocuments(msg))
		}
	}
	queued := m.editorCommands
	m.editorCommands = nil
	for _, cmd := range queued {
		execute(cmd)
	}
	for time.Now().Before(deadline) {
		pending := false
		for ed, state := range m.liveEditors {
			if !ed.IsOpen() {
				continue
			}
			if state.busy && state.done != nil {
				// The runtime may have queued but not started this exact command.
				// Once admission executes it exactly once or joins its active call.
				if state.start != nil {
					execute(state.start)
				} else {
					select {
					case result := <-state.done:
						m.applyLiveEditorResult(result)
					case <-time.After(time.Until(deadline)):
						return errors.New("shared editor shutdown acknowledgement timed out; draft retained")
					}
				}
			}
			if state.uncertain != nil {
				state.recoverDue = time.Time{}
			}
			// Bubble Tea is no longer consuming topic messages. The explicit
			// shutdown barrier may read canonical history to settle already
			// accepted commands; normal Update never takes this recovery path.
			if (state.sync || !state.topicReady) && !state.busy {
				ctx, cancel := context.WithDeadline(context.Background(), deadline)
				result, err := m.ws.(shellport.LiveDocuments).SyncDocument(ctx, ed.DocumentState().DocumentID, ed.DocumentState().Revision)
				cancel()
				state.sync = false
				m.applyLiveEditorResult(liveEditorResult{owner: m.ws, generation: m.workspaceSwitchGen.Load(), ed: ed, state: state, kind: "sync", sync: result, err: err})
			}
			if state.err != nil && state.uncertain == nil {
				return state.err
			}
			ds := ed.DocumentState()
			if ds.Conflict != nil && !errors.Is(ds.Conflict, editor.ErrDocumentPending) {
				return ds.Conflict
			}
			pending = pending || ds.PendingEdits > 0 || ds.InFlight || ds.UndoPending || ds.HistoryQueued || state.sync || state.uncertain != nil || state.undo != nil || state.save
		}
		if !pending {
			return nil
		}
		execute(m.drainLiveEditorCommands())
		for _, state := range m.liveEditors {
			if state.uncertain != nil {
				return errors.New("shared document outcome remains uncertain; draft retained")
			}
		}
	}
	return errors.New("shared editor shutdown flush timed out; draft retained")
}
