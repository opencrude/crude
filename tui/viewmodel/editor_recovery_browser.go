package viewmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"crude/clientstate"
	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
)

const editorRecoveryBrowserOverlay ui.OverlayID = "editor-recovery-browser"

type editorRecoveryBrowser struct {
	ui.Base
	owner                shellport.Workspace
	client               *composerDraftClient
	generation, sequence uint64
	ctx                  context.Context
	list                 *ui.List
	detail               *ui.TextArea
	actions              *ui.ActionBar
	busy                 bool
	record               clientstate.EditorRecoveryRecord
	draft                editor.DocumentRecovery
	sourceRevision       uint64
}
type editorRecoveryBrowserMsg struct {
	p              *editorRecoveryBrowser
	sequence       uint64
	action         string
	info           clientstate.EditorRecoveryInfo
	items          []clientstate.EditorRecoveryInfo
	record         clientstate.EditorRecoveryRecord
	draft          editor.DocumentRecovery
	sourceRevision uint64
	snapshot       document.Snapshot
	ed             *editor.Editor
	err            error
}

func (p *editorRecoveryBrowser) Children() []ui.Component {
	return []ui.Component{p.list, p.detail, p.actions}
}
func (p *editorRecoveryBrowser) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	lh := min(7, max(0, (r.H-2)/2))
	p.list.Layout(ctx, ui.Rect{W: r.W, H: lh})
	p.detail.Layout(ctx, ui.Rect{Y: lh, W: r.W, H: max(0, r.H-lh-2)})
	p.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *editorRecoveryBrowser) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() {
		return p.actions.Handle(ctx, e)
	}
	return ui.EventResult{}
}
func (p *editorRecoveryBrowser) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if p.busy && a.ID != "close" {
		return ui.EventResult{Handled: true}
	}
	msg := editorRecoveryBrowserMsg{p: p, action: a.ID}
	if a.ID == "list.activate" {
		if item, ok := a.Payload.(ui.ListItem); ok {
			msg.info, _ = item.Payload.(clientstate.EditorRecoveryInfo)
		}
		msg.action = "preview"
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return msg }}
}
func (p *editorRecoveryBrowser) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Private editor recovery"}
}
func (p *editorRecoveryBrowser) buttons() {
	ready := !p.busy && len(p.record.Payload) > 0
	p.actions.SetButtons(
		ui.ActionBarButton{Label: "Close", Action: "close", Enabled: true, Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: "Preview", Action: "preview", Enabled: !p.busy, Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: "Copy", Action: "copy", Enabled: ready, Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: "Open draft", Action: "open", Enabled: ready, Variant: ui.VariantSecondary},
		ui.ActionBarButton{Label: "Compare", Action: "compare", Enabled: ready, Variant: ui.VariantSecondary})
}

