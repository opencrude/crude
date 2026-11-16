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
	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/diffview"
	"crude/tui/theme"
	"crude/tui/ui"
)

const liveConflictOverlay ui.OverlayID = "live-editor-conflict"

// Changes-not-applied dialog buttons, in order. Keep is the safe default.
const (
	liveConflictDiscard = iota
	liveConflictCompare
	liveConflictKeep
)

// liveConflictPrompt decides about edits in this view that did not reach the
// shared document. It never writes shared text or disk; Discard keeps a
// durable private backup first. A file that changed on disk uses the disk
// conflict dialog instead.
type liveConflictPrompt struct {
	ed                   *editor.Editor
	state                *liveEditorState
	owner                shellport.Workspace
	generation, revision uint64
	shared               document.Snapshot
	draft                editor.DocumentRecovery
	detail               string
	busy                 bool
}
type liveConflictMsg struct {
	p         *liveConflictPrompt
	action    string
	snapshot  document.Snapshot
	err       error
	backupKey clientstate.EditorRecoveryKey
}

// frame shows the current state in the shared confirm frame. Discard and
// Compare wait for the exact shared text.
func (p *liveConflictPrompt) frame(th *theme.Theme, focused int) *ui.ComponentWidget {
	frame := ui.BuildConfirmFrame(th, ui.ConfirmFrameSpec{
		Title: "Changes not applied", Question: "Some of your changes did not reach " + p.ed.BaseTitle() + ".",
		Detail: p.detail, Severity: ui.ConfirmPromptWarning, Width: 66, FocusedIndex: focused,
		Buttons: []ui.ConfirmButtonSpec{{Label: "Discard", ActionID: ui.ConfirmActionDiscard}, {Label: "Compare", ActionID: ui.ConfirmActionCompare}, {Label: "Keep", ActionID: ui.ConfirmActionCancel}},
	})
	if p.busy || p.shared.ID == "" {
		setConfirmButtonDisabled(frame, liveConflictDiscard, true)
		setConfirmButtonDisabled(frame, liveConflictCompare, true)
	}
	return frame
}

func (p *liveConflictPrompt) action(index int) string {
	switch index {
	case liveConflictDiscard:
		return "use"
	case liveConflictCompare:
		return "compare"
	case liveConflictKeep:
		return "keep"
	}
	return ""
}

// OpenLiveEditorConflict opens an explicit, non-writing private draft decision.
// Entry points may invoke it for recovered or parked drafts as well as overlaps.
func (m *App) OpenLiveEditorConflict(ed *editor.Editor) tea.Cmd {
	live, ok := m.ws.(shellport.LiveDocuments)
	state := m.liveEditors[ed.Primary()]
	if !ok || ed == nil || state == nil || !ed.IsOpen() || m.overlayHost == nil {
		return nil
	}
	p := &liveConflictPrompt{ed: ed, state: state, owner: m.ws, generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), busy: true, detail: "Reading the current text…"}
	p.draft, _ = ed.ExportDocumentRecovery()
	m.overlayHost.Close(liveConflictOverlay)
	m.liveConflict = p
	m.openConfirmModal(liveConflictOverlay, p.frame(m.Theme, liveConflictKeep), func(index int) tea.Cmd {
		return m.activateLiveConflict(p, index)
	}, func() {
		if m.liveConflict == p {
			m.liveConflict = nil
		}
	})
	id, path := ed.DocumentState().DocumentID, ed.FilePath()
	untitled, _ := m.ws.(shellport.UntitledDocuments)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		if document.IsUntitledID(id) {
			if untitled != nil {
				_, err = untitled.OpenUntitledDocument(ctx, id)
			} else {
				err = errors.New("unnamed document is unavailable")
			}
		} else {
			_, err = live.OpenDocument(ctx, path) // restored attachment must own a view first
		}
		var s document.SyncResult
		if err == nil {
			s, err = live.SyncDocument(ctx, id, 0)
		}
		return liveConflictMsg{p: p, action: "loaded", snapshot: s.Snapshot, err: err}
	}
}

// activateLiveConflict maps a pressed button to its decision.
func (m *App) activateLiveConflict(p *liveConflictPrompt, index int) tea.Cmd {
	action := p.action(index)
	if action == "" || m.liveConflict != p || p.busy && action != "keep" || action != "keep" && p.shared.ID == "" {
		return nil
	}
	return m.applyLiveConflict(liveConflictMsg{p: p, action: action})
}

func (m *App) liveConflictCurrent(p *liveConflictPrompt) bool {
	return p != nil && m.liveConflict == p && p.owner == m.ws && p.generation == m.workspaceSwitchGen.Load() &&
		m.liveEditors[p.ed.Primary()] == p.state && p.ed.IsOpen() && m.overlayHost.Window(liveConflictOverlay) != nil
}

