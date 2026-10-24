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
	"crude/tui/ui"
	"crude/tui/wm"
)

const liveConflictOverlay ui.OverlayID = "live-editor-conflict"

type liveConflictPrompt struct {
	ui.Base
	ed                   *editor.Editor
	state                *liveEditorState
	owner                shellport.Workspace
	generation, revision uint64
	shared               document.Snapshot
	draft                editor.DocumentRecovery
	detail               *ui.TextArea
	actions              *ui.ActionBar
	busy, confirm        bool
}
type liveConflictMsg struct {
	p         *liveConflictPrompt
	action    string
	snapshot  document.Snapshot
	err       error
	backupKey clientstate.EditorRecoveryKey
}

func (p *liveConflictPrompt) Children() []ui.Component { return []ui.Component{p.detail, p.actions} }
func (p *liveConflictPrompt) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.detail.Layout(ctx, ui.Rect{W: r.W, H: max(0, r.H-2)})
	p.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *liveConflictPrompt) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() {
		return p.actions.Handle(ctx, e)
	}
	return ui.EventResult{}
}
func (p *liveConflictPrompt) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if p.busy && a.ID != "keep" {
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return liveConflictMsg{p: p, action: a.ID} }}
}
func (p *liveConflictPrompt) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Resolve shared editor draft"}
}
func (p *liveConflictPrompt) buttons() {
	label := "Use shared text"
	if p.confirm {
		label = "Confirm local discard"
	}
	p.actions.SetButtons(
		ui.ActionBarButton{Label: "Keep draft", Action: "keep", Enabled: true, Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: "Compare with shared", Action: "compare", Enabled: p.shared.ID != "", Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: label, Action: "use", Enabled: p.shared.ID != "", Variant: ui.VariantDanger})
	p.actions.FocusFirst()
}