// OpenEditorRecoveryBrowser previews private records without replaying work.
// Payloads load only after explicit Preview/row activation.
func (m *App) OpenEditorRecoveryBrowser() tea.Cmd {
	if m.overlayHost == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &editorRecoveryBrowser{Base: ui.NewBase("editor-recovery/body"), owner: m.ws, client: m.composerDraftClient, generation: m.workspaceSwitchGen.Load(), ctx: ctx, busy: true, sequence: 1}
	ts := ui.NewTokenSet(m.Theme)
	p.list = ui.NewList("editor-recovery/list", ts)
	p.detail = ui.NewTextArea("editor-recovery/detail", ts, true)
	p.detail.SetReadOnly(true)
	p.detail.SetValue("Loading private recovery records. Select a row and Preview. Close changes nothing.")
	p.actions = ui.NewActionBar("editor-recovery/actions", ts)
	p.actions.SetCancelAction("close")
	p.buttons()
	p.actions.FocusFirst()
	w, h := min(96, max(1, m.width-4)), min(24, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "editor-recovery/frame", ui.FrameDialog, "Private editor recovery", p, nil, false, w, h)
	frame.Host().Focus(p.actions.ID())
	fw := wm.NewFloatingWindow(string(editorRecoveryBrowserOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(editorRecoveryBrowserOverlay, fw, ui.ModalDialogPolicy(), cancel)
	return func() tea.Msg {
		msg := editorRecoveryBrowserMsg{p: p, sequence: 1, action: "listed"}
		if p.client == nil {
			msg.err = errors.New("private client profile is unavailable")
			return msg
		}
		ctx, cancel := context.WithTimeout(p.ctx, composerDraftIOTimeout)
		defer cancel()
		msg.items, msg.err = p.client.store.ListEditorRecoveries(ctx, p.client.attachment, p.client.workspaceID)
		return msg
	}
}

func (m *App) handleEditorRecoveryBrowserMessage(raw tea.Msg) (bool, tea.Cmd) {
	msg, ok := raw.(editorRecoveryBrowserMsg)
	if !ok {
		return false, nil
	}
	p := msg.p
	if p == nil || m.overlayHost == nil || p.ctx.Err() != nil || p.owner != m.ws || p.client != m.composerDraftClient || p.generation != m.workspaceSwitchGen.Load() {
		return true, nil
	}
	fw := m.overlayHost.Window(editorRecoveryBrowserOverlay)
	if fw == nil {
		return true, nil
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok || frame.Body() != p {
		return true, nil
	}
	if msg.sequence != 0 && msg.sequence != p.sequence {
		return true, nil
	}
	refresh := func() { p.buttons(); frame.Host().MarkLayout(); frame.MarkContentDirty(); m.forceFrame() }
	if msg.action == "close" {
		m.overlayHost.Close(editorRecoveryBrowserOverlay)
		return true, nil
	}
	if msg.err != nil {
		p.busy = false
		p.detail.SetValue("Recovery retained. " + msg.err.Error())
		refresh()
		return true, nil
	}
	switch msg.action {
	case "listed":
		p.busy = false
		items := make([]ui.ListItem, 0, len(msg.items))
		for _, r := range msg.items {
			kind := "Unresolved draft"
			if strings.Contains(r.Key.Path, "#discard-backup-") {
				kind = "Discard backup"
			}
			label := path.Base(strings.ReplaceAll(r.Key.Path, "\\", "/"))
			items = append(items, ui.ListItem{ID: r.Key.Path, Label: label, Detail: fmt.Sprintf("%s · r%d · %d bytes · %s", kind, r.Revision, r.Bytes, r.Key.Path), Payload: r})
		}
		p.list.SetItems(items)
		p.detail.SetValue("Showing up to 128 private records. Select a row and Preview to load its original path and retained text. Open draft never publishes edits; an already-open editor is compared, never replaced. Close changes nothing.")
		if len(items) == 0 {
			p.detail.SetValue("No retained editor drafts in this private profile and workspace.")
		}
		refresh()
	case "preview":
		if p.busy || p.client == nil {
			return true, nil
		}
		info := msg.info
		if info.Key.Path == "" {
			if item, ok := p.list.Selected(); ok {
				info, _ = item.Payload.(clientstate.EditorRecoveryInfo)
			}
		}
		if info.Key.Path == "" {
			return true, nil
		}
		p.sequence++
		seq := p.sequence
		p.busy = true
		p.record = clientstate.EditorRecoveryRecord{}
		refresh()
		return true, func() tea.Msg {
			result := editorRecoveryBrowserMsg{p: p, sequence: seq, action: "previewed"}
			ctx, cancel := context.WithTimeout(p.ctx, composerDraftIOTimeout)
			defer cancel()
			result.record, result.err = p.client.store.LoadEditorRecovery(ctx, p.client.attachment, info.Key)
			if result.err == nil && result.record.Revision != info.Revision {
				result.err = clientstate.ErrConflict
			}
			if result.err == nil {
				result.err = json.Unmarshal(result.record.Payload, &result.draft)
			}
			if result.err == nil {
				result.err = result.draft.Validate()
			}
			if result.err == nil {
				key := info.Key
				key.Path = result.draft.Path
				source, err := p.client.store.LoadEditorRecovery(ctx, p.client.attachment, key)
				if err != nil && !errors.Is(err, clientstate.ErrNotFound) {
					result.err = err
				}
				result.sourceRevision = source.Revision
			}
			return result
		}
	case "previewed":
		p.busy = false
		p.record = msg.record
		p.draft = msg.draft
		p.sourceRevision = msg.sourceRevision
		text := string(p.draft.OptimisticText)
		if len(text) > 64<<10 {
			end := 64 << 10
			for !utf8.RuneStart(text[end]) {
				end--
			}
			text = text[:end] + "\n[Preview clipped; Copy retains complete draft]"
		}
		p.detail.SetValue(fmt.Sprintf("Original path: %s\nPrivate record revision: %d\nConflict: %s\nNo edits, saves, or old commands are sent automatically.\n\n%s", p.draft.Path, p.record.Revision, p.draft.Conflict, text))
		refresh()
	case "copy":
		if !p.busy && len(p.record.Payload) > 0 {
			m.copyToClipboard(string(p.draft.OptimisticText))
		}
	case "open", "compare":
		if p.busy || len(p.record.Payload) == 0 {
			return true, nil
		}
		live, ok := m.ws.(shellport.LiveDocuments)
		if !ok {
			p.detail.SetValue("Recovery requires a connected shared editor; use Copy to preserve this draft.")
			refresh()
			return true, nil
		}
		if tab := m.EditorState.Tabs[p.draft.Path]; tab != nil && tab.Editor.IsOpen() {
			ed := tab.Editor
			p.busy = true
			p.sequence++
			seq := p.sequence
			id := ed.DocumentState().DocumentID
			return true, func() tea.Msg {
				ctx, cancel := context.WithTimeout(p.ctx, composerDraftIOTimeout)
				defer cancel()
				s, err := live.SyncDocument(ctx, id, 0)
				return editorRecoveryBrowserMsg{p: p, sequence: seq, action: "compared", ed: ed, snapshot: s.Snapshot, err: err}
			}
		}
		if msg.action == "compare" {
			p.detail.SetValue("Open the original editor first, then Compare. Its current draft will not be replaced.")
			refresh()
			return true, nil
		}
		p.busy = true
		p.sequence++
		seq := p.sequence
		record := p.record
		key := record.Key
		key.Path = p.draft.Path
		expected := p.sourceRevision
		refresh()
		return true, func() tea.Msg {
			ctx, cancel := context.WithTimeout(p.ctx, composerDraftIOTimeout)
			defer cancel()
			r, err := p.client.store.StageEditorRecovery(ctx, p.client.attachment, record, key, expected)
			return editorRecoveryBrowserMsg{p: p, sequence: seq, action: "staged", record: r, err: err}
		}
	case "staged":
		p.busy = false
		if tab := m.EditorState.Tabs[p.draft.Path]; tab != nil && tab.Editor.IsOpen() {
			p.detail.SetValue("Editor opened while staging. Its draft is unchanged; choose Compare. The staged private recovery is retained.")
			refresh()
			return true, nil
		}
		m.overlayHost.Close(editorRecoveryBrowserOverlay)
		cmd, err := m.openEditorTab(p.draft.Path)
		if err != nil {
			m.showOperationFailure("Editor draft could not open", err.Error())
		}
		return true, cmd
	case "compared":
		p.busy = false
		if !msg.ed.IsOpen() || m.EditorState.Tabs[p.draft.Path] == nil || m.EditorState.Tabs[p.draft.Path].Editor != msg.ed {
			return true, nil
		}
		m.overlayHost.Close(editorRecoveryBrowserOverlay)
		return true, m.compareLiveEditorSnapshot(msg.ed, msg.snapshot, p.draft.OptimisticText, p.draft.Canonical.Revision)
	}
	return true, nil
}