func (m *App) applyLiveConflict(msg liveConflictMsg) tea.Cmd {
	p := msg.p
	if !m.liveConflictCurrent(p) {
		return nil
	}
	refresh := func(detail string) {
		p.detail = detail
		if window := m.overlayHost.Window(liveConflictOverlay); window != nil {
			focused := liveConflictKeep
			if old, ok := window.Content.(*ui.ComponentWidget); ok {
				focused = max(0, ui.ConfirmButtonIndexFromID(old.Host().Focused()))
			}
			m.refreshConfirmModal(liveConflictOverlay, p.frame(m.Theme, focused))
		}
	}
	if msg.action == "keep" {
		m.overlayHost.Close(liveConflictOverlay)
		return nil
	}
	if msg.err != nil {
		p.busy = false
		refresh("Your changes stay in the editor. " + msg.err.Error())
		return nil
	}
	switch msg.action {
	case "loaded":
		p.busy = false
		p.shared = msg.snapshot
		refresh("Discard removes them and keeps a backup copy. Compare shows both versions. Keep leaves them in the editor.")
	case "compare":
		m.overlayHost.Close(liveConflictOverlay)
		return m.compareLiveEditorSnapshot(p.ed, p.shared, p.draft.OptimisticText, p.revision)
	case "use":
		if p.state.busy || p.state.uncertain != nil || p.ed.DocumentState().InFlight || p.ed.DocumentState().UndoPending {
			refresh("Wait. The last change is not finished yet.")
			return nil
		}
		if p.ed.Revision() != p.revision {
			refresh("Your text changed. Choose Keep, then try again.")
			return nil
		}
		c := m.composerDraftClient
		if c == nil {
			refresh("Crude cannot keep a backup copy here, so it does not discard. Your changes stay in the editor.")
			return nil
		}
		p.busy = true
		refresh("Discarding…")
		known := uint64(0)
		ioState := asyncIOStateFor(m)
		ioState.mu.Lock()
		known = ioState.editorRecoveryRevisions[c.workspaceID+"\x00"+p.draft.Path]
		ioState.mu.Unlock()
		live := p.owner.(shellport.LiveDocuments)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			result := liveConflictMsg{p: p, action: "resolved"}
			current, err := live.SyncDocument(ctx, p.shared.ID, 0)
			if err == nil && !sameLiveSnapshot(current.Snapshot, p.shared) {
				err = errors.New("the file text changed. Choose Keep, then try again")
			}
			if err == nil {
				err = p.draft.Validate()
			}
			key := clientstate.EditorRecoveryKey{ClientID: c.attachment.ClientID, WorkspaceID: c.workspaceID, Path: p.draft.Path}
			var originalPayload json.RawMessage
			if err == nil {
				old, e := c.store.LoadEditorRecovery(ctx, c.attachment, key)
				if e != nil && !errors.Is(e, clientstate.ErrNotFound) {
					err = e
				} else if old.Revision != known {
					err = clientstate.ErrConflict
				} else {
					originalPayload = append(json.RawMessage(nil), old.Payload...)
				}
			}
			if err == nil {
				raw, e := json.Marshal(p.draft)
				err = e
				if err == nil {
					if len(originalPayload) > 0 {
						raw = originalPayload
					}
					key.Path = fmt.Sprintf("%s#discard-backup-%d-%d", key.Path, p.revision, time.Now().UnixNano())
					_, err = c.store.SaveEditorRecovery(ctx, c.attachment, key, 0, raw)
					result.backupKey = key
				}
			}
			result.snapshot = current.Snapshot
			result.err = err
			return result
		}
	case "resolved":
		p.busy = false
		if p.state.busy || p.state.uncertain != nil || p.ed.Revision() != p.revision {
			refresh("Your text changed, so Crude did not discard. A backup copy is kept.")
			return nil
		}
		if err := p.ed.UseSharedDocument(msg.snapshot, p.revision); err != nil {
			refresh("Your changes stay in the editor. " + err.Error())
			return nil
		}
		p.state.err = nil
		p.state.topic = nil
		p.state.topicReady = false
		p.state.sync = false
		if topics, ok := m.ws.(shellport.LiveDocumentEpoch); ok {
			topics.ResumeDocumentTopics()
		}
		p.state.save = false
		p.state.format = false
		p.state.undo = nil
		p.state.closeAfter = false
		p.state.closeView = false
		if err := m.retargetLiveEditor(p.ed); err != nil {
			m.Feedback.NotifyError(err.Error())
		}
		m.overlayHost.Close(liveConflictOverlay)
		m.Feedback.NotifyInfo("Changes discarded. Crude kept a backup copy.")
		return m.clearResolvedEditorRecovery(p.ed)
	}
	return nil
}
func sameLiveSnapshot(a, b document.Snapshot) bool {
	return a.ID == b.ID && a.Path == b.Path && a.Encoding == b.Encoding && a.SavedEncoding == b.SavedEncoding && a.Revision == b.Revision && a.DiskHash == b.DiskHash && a.DiskExists == b.DiskExists && a.Conflict == b.Conflict && bytes.Equal(a.Text, b.Text) && bytes.Equal(a.Saved, b.Saved)
}

// compareLiveEditorSnapshot installs detached evidence into the existing native
// comparison tab; it does not read disk or consume the live source draft.
func (m *App) compareLiveEditorSnapshot(ed *editor.Editor, s document.Snapshot, draft []byte, revision uint64) tea.Cmd {
	return m.openSnapshotComparison(ed, localDiffShared, diffview.Comparison{
		Path: ed.FilePath(), BeforeLabel: "File text", AfterLabel: "Your text", Before: string(s.Text), After: string(draft),
	}, revision)
}
