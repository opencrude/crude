package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/fileio"
	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/launcher"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"

	tea "charm.land/bubbletea/v2"
)

type workspaceDeletePrompt struct {
	ui.Base
	screen         *launcher.Screen
	generation     uint64
	plan           workspace.WorkspaceDeletion
	deleted        workspace.DeletedWorkspace
	busy           bool
	entry          launcher.WorkspaceEntry
	cancel         context.CancelFunc
	deleteReady    bool
	receiptID      string
	receiptRestore bool
	title          *ui.Label
	detail         *ui.TextArea
	actions        *ui.ActionBar
}
type workspaceDeleteIntent struct {
	target *workspaceDeletePrompt
	action string
}
type workspaceDeletePreviewMsg struct {
	target *workspaceDeletePrompt
	plan   workspace.WorkspaceDeletion
	err    error
}
type workspaceDeleteResultMsg struct {
	target   *workspaceDeletePrompt
	deleted  workspace.DeletedWorkspace
	restored bool
	err      error
}

func newWorkspaceDeletePrompt(th *theme.Theme, screen *launcher.Screen, generation uint64) *workspaceDeletePrompt {
	p := &workspaceDeletePrompt{Base: ui.NewBase("workspace-delete/body"), screen: screen, generation: generation}
	p.title = ui.NewLabel("workspace-delete/title", "", th)
	p.actions = ui.NewActionBar("workspace-delete/actions", ui.NewTokenSet(th))
	p.actions.SetAlignEnd(false)
	p.detail = ui.NewTextArea("workspace-delete/detail", ui.NewTokenSet(th), true)
	p.detail.SetReadOnly(true)
	p.actions.SetCancelAction("cancel")
	p.set("Checking workspace use…", "Releasing unused core resources and preparing a safe preview.", "Your project files will not be deleted.")
	p.buttons(ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	return p
}
func (p *workspaceDeletePrompt) set(lines ...string) {
	if len(lines) > 0 {
		p.title.SetText(lines[0])
	}
	if len(lines) > 1 {
		p.detail.SetValue(strings.Join(lines[1:], "\n"))
	} else {
		p.detail.SetValue("")
	}
}
func (p *workspaceDeletePrompt) buttons(buttons ...ui.ActionBarButton) {
	p.deleteReady = false
	for _, button := range buttons {
		if button.Action == "delete" {
			p.deleteReady = button.Enabled
		}
	}
	p.actions.SetButtons(buttons...)
	p.actions.FocusLast()
}
func (p *workspaceDeletePrompt) Children() []ui.Component {
	return []ui.Component{p.title, p.detail, p.actions}
}
func (p *workspaceDeletePrompt) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.title.Layout(ctx, ui.Rect{W: r.W, H: min(1, r.H)})
	p.detail.Layout(ctx, ui.Rect{Y: min(2, r.H), W: r.W, H: max(0, r.H-4)})
	// A destructive action must not be reachable when its label cannot fit.
	p.actions.SetEnabled("delete", p.deleteReady && !p.busy && r.W >= rendering.VisibleWidth("[ Delete workspace ]  [ Cancel ]"))
	p.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *workspaceDeletePrompt) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		if event.Key.Code == tea.KeyEsc {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "cancel"}}
		}
		// Let the native read-only area own scrolling/selection when focused;
		// action-bar focus keeps normal safe-default confirmation behavior.
		return ui.EventResult{}
	}
	return ui.EventResult{}
}
func (p *workspaceDeletePrompt) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if p.busy {
		return ui.EventResult{Handled: true}
	}
	switch action.ID {
	case "delete", "undo", "review", "check", "cancel":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return workspaceDeleteIntent{target: p, action: action.ID} }}
	}
	return ui.EventResult{Handled: true}
}
func (p *workspaceDeletePrompt) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Delete workspace"}
}

func workspaceDeleteErrorText(err error) string {
	var fault *coreprotocol.Fault
	if errors.As(err, &fault) {
		return fault.Message
	}
	return err.Error()
}

