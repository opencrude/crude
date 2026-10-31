package viewmodel

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const reviewedEditOverlay ui.OverlayID = "reviewed-edit-preview"

// Rename and code actions compose the same immutable review/receipt owner.
// These are presentation labels, not behavioral modes or caller-authored edits.
type reviewedEditDialogRequest struct {
	owner                                                    *editor.Editor
	revision                                                 uint64
	generation                                               uint64
	filePath                                                 string
	preview                                                  workspace.ReviewedEditPreview
	port                                                     shellport.ReviewedEdits
	title, intro, applyLabel, committedLabel, operationLabel string
	retire                                                   func(context.Context) error
}

type reviewedEditAppliedMsg struct {
	preview    *reviewedEditDialog
	generation uint64
	result     workspace.ReviewedEditResult
	err        error
}
type reviewedEditReleasedMsg struct {
	preview    *reviewedEditDialog
	generation uint64
	err        error
}
type reviewedEditCleanupMsg struct {
	request reviewedEditDialogRequest
	err     error
}
type reviewedEditActionMsg struct {
	preview *reviewedEditDialog
	action  string
}
type reviewedEditDialog struct {
	ui.Base
	area      *ui.TextArea
	bar       *ui.ActionBar
	request   reviewedEditDialogRequest
	busy      bool
	attempted bool
	released  bool
	cancel    context.CancelFunc
}

func newReviewedEditDialog(th *theme.Theme, msg reviewedEditDialogRequest) *reviewedEditDialog {
	tokens := ui.NewTokenSet(th)
	p := &reviewedEditDialog{Base: ui.NewBase("reviewed-edit/body"), request: msg}
	p.area = ui.NewTextArea("reviewed-edit/detail", tokens, true)
	p.area.SetReadOnly(true)
	p.area.SetValue(msg.intro + "\nApply requires an unchanged canonical source and a retained Local History safety snapshot.\n\n" + msg.preview.Text)
	p.area.SetCursorFlat(0)
	p.bar = ui.NewActionBar("reviewed-edit/actions", tokens, ui.ActionBarButton{Label: msg.applyLabel, Action: "apply", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	p.bar.SetCancelAction("cancel")
	p.bar.FocusLast()
	return p
}
func (p *reviewedEditDialog) Children() []ui.Component { return []ui.Component{p.area, p.bar} }
func (p *reviewedEditDialog) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (p *reviewedEditDialog) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.area.Layout(ctx, ui.Rect{W: r.W, H: max(0, r.H-2)})
	p.bar.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *reviewedEditDialog) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (p *reviewedEditDialog) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
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
func (p *reviewedEditDialog) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if p.busy {
		return ui.EventResult{Handled: true}
	}
	if a.ID == "apply" || a.ID == "cancel" || a.ID == "inspect" {
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return reviewedEditActionMsg{p, a.ID} }}
	}
	return ui.EventResult{}
}

func (p *reviewedEditDialog) SetTokens(tokens *ui.TokenSet) {
	p.area.SetTokens(tokens)
	p.bar.SetTokens(tokens)
}

func releaseReviewedEditPreview(msg reviewedEditDialogRequest) tea.Cmd {
	if msg.port == nil || msg.preview.ID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := msg.port.ReleaseReviewedEdit(ctx, msg.preview.ID, msg.preview.Digest)
		if err == nil && msg.retire != nil {
			err = msg.retire(ctx)
		}
		return reviewedEditCleanupMsg{msg, err}
	}
}

func retireReviewedEditRequest(msg reviewedEditDialogRequest) tea.Cmd {
	if msg.retire == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return reviewedEditCleanupMsg{msg, msg.retire(ctx)}
	}
}

