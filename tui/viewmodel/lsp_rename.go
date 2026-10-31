package viewmodel

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const lspRenameOverlay ui.OverlayID = "lsp-rename-preview"

type lspRenameAppliedMsg struct {
	preview    *lspRenamePreview
	generation uint64
	result     workspace.ReviewedEditResult
	err        error
}
type lspRenameReleasedMsg struct {
	preview    *lspRenamePreview
	generation uint64
	err        error
}
type lspRenameActionMsg struct {
	preview *lspRenamePreview
	action  string
}
type lspRenamePreview struct {
	ui.Base
	area      *ui.TextArea
	bar       *ui.ActionBar
	request   lspRenameResultMsg
	busy      bool
	attempted bool
	released  bool
	cancel    context.CancelFunc
}

func newLSPRenamePreview(th *theme.Theme, msg lspRenameResultMsg) *lspRenamePreview {
	tokens := ui.NewTokenSet(th)
	p := &lspRenamePreview{Base: ui.NewBase("lsp-rename/body"), request: msg}
	p.area = ui.NewTextArea("lsp-rename/detail", tokens, true)
	p.area.SetReadOnly(true)
	p.area.SetValue(fmt.Sprintf("Rename to %q in %d files.\nApply requires an unchanged canonical source and a retained Local History safety snapshot.\n\n%s", msg.newName, len(msg.preview.Files), msg.preview.Text))
	p.area.SetCursorFlat(0)
	p.bar = ui.NewActionBar("lsp-rename/actions", tokens, ui.ActionBarButton{Label: "Apply rename", Action: "apply", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	p.bar.SetCancelAction("cancel")
	p.bar.FocusLast()
	return p
}
func (p *lspRenamePreview) Children() []ui.Component { return []ui.Component{p.area, p.bar} }
func (p *lspRenamePreview) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (p *lspRenamePreview) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.area.Layout(ctx, ui.Rect{W: r.W, H: max(0, r.H-2)})
	p.bar.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *lspRenamePreview) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (p *lspRenamePreview) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return p.HandleAction(ctx, ui.Action{ID: "cancel"})
	}
	if event.IsKey() {
		switch event.Key.Code {
		case tea.KeyPgUp, tea.KeyPgDown, tea.KeyUp, tea.KeyDown, tea.KeyHome, tea.KeyEnd:
			return p.area.Handle(ctx, event)
		}
		return p.bar.Handle(ctx, event)
	}
	return ui.EventResult{}
}
func (p *lspRenamePreview) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if p.busy {
		return ui.EventResult{Handled: true}
	}
	if a.ID == "apply" || a.ID == "cancel" || a.ID == "inspect" {
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return lspRenameActionMsg{p, a.ID} }}
	}
	return ui.EventResult{}
}

func (p *lspRenamePreview) SetTokens(tokens *ui.TokenSet) {
	p.area.SetTokens(tokens)
	p.bar.SetTokens(tokens)
}

func releaseLSPRenamePreview(msg lspRenameResultMsg) tea.Cmd {
	if msg.port == nil || msg.preview.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = msg.port.ReleaseReviewedEdit(ctx, msg.preview.ID, msg.preview.Digest)
		return nil
	}
}

