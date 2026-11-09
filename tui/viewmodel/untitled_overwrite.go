package viewmodel

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/diffview"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const untitledOverwriteOverlay ui.OverlayID = "untitled-overwrite"

type untitledOverwriteState struct {
	owner     *untitledSaveOwner
	preview   workspace.ReviewedEditPreview
	port      shellport.ReviewedEdits
	revision  uint64
	attempted bool
	busy      bool
	cancel    context.CancelFunc
}

type untitledOverwriteResult struct {
	review     *untitledOverwriteState
	action     string
	result     workspace.ReviewedEditResult
	comparison workspace.UntitledOverwriteComparison
	err        error
}

func (m *App) currentUntitledReview(p *untitledOverwriteState) bool {
	return p != nil && m.untitledSave == p.owner && p.owner.review == p &&
		p.owner.workspace == m.ws && p.owner.generation == m.workspaceSwitchGen.Load()
}

func (m *App) showUntitledOverwrite() {
	if m.untitledSave == nil || !m.currentUntitledReview(m.untitledSave.review) {
		return
	}
	p := m.untitledSave.review
	if !p.attempted {
		p.owner.picker.SetSavePending(true, "")
	}
	spec := ui.ConfirmFrameSpec{
		Title: "Replace File", Question: "Replace " + filepath.Base(p.preview.Files[0].Path) + "?",
		Detail:   p.preview.Files[0].Path + "\nCrude keeps the old file in Local History.",
		Severity: ui.ConfirmPromptDanger, Width: min(76, max(36, m.width-4)),
		FocusedIndex: 2,
		Buttons: []ui.ConfirmButtonSpec{
			{Label: "Replace", ActionID: ui.ConfirmActionDiscard},
			{Label: "Compare", ActionID: "compare"},
			{Label: "Cancel", ActionID: ui.ConfirmActionCancel},
		},
	}
	if p.attempted {
		spec.Title, spec.Question = "Save File", "Could not confirm the save."
		spec.Detail = "Check the original result. Do not save this file again."
		spec.Severity, spec.FocusedIndex = ui.ConfirmPromptWarning, 1
		spec.Buttons = []ui.ConfirmButtonSpec{{Label: "Check result", ActionID: ui.ConfirmActionSave}, {Label: "Close", ActionID: ui.ConfirmActionCancel}}
	}
	frame := ui.BuildConfirmFrame(m.Theme, spec)
	w, h := frame.Measure(m.width, m.height)
	win := wm.NewFloatingWindow(string(untitledOverwriteOverlay), "", frame, w, h,
		wm.WithMovable(true), wm.WithModal(true), wm.WithFocusable(true),
		wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(win, m.width, m.height)
	m.overlayHost.RegisterContentHandler(untitledOverwriteOverlay, func(_ *ui.OverlayHost, win *wm.FloatingWindow, x, y int, _ tea.Msg) tea.Cmd {
		if index := confirmFrameButtonAt(win, x, y); index >= 0 {
			return m.chooseUntitledOverwrite(p, index)
		}
		return nil
	})
	m.overlayHost.RegisterHoverHandler(untitledOverwriteOverlay, func(_ *ui.OverlayHost, win *wm.FloatingWindow, x, y int, _ tea.MouseButton) {
		if widget, ok := win.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(widget, confirmFrameButtonAt(win, x, y))
		}
	})
	policy := ui.ModalDialogPolicy()
	policy.Movable = true
	m.overlayHost.Open(untitledOverwriteOverlay, win, policy, nil)
	m.forceFrame()
}