func (m *App) showReviewedEditDialog(msg reviewedEditDialogRequest) {
	p := newReviewedEditDialog(m.Theme, msg)
	m.LSPPopup.EditPreview = p
	w, h := min(100, max(1, m.width-4)), min(26, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "reviewed-edit/frame", ui.FrameDialog, msg.title, p, nil, false, w, h)
	frame.Host().Focus(p.bar.ID())
	fw := wm.NewFloatingWindow(string(reviewedEditOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(reviewedEditOverlay, fw, ui.ModalDialogPolicy(), func() {
		if p.cancel != nil {
			p.cancel()
		}
		if !p.attempted && !p.released {
			m.runDetachedAsyncCmd(releaseReviewedEditPreview(p.request))
		} else if !p.released {
			m.runDetachedAsyncCmd(retireReviewedEditRequest(p.request))
		}
		if m.LSPPopup.EditPreview == p {
			m.LSPPopup.EditPreview = nil
		}
	})
}

func (m *App) markReviewedEditDirty(p *reviewedEditDialog) {
	if p == nil || m.LSPPopup.EditPreview != p || m.overlayHost == nil {
		return
	}
	fw := m.overlayHost.Window(reviewedEditOverlay)
	if fw == nil {
		return
	}
	if frame, ok := fw.Content.(*ui.WindowFrameWidget); ok && frame.Body() == p {
		// Replacing Apply/Cancel with Working or Check result/Close replaces
		// button children. Their bounds and control registry must be rebuilt,
		// not merely repaint the old tree with invisible new buttons.
		frame.Host().MarkLayout()
		fw.MarkContentDirty()
		m.forceFrame()
	}
}

func (m *App) handleReviewedEditAction(msg reviewedEditActionMsg) tea.Cmd {
	p := msg.preview
	if p == nil || m.LSPPopup.EditPreview != p || p.busy {
		return nil
	}
	if msg.action == "cancel" {
		if !p.attempted && p.request.port != nil {
			p.busy = true
			p.bar.SetButtons(ui.ActionBarButton{Label: "Cancelling…", Action: "cancel", Enabled: false})
			m.markReviewedEditDirty(p)
			ctx, _ := m.lspRequestContext()
			return func() tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				err := p.request.port.ReleaseReviewedEdit(ctx, p.request.preview.ID, p.request.preview.Digest)
				if err == nil && p.request.retire != nil {
					err = p.request.retire(ctx)
				}
				return reviewedEditReleasedMsg{p, p.request.generation, err}
			}
		}
		m.overlayHost.Close(reviewedEditOverlay)
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
		m.Feedback.NotifyError("Edit review is stale; request a new review")
		m.overlayHost.Close(reviewedEditOverlay)
		return nil
	}
	p.busy, p.attempted = true, true
	p.bar.SetButtons(ui.ActionBarButton{Label: "Working…", Action: "apply", Enabled: false})
	m.markReviewedEditDirty(p)
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
		return reviewedEditAppliedMsg{preview: p, generation: r.generation, result: result, err: err}
	}
}

func (m *App) handleReviewedEditReleased(msg reviewedEditReleasedMsg) {
	if !m.acceptsLSPGeneration(msg.generation) || m.LSPPopup.EditPreview != msg.preview {
		return
	}
	p := msg.preview
	p.busy = false
	if msg.err == nil {
		p.released = true
		m.overlayHost.Close(reviewedEditOverlay)
		return
	}
	p.area.SetValue("Could not confirm cancellation of review " + p.request.preview.ID +
		". Cancel this original review again; do not apply it.\n\n" + msg.err.Error())
	p.area.SetCursorFlat(0)
	p.bar.SetButtons(ui.ActionBarButton{Label: "Cancel review", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	m.markReviewedEditDirty(p)
}

func (m *App) handleReviewedEditApplied(msg reviewedEditAppliedMsg) {
	if !m.acceptsLSPGeneration(msg.generation) || m.LSPPopup.EditPreview != msg.preview {
		return
	}
	p, result := msg.preview, msg.result
	p.busy = false
	exact := result.ID == p.request.preview.ID && result.Digest == p.request.preview.Digest
	if msg.err == nil && p.confirmsCommit(result) {
		// Canonical topics, not this private receipt, publish all open editor text.
		m.Feedback.NotifyInfo(fmt.Sprintf("%s in %d files · recover via Local History (%s)", p.request.committedLabel, len(p.request.preview.Files), result.SafetySnapshotID[:min(8, len(result.SafetySnapshotID))]))
		m.overlayHost.Close(reviewedEditOverlay)
		return
	}
	status := "Outcome unconfirmed. Check the original result; do not apply again."
	if exact && result.State == "refused" && msg.err == nil {
		status = p.request.operationLabel + " refused without a disk effect: " + result.Error
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
		m.Feedback.NotifyError("Edit outcome requires inspection. Review " + p.request.preview.ID +
			"; safety snapshot " + result.SafetySnapshotID + ". Check the original result or use Local History. Do not apply it again.")
	}
	p.bar.FocusLast()
	p.area.SetValue(status + "\nReview: " + p.request.preview.ID + "\nSafety snapshot: " + result.SafetySnapshotID + "\n\n" + p.request.preview.Text)
	p.area.SetCursorFlat(0)
	m.markReviewedEditDirty(p)
}

func (p *reviewedEditDialog) confirmsCommit(result workspace.ReviewedEditResult) bool {
	return result.ID == p.request.preview.ID && result.Digest == p.request.preview.Digest &&
		result.State == "committed" && result.SafetySnapshotID != "" && result.SafetyReference == result.ID
}

func (m *App) handleReviewedEditCleanup(msg reviewedEditCleanupMsg) {
	if msg.err != nil && m.acceptsLSPGeneration(msg.request.generation) {
		m.Feedback.NotifyInfo("Pending edit-capability release is unconfirmed; original outcomes and retained recovery are unchanged.")
	}
}