func (m *App) showLSPRenamePreview(msg lspRenameResultMsg) {
	p := newLSPRenamePreview(m.Theme, msg)
	m.LSPPopup.RenamePreview = p
	w, h := min(100, max(1, m.width-4)), min(26, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "lsp-rename/frame", ui.FrameDialog, "Review rename", p, nil, false, w, h)
	frame.Host().Focus(p.bar.ID())
	fw := wm.NewFloatingWindow(string(lspRenameOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(lspRenameOverlay, fw, ui.ModalDialogPolicy(), func() {
		if p.cancel != nil {
			p.cancel()
		}
		if !p.attempted && !p.released {
			m.runDetachedAsyncCmd(releaseLSPRenamePreview(p.request))
		}
		if m.LSPPopup.RenamePreview == p {
			m.LSPPopup.RenamePreview = nil
		}
	})
}

func (m *App) handleLSPRenameAction(msg lspRenameActionMsg) tea.Cmd {
	p := msg.preview
	if p == nil || m.LSPPopup.RenamePreview != p || p.busy {
		return nil
	}
	if msg.action == "cancel" {
		if !p.attempted && p.request.port != nil {
			p.busy = true
			p.bar.SetButtons(ui.ActionBarButton{Label: "Cancelling…", Action: "cancel", Enabled: false})
			ctx, _ := m.lspRequestContext()
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				err := p.request.port.ReleaseReviewedEdit(ctx, p.request.preview.ID, p.request.preview.Digest)
				return lspRenameReleasedMsg{p, p.request.generation, err}
			}
		}
		m.overlayHost.Close(lspRenameOverlay)
		return nil
	}
	r := p.request
	if r.port == nil {
		return nil
	}
	checking := msg.action == "inspect"
	if !checking && (msg.action != "apply" || p.attempted) {
		return nil
	}
	if !checking && !m.acceptsLSPDocument(r.generation, r.filePath, r.owner, r.revision) {
		m.Feedback.NotifyError("Rename review is stale; request a new review")
		m.overlayHost.Close(lspRenameOverlay)
		return nil
	}
	p.busy, p.attempted = true, true
	p.bar.SetButtons(ui.ActionBarButton{Label: "Working…", Action: "apply", Enabled: false})
	ctx, _ := m.lspRequestContext()
	ctx, p.cancel = context.WithCancel(ctx)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var result workspace.ReviewedEditResult
		var err error
		if checking {
			result, err = r.port.InspectReviewedEdit(ctx, r.preview.ID, r.preview.Digest)
		} else {
			result, err = r.port.ApplyReviewedEdit(ctx, r.preview.ID, r.preview.Digest)
		}
		return lspRenameAppliedMsg{preview: p, generation: r.generation, result: result, err: err}
	}
}

func (m *App) handleLSPRenameReleased(msg lspRenameReleasedMsg) {
	if !m.acceptsLSPGeneration(msg.generation) || m.LSPPopup.RenamePreview != msg.preview {
		return
	}
	p := msg.preview
	p.busy = false
	if msg.err == nil {
		p.released = true
		m.overlayHost.Close(lspRenameOverlay)
		return
	}
	p.area.SetValue("Could not confirm cancellation of review " + p.request.preview.ID +
		". Cancel this original review again; do not apply it.\n\n" + msg.err.Error())
	p.area.SetCursorFlat(0)
	p.bar.SetButtons(ui.ActionBarButton{Label: "Cancel review", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
}

func (m *App) handleLSPRenameApplied(msg lspRenameAppliedMsg) {
	if !m.acceptsLSPGeneration(msg.generation) || m.LSPPopup.RenamePreview != msg.preview {
		return
	}
	p, result := msg.preview, msg.result
	p.busy = false
	exact := result.ID == p.request.preview.ID && result.Digest == p.request.preview.Digest
	if msg.err == nil && p.confirmsCommit(result) {
		// Canonical topics, not this private receipt, publish all open editor text.
		m.Feedback.NotifyInfo(fmt.Sprintf("Renamed in %d files · recover via Local History (%s)", len(p.request.preview.Files), result.SafetySnapshotID[:min(8, len(result.SafetySnapshotID))]))
		m.overlayHost.Close(lspRenameOverlay)
		return
	}
	status := "Outcome unconfirmed. Check the original result; do not apply again."
	if exact && result.State == "refused" && msg.err == nil {
		status = "Rename refused without a disk effect: " + result.Error
		p.bar.SetButtons(ui.ActionBarButton{Label: "Close", Action: "cancel", Enabled: true})
	} else {
		if exact && result.Error != "" {
			status += "\n" + result.Error
		}
		if exact && result.Detail != "" {
			status += "\n" + result.Detail
		}
		if msg.err != nil {
			status += "\n" + msg.err.Error()
		}
		p.bar.SetButtons(ui.ActionBarButton{Label: "Check result", Action: "inspect", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Close", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		m.Feedback.NotifyError("Rename outcome requires inspection. Review " + p.request.preview.ID +
			"; safety snapshot " + result.SafetySnapshotID + ". Check the original result or use Local History. Do not apply it again.")
	}
	p.bar.FocusLast()
	p.area.SetValue(status + "\nReview: " + p.request.preview.ID + "\nSafety snapshot: " + result.SafetySnapshotID + "\n\n" + p.request.preview.Text)
	p.area.SetCursorFlat(0)
}

func (p *lspRenamePreview) confirmsCommit(result workspace.ReviewedEditResult) bool {
	return result.ID == p.request.preview.ID && result.Digest == p.request.preview.Digest &&
		result.State == "committed" && result.SafetySnapshotID != "" && result.SafetyReference == result.ID
}