// OpenLiveEditorConflict opens an explicit, non-writing private draft decision.
// Entry points may invoke it for recovered or parked drafts as well as overlaps.
func (m *App) OpenLiveEditorConflict(ed *editor.Editor) tea.Cmd {
	live, ok := m.ws.(shellport.LiveDocuments)
	state := m.liveEditors[ed]
	if !ok || ed == nil || state == nil || !ed.IsOpen() || m.overlayHost == nil {
		return nil
	}
	p := &liveConflictPrompt{Base: ui.NewBase("live-conflict/body"), ed: ed, state: state, owner: m.ws, generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), busy: true}
	p.draft, _ = ed.ExportDocumentRecovery()
	p.detail = ui.NewTextArea("live-conflict/detail", ui.NewTokenSet(m.Theme), true)
	p.detail.SetReadOnly(true)
	p.detail.SetValue("Loading exact shared snapshot. Your local draft remains unchanged.")
	p.actions = ui.NewActionBar("live-conflict/actions", ui.NewTokenSet(m.Theme))
	p.actions.SetCancelAction("keep")
	p.buttons()
	w, h := min(86, max(1, m.width-4)), min(15, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "live-conflict/frame", ui.FrameDialog, "Resolve shared editor draft", p, nil, false, w, h)
	frame.Host().Focus(p.actions.ID())
	fw := wm.NewFloatingWindow(string(liveConflictOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(liveConflictOverlay, fw, ui.ModalDialogPolicy(), nil)
	id, path := ed.DocumentState().DocumentID, ed.FilePath()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := live.OpenDocument(ctx, path) // restored attachment must own a view first
		var s document.SyncResult
		if err == nil {
			s, err = live.SyncDocument(ctx, id, 0)
		}
		return liveConflictMsg{p: p, action: "loaded", snapshot: s.Snapshot, err: err}
	}
}
func (m *App) liveConflictFrame(p *liveConflictPrompt) *ui.WindowFrameWidget {
	if p == nil || p.owner != m.ws || p.generation != m.workspaceSwitchGen.Load() || m.liveEditors[p.ed] != p.state || !p.ed.IsOpen() {
		return nil
	}
	fw := m.overlayHost.Window(liveConflictOverlay)
	if fw == nil {
		return nil
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || frame.Body() != p {
		return nil
	}
	return frame
}
func (m *App) applyLiveConflict(msg liveConflictMsg) tea.Cmd {
	p := msg.p
	frame := m.liveConflictFrame(p)
	if frame == nil {
		return nil
	}
	refresh := func() {
		p.buttons()
		frame.Host().MarkLayout()
		frame.Host().Focus(p.actions.ID())
		frame.MarkContentDirty()
		m.forceFrame()
	}
	if msg.action == "keep" {
		m.overlayHost.Close(liveConflictOverlay)
		return nil
	}
	if msg.err != nil {
		p.busy = false
		p.detail.SetValue("Draft retained. " + msg.err.Error())
		refresh()
		return nil
	}
	switch msg.action {
	case "loaded":
		p.busy = false
		p.shared = msg.snapshot
		p.detail.SetValue(fmt.Sprintf("Shared revision %d. Keep draft and Cancel change nothing. Compare opens immutable shared and local snapshots. Use shared text discards only this private draft after a durable backup; it never overwrites collaborators. Unknown commands must settle first.", p.shared.Revision))
		if p.shared.Conflict {
			p.detail.SetValue(p.detail.Value() + "\n\nThe disk file changed outside this shared document. Use shared text does not resolve that disk conflict or authorize overwriting disk. Keep the draft, compare with disk using the editor comparison action, and explicitly reconcile the external file before saving.")
		}
		refresh()
	case "compare":
		m.overlayHost.Close(liveConflictOverlay)
		return m.compareLiveEditorSnapshot(p.ed, p.shared, p.draft.OptimisticText, p.revision)
	case "use":
		if p.state.busy || p.state.uncertain != nil || p.ed.DocumentState().InFlight || p.ed.DocumentState().UndoPending {
			p.detail.SetValue("Original command outcome is still unknown. Keep this draft; settle its exact receipt before discarding.")
			refresh()
			return nil
		}
		if p.ed.Revision() != p.revision {
			p.detail.SetValue("Local draft changed. Keep it and reopen this decision to review the latest versions.")
			refresh()
			return nil
		}
		if !p.confirm {
			p.confirm = true
			p.detail.SetValue("Confirm discarding this local projection? A durable private backup is required and retained. Shared content is never written. Keep draft is still the default.")
			refresh()
			return nil
		}
		c := m.composerDraftClient
		if c == nil {
			p.detail.SetValue("Private recovery storage is unavailable. Draft retained; configure a private client profile before discarding.")
			refresh()
			return nil
		}
		p.busy = true
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
				err = errors.New("shared text or savepoint changed; reopen the decision")
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
			p.detail.SetValue("Draft or command state changed; backup retained, resolution refused.")
			refresh()
			return nil
		}
		if err := p.ed.UseSharedDocument(msg.snapshot, p.revision); err != nil {
			p.detail.SetValue(err.Error())
			refresh()
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
		m.Feedback.NotifyInfo("Private draft backup retained: " + msg.backupKey.Path)
		return m.clearResolvedEditorRecovery(p.ed)
	}
	return nil
}
func sameLiveSnapshot(a, b document.Snapshot) bool {
	return a.ID == b.ID && a.Path == b.Path && a.Revision == b.Revision && a.DiskHash == b.DiskHash && a.DiskExists == b.DiskExists && a.Conflict == b.Conflict && bytes.Equal(a.Text, b.Text) && bytes.Equal(a.Saved, b.Saved)
}

// compareLiveEditorSnapshot installs detached evidence into the existing native
// comparison tab; it does not read disk or consume the live source draft.
func (m *App) compareLiveEditorSnapshot(ed *editor.Editor, s document.Snapshot, draft []byte, revision uint64) tea.Cmd {
	_ = m.openLocalDiff(ed, localDiffShared)
	view := m.activeLocalDiff()
	if view == nil {
		return nil
	}
	if view.cancel != nil {
		view.cancel()
		view.cancel = nil
	}
	view.sequence++
	ctx, cancel := m.sourceControlContext()
	view.cancel = cancel
	view.screen.SetLoading(true)
	msg := localDiffPreparedMsg{owner: view, sequence: view.sequence, generation: m.workspaceSwitchGen.Load(), widthGen: m.comparisonWidthGen, revision: revision, diskGeneration: ed.DiskGeneration()}
	path := ed.FilePath()
	before, after := string(s.Text), string(draft)
	return m.comparisonCommand(cancel, func() tea.Msg {
		defer cancel()
		msg.prepared, msg.err = diffview.PrepareContext(ctx, diffview.Comparison{Path: path, BeforeLabel: fmt.Sprintf("Shared snapshot · revision %d", s.Revision), AfterLabel: fmt.Sprintf("Private draft · revision %d", revision), Before: before, After: after})
		return msg
	})
}