func (m *App) openWorkspaceDelete(entry launcher.WorkspaceEntry) {
	p := newWorkspaceDeletePrompt(m.Theme, m.launcherScreen, m.workspaceSwitchGen.Load())
	p.entry = entry
	w, h := min(80, max(24, m.width-4)), min(18, max(5, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "workspace-delete/frame", ui.FrameDialog, "Delete workspace", p, nil, false, w, h)
	frame.Frame().SetBorderAccent(ui.NewTokenSet(m.Theme).Color(ui.ColorError))
	frame.Host().Focus(p.actions.ID())
	fw := wm.NewFloatingWindow(string(ui.OverlayWorkspaceDelete), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	policy := ui.ModalDialogPolicy()
	policy.Decorated = true
	policy.Movable = true
	m.overlayHost.Open(ui.OverlayWorkspaceDelete, fw, policy, func() {
		if p.cancel != nil {
			p.cancel()
		}
	})
	m.prepareWorkspaceDelete(p)
}

func (m *App) prepareWorkspaceDelete(p *workspaceDeletePrompt) {
	if p.cancel != nil {
		p.cancel()
	}
	owner := m.ws
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p.cancel = cancel
	p.set("Checking workspace use…", "Releasing unused core resources and preparing a safe preview.", "Your project files will not be deleted.")
	p.buttons(ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	m.runDetachedAsyncCmd(func() tea.Msg {
		defer cancel()
		plan, err := owner.PreviewWorkspaceDeletion(ctx, p.entry.ID, p.entry.Path)
		return workspaceDeletePreviewMsg{target: p, plan: plan, err: err}
	})
}
func (m *App) workspaceDeleteWindow(p *workspaceDeletePrompt) *ui.WindowFrameWidget {
	if p == nil || p.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	w := m.overlayHost.Window(ui.OverlayWorkspaceDelete)
	if w == nil {
		return nil
	}
	frame, ok := w.Content.(*ui.WindowFrameWidget)
	if !ok || frame.Body() != p {
		return nil
	}
	return frame
}
func (m *App) refreshWorkspaceDelete(p *workspaceDeletePrompt) {
	if frame := m.workspaceDeleteWindow(p); frame != nil {
		frame.Host().MarkLayout()
		frame.Host().Focus(p.actions.ID())
		frame.MarkContentDirty()
	}
}
func (m *App) applyWorkspaceDeletePreview(msg workspaceDeletePreviewMsg) {
	p := msg.target
	if m.workspaceDeleteWindow(p) == nil {
		return
	}
	if msg.err != nil {
		p.set("Workspace could not be reviewed", workspaceDeleteErrorText(msg.err), "No saved data was deleted. Project files are unchanged.")
		p.buttons(ui.ActionBarButton{Label: "Review again", Action: "review", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		m.refreshWorkspaceDelete(p)
		return
	}
	if issue := msg.plan.Issue; issue != nil {
		p.set(issue.Title, issue.Summary, issue.Detail, issue.Guidance, "No saved data was deleted. Project files are unchanged.")
		p.buttons(ui.ActionBarButton{Label: "Review again", Action: "review", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		m.refreshWorkspaceDelete(p)
		return
	}
	p.plan = msg.plan
	p.set("Delete "+msg.plan.Name+"?", msg.plan.ProjectDir, fmt.Sprintf("%d saved sessions, workspace settings, memory and history.", msg.plan.SessionCount), "Project files will not be changed.", "Saved data moves to recovery storage; Undo is available.")
	p.buttons(ui.ActionBarButton{Label: "Delete workspace", Action: "delete", Enabled: true, Variant: ui.VariantDanger}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	m.refreshWorkspaceDelete(p)
}
func (m *App) handleWorkspaceDeleteIntent(msg workspaceDeleteIntent) tea.Cmd {
	p := msg.target
	if m.workspaceDeleteWindow(p) == nil || p.busy {
		return nil
	}
	if msg.action == "cancel" {
		m.overlayHost.Close(ui.OverlayWorkspaceDelete)
		return nil
	}
	if msg.action == "review" && p.actions.ActionEnabled("review") {
		m.prepareWorkspaceDelete(p)
		m.refreshWorkspaceDelete(p)
		return nil
	}
	if msg.action == "check" && p.actions.ActionEnabled("check") {
		observer, ok := m.ws.(shellport.SavedWorkspaceDeletionReceipts)
		if !ok || p.receiptID == "" {
			return nil
		}
		p.busy = true
		p.actions.SetEnabled("check", false)
		p.title.SetText("Checking the original result…")
		m.refreshWorkspaceDelete(p)
		id, restored, deleted := p.receiptID, p.receiptRestore, p.deleted
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			receipt, err := observer.WorkspaceDeletionReceipt(ctx, id)
			if err != nil {
				return workspaceDeleteResultMsg{target: p, restored: restored, deleted: deleted, err: &coreclient.UncertainError{RequestID: id, Cause: err}}
			}
			method := coreprotocol.WorkspaceDelete
			if restored {
				method = coreprotocol.WorkspaceRestore
			}
			if receipt.RequestID != id || receipt.Response.ID != id || receipt.State != "completed" || receipt.Method != method {
				return workspaceDeleteResultMsg{target: p, restored: restored, deleted: deleted, err: &coreclient.UncertainError{RequestID: id, Cause: errors.New("the original operation has no confirmed result yet; no action was repeated")}}
			}
			if receipt.Response.Error != nil {
				return workspaceDeleteResultMsg{target: p, restored: restored, deleted: deleted, err: receipt.Response.Error}
			}
			var out coreprotocol.WorkspaceDeletionResult
			if err := coreprotocol.Decode(receipt.Response.Payload, &out); err != nil {
				return workspaceDeleteResultMsg{target: p, restored: restored, deleted: deleted, err: &coreclient.UncertainError{RequestID: id, Cause: err}}
			}
			if out.Warning != "" {
				err = fileio.CommittedWarning(out.Warning)
			}
			return workspaceDeleteResultMsg{target: p, restored: restored, deleted: out.Deleted, err: err}
		}
	}
	if msg.action != "delete" && msg.action != "undo" {
		return nil
	}
	if !p.actions.ActionEnabled(msg.action) {
		return nil
	}
	p.busy = true
	p.actions.SetEnabled("delete", false)
	p.actions.SetEnabled("undo", false)
	p.actions.SetEnabled("cancel", false)
	p.title.SetText("Moving saved data to recovery…")
	if msg.action == "undo" {
		p.title.SetText("Restoring workspace…")
	}
	m.refreshWorkspaceDelete(p)
	plan, deleted, owner := p.plan, p.deleted, m.ws
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if msg.action == "undo" {
			return workspaceDeleteResultMsg{target: p, deleted: deleted, restored: true, err: owner.RestoreDeletedWorkspace(ctx, deleted)}
		}
		result, err := owner.DeleteSavedWorkspace(ctx, plan)
		return workspaceDeleteResultMsg{target: p, deleted: result, err: err}
	}
}
func (m *App) applyWorkspaceDeleteResult(msg workspaceDeleteResultMsg) {
	p := msg.target
	if m.workspaceDeleteWindow(p) == nil {
		return
	}
	p.busy = false
	if msg.err != nil && !fileio.WriteCommitted(msg.err) {
		var uncertain *coreclient.UncertainError
		var fault *coreprotocol.Fault
		unknown := errors.As(msg.err, &uncertain) || errors.As(msg.err, &fault) && strings.HasPrefix(fault.Code, "outcome_")
		if unknown {
			p.set("Workspace result is not confirmed", msg.err.Error(), "Do not repeat the operation until its original receipt is checked.", "Project files were not changed.")
			if uncertain != nil {
				p.receiptID, p.receiptRestore, p.deleted = uncertain.RequestID, msg.restored, msg.deleted
			}
			_, canObserve := m.ws.(shellport.SavedWorkspaceDeletionReceipts)
			p.buttons(ui.ActionBarButton{Label: "Check result", Action: "check", Enabled: canObserve && p.receiptID != "", Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Close", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		} else if msg.restored {
			p.set("Workspace could not be restored", workspaceDeleteErrorText(msg.err), "Recovery data was kept at:", msg.deleted.RecoveryPath, "Project files are unchanged.")
			p.buttons(ui.ActionBarButton{Label: "Close", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		} else {
			p.set("Workspace was not deleted", workspaceDeleteErrorText(msg.err), "Review again after resolving the issue. Project files are unchanged.")
			p.buttons(ui.ActionBarButton{Label: "Review again", Action: "review", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Close", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		}
	} else if msg.restored {
		p.set("Workspace restored", msg.deleted.ProjectDir)
		p.buttons(ui.ActionBarButton{Label: "Done", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	} else {
		p.deleted = msg.deleted
		// The App owns the recent-workspace catalog. Remove the committed
		// resource there as well as in Home so an unmounted Home cannot leave
		// a deleted entry in the sidebar while the asynchronous refresh runs.
		for i, info := range m.workspaceInfos {
			if info.ID == msg.deleted.ID {
				m.workspaceInfos = append(m.workspaceInfos[:i], m.workspaceInfos[i+1:]...)
				break
			}
		}
		if p.screen != nil && p.screen == m.launcherScreen {
			p.screen.RemoveWorkspace(msg.deleted.ID)
		}
		p.set("Workspace deleted", msg.deleted.Name, "Project files are unchanged. Saved data can be restored.", "Recovery location:", msg.deleted.RecoveryPath)
		p.buttons(ui.ActionBarButton{Label: "Undo", Action: "undo", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Done", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		m.Feedback.NotifyInfo("Workspace deleted; recovery data: " + msg.deleted.RecoveryPath)
	}
	if fileio.WriteCommitted(msg.err) {
		m.Feedback.NotifyError(msg.err.Error())
	}
	if msg.err == nil || fileio.WriteCommitted(msg.err) {
		m.refreshLauncherAsync(m.ws.RootPath())
	}
	m.refreshWorkspaceDelete(p)
}