func (m *App) handleUntitledOverwriteInput(msg tea.Msg) (bool, tea.Cmd) {
	if m.overlayHost == nil {
		return false, nil
	}
	top := m.overlayHost.Top()
	if top == nil || top.ID != untitledOverwriteOverlay || m.untitledSave == nil {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	p := m.untitledSave.review
	if !m.currentUntitledReview(p) || p.busy {
		return true, nil
	}
	frame, ok := top.Window.Content.(*ui.ComponentWidget)
	if !ok {
		return true, nil
	}
	count := 3
	if p.attempted {
		count = 2
	}
	m.forceFrame()
	switch key.Code {
	case tea.KeyEsc:
		return true, m.chooseUntitledOverwrite(p, count-1)
	case tea.KeyEnter:
		return true, m.chooseUntitledOverwrite(p, ui.ConfirmButtonIndexFromID(frame.Host().Focused()))
	case tea.KeyLeft, tea.KeyUp:
		ui.MoveConfirmFocus(frame, -1, count)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if key.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		ui.MoveConfirmFocus(frame, delta, count)
	}
	return true, nil
}

func (m *App) chooseUntitledOverwrite(p *untitledOverwriteState, index int) tea.Cmd {
	if !m.currentUntitledReview(p) || p.busy {
		return nil
	}
	cancelIndex := 2
	if p.attempted {
		cancelIndex = 1
	}
	if index == cancelIndex {
		m.overlayHost.Close(untitledOverwriteOverlay)
		if p.attempted {
			if state := m.liveEditors[p.owner.editor]; state != nil {
				state.err = errors.New("file replacement outcome is unknown")
			}
			m.uncertainUntitledSave(p.owner.editor)
		} else {
			m.releaseUntitledReview(p)
			p.owner.review = nil
			m.failUntitledSave(p.owner.editor, "")
		}
		return nil
	}
	if index < 0 || index > cancelIndex {
		return nil
	}
	action := "replace"
	if p.attempted {
		action = "inspect"
	} else if index == 1 {
		action = "compare"
	}
	ed := p.owner.editor
	ds := ed.DocumentState()
	if action != "inspect" && (!ed.IsOpen() || ed.FilePath() != "" || ed.Revision() != p.revision ||
		ds.PendingEdits != 0 || ds.Revision != p.preview.Boundary.Source.Revision) {
		m.overlayHost.Close(untitledOverwriteOverlay)
		m.releaseUntitledReview(p)
		p.owner.review = nil
		m.failUntitledSave(ed, "The draft changed. Save again to check the destination.")
		return nil
	}
	p.busy = true
	if action == "replace" {
		p.attempted = true
	}
	m.overlayHost.Close(untitledOverwriteOverlay)
	p.owner.picker.SetSavePending(true, "Working…")
	m.markFilePickerDirty()
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	return m.clientIOCommand(ctx, func(parent context.Context) tea.Msg {
		ctx, stop := context.WithTimeout(parent, 30*time.Second)
		defer stop()
		msg := untitledOverwriteResult{review: p, action: action}
		switch action {
		case "replace":
			msg.result, msg.err = p.port.ApplyReviewedEdit(ctx, p.preview.ID, p.preview.Digest)
		case "inspect":
			msg.result, msg.err = p.port.InspectReviewedEdit(ctx, p.preview.ID, p.preview.Digest)
		case "compare":
			msg.comparison, msg.err = p.owner.workspace.(shellport.UntitledOverwriteReviewer).ReadUntitledOverwrite(ctx, p.preview.ID, p.preview.Digest)
		}
		return msg
	})
}

func (m *App) releaseUntitledReview(p *untitledOverwriteState) {
	if p != nil && !p.attempted {
		m.runDetachedAsyncCmd(releaseReviewedEditPreview(reviewedEditDialogRequest{
			port: p.port, preview: p.preview, generation: p.owner.generation,
		}))
	}
}

func (m *App) applyUntitledOverwriteResult(msg untitledOverwriteResult) tea.Cmd {
	p := msg.review
	if !m.currentUntitledReview(p) {
		return nil
	}
	p.busy = false
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	if msg.action == "compare" {
		if msg.err != nil {
			m.releaseUntitledReview(p)
			p.owner.review = nil
			m.failUntitledSave(p.owner.editor, "Cannot compare this file. Choose a name and save again.")
			return nil
		}
		m.releaseUntitledReview(p)
		p.owner.review = nil
		if m.workspaceSwitchIntent != nil {
			m.cancelRemoteWorkspaceSwitch(false)
		}
		m.overlayHost.Close(ui.OverlayDirtyClose)
		m.overlayHost.Close(ui.OverlayFilePicker)
		cmd := m.openSnapshotComparison(p.owner.editor, localDiffReplacement, diffview.Comparison{
			Path: msg.comparison.Path, BeforeLabel: "Existing file", AfterLabel: "New file",
			Before: string(msg.comparison.Before), After: string(msg.comparison.After),
		}, p.revision)
		if view := m.activeLocalDiff(); view != nil {
			view.returnSave = p.owner
			view.screen.SetNotice("Close to return to Save File.")
		}
		return cmd
	}
	result := msg.result
	exact := result.ID == p.preview.ID && result.Digest == p.preview.Digest
	if msg.err == nil && exact && result.State == "committed" &&
		result.SafetyReference == p.preview.ID && result.SafetySnapshotID != "" {
		p.owner.review = nil
		if state := m.liveEditors[p.owner.editor]; state != nil {
			state.firstSavePath = p.preview.Files[0].Path
			if p.owner.editor.FilePath() == state.firstSavePath {
				m.finishUntitledSave(p.owner.editor)
			}
		}
		return nil
	}
	if msg.err == nil && exact && result.State == "refused" {
		p.attempted = false // Positive no-effect result permits retiring the plan.
		m.releaseUntitledReview(p)
		p.owner.review = nil
		m.failUntitledSave(p.owner.editor, "File was not replaced. Save again to check the destination.")
		return nil
	}
	m.showUntitledOverwrite()
	return nil
}

func (m *App) restoreUntitledSave(owner *untitledSaveOwner) {
	if owner == nil || owner.workspace != m.ws || owner.generation != m.workspaceSwitchGen.Load() ||
		!owner.editor.IsOpen() || owner.editor.FilePath() != "" || m.untitledSave != nil {
		return
	}
	next := *owner
	next.review = nil
	m.startUntitledSave(&next)
}
