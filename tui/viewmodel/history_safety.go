package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/localhistory"
	"crude/shellport"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const historySafetyConfirmOverlay ui.OverlayID = "history-safety-confirm"

type historySafetyView struct {
	owner      shellport.Workspace
	port       shellport.HistorySafety
	generation uint64
	screen     *historyscreen.SafetyScreen
	page       workspace.HistorySafetyPage
	detail     bool
	sequence   uint64
	cancel     context.CancelFunc
	confirm    *historySafetyConsent
}

type historySafetyConsent struct {
	view    *historySafetyView
	detail  workspace.HistorySafetyDetail
	request workspace.HistorySafetyChangeRequest
}

type historySafetyReadMsg struct {
	view     *historySafetyView
	sequence uint64
	kind     string
	page     workspace.HistorySafetyPage
	detail   workspace.HistorySafetyDetail
	plan     localhistory.RestorePlan
	err      error
}

type historySafetyOperation struct {
	owner      shellport.Workspace
	port       shellport.HistorySafety
	generation uint64
	request    workspace.HistorySafetyChangeRequest
	busy       bool
	requestID  string
	detail     string
	cancel     context.CancelFunc
	run        tea.Cmd
}

type historySafetyResultMsg struct {
	operation *historySafetyOperation
	result    workspace.HistorySafetyChangeResult
	err       error
}

func (m *App) historySafetyVisible(v *historySafetyView) bool {
	if v == nil || m.historySafety != v || v.owner != m.ws || v.generation != m.workspaceSwitchGen.Load() ||
		m.asyncIOStopped.Load() || m.overlayHost == nil {
		return false
	}
	fw := m.overlayHost.Window(ui.OverlayLocalHistory)
	if fw == nil {
		return false
	}
	frame, ok := fw.Content.(*ui.WindowFrameWidget)
	return ok && frame.Body() == v.screen
}

func (m *App) mountHistoryBody(body ui.Component) {
	if m.overlayHost == nil {
		return
	}
	fw := m.overlayHost.Window(ui.OverlayLocalHistory)
	if fw == nil {
		return
	}
	width, height := fw.Size()
	frame := ui.NewWindowFrameWidget(m.Theme, ui.NewID(ui.RootID, "local-history-frame"),
		ui.FrameWindow, "Local History", body, nil, true, width, height)
	frame.SetMarkContentDirty(fw.MarkContentDirty)
	fw.SetContent(frame)
	m.forceFrame()
}

func (m *App) closeHistorySafetyView() {
	if v := m.historySafety; v != nil {
		v.sequence++
		if v.cancel != nil {
			v.cancel()
		}
		if m.overlayHost != nil {
			m.overlayHost.Close(historySafetyConfirmOverlay)
		}
	}
	m.historySafety = nil
}

func (m *App) openHistorySafety() tea.Cmd {
	port, ok := m.ws.(shellport.HistorySafety)
	if !ok || m.historyScreen == nil || m.historyOperation != nil {
		return nil
	}
	m.closeHistorySafetyView()
	v := &historySafetyView{owner: m.ws, port: port, generation: m.workspaceSwitchGen.Load(), screen: historyscreen.NewSafety(m.Theme)}
	m.historySafety = v
	m.mountHistoryBody(v.screen)
	if op := m.historySafetyOperation; op != nil && op.owner == m.ws && op.generation == v.generation {
		v.screen.SetPending(op.requestID, op.detail)
		v.screen.SetBusy(op.busy)
		m.markHistorySafetyDirty()
		return nil
	}
	return m.historySafetyRead(v, "page", "")
}

func (m *App) markHistorySafetyDirty() {
	if m.overlayHost != nil {
		if fw := m.overlayHost.Window(ui.OverlayLocalHistory); fw != nil {
			if framed, ok := fw.Content.(*ui.WindowFrameWidget); ok && framed.ComponentWidget != nil {
				framed.Host().MarkLayout()
			}
			fw.MarkContentDirty()
		}
	}
	m.forceFrame()
}

func (m *App) historySafetyRead(v *historySafetyView, kind, key string) tea.Cmd {
	if !m.historySafetyVisible(v) || m.historySafetyOperation != nil {
		return nil
	}
	if v.cancel != nil {
		v.cancel()
	}
	v.sequence++
	sequence := v.sequence
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	v.cancel = cancel
	v.screen.SetLoading("Reading original review safety evidence…")
	m.markHistorySafetyDirty()
	// All owners/options are captured before leaving Update. The existing
	// client-I/O owner cancels and joins reads at workspace switch/shutdown.
	port, store := v.port, v.owner.LocalHistory()
	options := historySnapshotOptions(v.owner.LoadAppSettings().LocalHistory)
	return m.clientIOCommand(ctx, func(ctx context.Context) tea.Msg {
		defer cancel()
		msg := historySafetyReadMsg{view: v, sequence: sequence, kind: kind}
		switch kind {
		case "page":
			msg.page, msg.err = port.ListHistorySafety(ctx, workspace.HistorySafetyListRequest{Cursor: key, Limit: 100})
		case "inspect":
			msg.detail, msg.err = port.InspectHistorySafety(ctx, key)
		case "restore":
			if store == nil {
				msg.err = errors.New("local history is unavailable")
			} else {
				msg.plan, msg.err = store.PlanRestore(ctx, key, options)
			}
		}
		return msg
	})
}

func (m *App) applyHistorySafetyRead(msg historySafetyReadMsg) tea.Cmd {
	v := msg.view
	if !m.historySafetyVisible(v) || msg.sequence != v.sequence {
		return nil
	}
	v.cancel = nil
	var uncertain *coreclient.UncertainError
	knownCleanup := errors.As(msg.err, &uncertain) && uncertain.ResultKnown
	if msg.err != nil && !knownCleanup {
		v.screen.SetError(msg.err.Error())
	} else {
		switch msg.kind {
		case "page":
			v.page, v.detail = msg.page, false
			v.screen.SetPage(msg.page)
		case "inspect":
			v.detail = true
			v.screen.SetDetail(msg.detail)
		case "restore":
			m.closeHistorySafetyView()
			m.mountHistoryBody(m.historyScreen)
			if fw := m.overlayHost.Window(ui.OverlayLocalHistory); fw != nil {
				return m.forwardEventToWindowContent(fw, historyscreen.PlanMsg{Plan: msg.plan})
			}
		}
		if knownCleanup {
			m.Feedback.NotifyError("Review safety evidence was returned, but its exchange cleanup is unconfirmed.")
		}
	}
	m.markHistorySafetyDirty()
	return nil
}

func (m *App) handleHistorySafetyAction(action *ui.Action) tea.Cmd {
	if action == nil {
		return nil
	}
	if action.ID == historyscreen.ActionSafetyOpen {
		return m.openHistorySafety()
	}
	v := m.historySafety
	if !m.historySafetyVisible(v) || v.confirm != nil {
		return nil
	}
	key, _ := action.Payload.(string)
	switch action.ID {
	case historyscreen.ActionSafetyInspect:
		return m.historySafetyRead(v, "inspect", key)
	case historyscreen.ActionSafetyNext:
		return m.historySafetyRead(v, "page", key)
	case historyscreen.ActionSafetyRefresh:
		return m.historySafetyRead(v, "page", "")
	case historyscreen.ActionSafetyRestore:
		return m.historySafetyRead(v, "restore", key)
	case historyscreen.ActionSafetyBack:
		if m.historySafetyOperation == nil && v.detail {
			v.sequence++
			if v.cancel != nil {
				v.cancel()
				v.cancel = nil
			}
			v.detail = false
			v.screen.SetPage(v.page)
			m.markHistorySafetyDirty()
		} else {
			m.closeHistorySafetyView()
			m.mountHistoryBody(m.historyScreen)
		}
	case historyscreen.ActionSafetyRelease, historyscreen.ActionSafetyReprotect:
		m.confirmHistorySafety(action.ID == historyscreen.ActionSafetyRelease)
	case historyscreen.ActionSafetyCheck:
		return m.historySafetyCheck(key)
	}
	return nil
}

func (m *App) confirmHistorySafety(release bool) {
	v := m.historySafety
	if !m.historySafetyVisible(v) || m.historySafetyOperation != nil || v.confirm != nil {
		return
	}
	detail, ok := v.screen.SelectedDetail()
	if !ok || detail.InspectionDigest == "" || release && !detail.CanRelease || !release && !detail.CanReprotect {
		v.screen.SetBusy(false)
		return
	}
	action, title, label, severity := "reprotect", "Keep safety protected", "Keep protected again", ui.ConfirmPromptQuestion
	explanation := "Protect this original snapshot again. This does not restore or change project files."
	if release {
		action, title, label, severity = "release", "Release safety protection", "Release protection", ui.ConfirmPromptDanger
		explanation = "This does not change files or delete the snapshot now. Ordinary retention may collect it later. Other protections still apply. Re-protection is possible only while the original snapshot remains."
	}
	consent := &historySafetyConsent{view: v, detail: detail, request: workspace.HistorySafetyChangeRequest{
		Reference: detail.Reference, SnapshotID: detail.SnapshotID, OperationID: detail.Preview.ID,
		OperationDigest: detail.Preview.Digest, ExpectedRevision: detail.Revision,
		InspectionDigest: detail.InspectionDigest, Action: action,
	}}
	v.confirm = consent
	v.screen.SetBusy(true)
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: title, Question: label + "?", Severity: severity,
		Detail: fmt.Sprintf("Reference: %s\nSnapshot: %s\nOriginal outcome: %s\n%s",
			detail.Reference, detail.SnapshotID, detail.OriginalState, explanation),
		Width: min(76, max(36, m.width-4)), FocusedIndex: 1,
		Buttons: []ui.ConfirmButtonSpec{{Label: label, ActionID: ui.ConfirmActionYes}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}},
	})
	w, h := frame.Measure(m.width, m.height)
	w, h = min(w, max(1, m.width)), min(h, max(1, m.height))
	fw := wm.NewFloatingWindow(string(historySafetyConfirmOverlay), "", frame, w, h,
		wm.WithMovable(true), wm.WithResizable(false), wm.WithClosable(false), wm.WithModal(true),
		wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.RegisterContentHandler(historySafetyConfirmOverlay, func(_ *ui.OverlayHost, win *wm.FloatingWindow, x, y int, _ tea.Msg) tea.Cmd {
		if choice := confirmFrameButtonAt(win, x, y); choice >= 0 {
			return m.resolveHistorySafetyConsent(choice == 0)
		}
		return nil
	})
	m.overlayHost.RegisterHoverHandler(historySafetyConfirmOverlay, func(_ *ui.OverlayHost, win *wm.FloatingWindow, x, y int, _ tea.MouseButton) {
		if widget, ok := win.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(widget, confirmFrameButtonAt(win, x, y))
		}
	})
	policy := ui.ModalDialogPolicy()
	policy.Movable = true
	m.overlayHost.Open(historySafetyConfirmOverlay, fw, policy, func() {
		if v.confirm == consent {
			v.confirm = nil
			v.screen.SetBusy(false)
			m.markHistorySafetyDirty()
		}
	})
	m.forceFrame()
}

func (m *App) resolveHistorySafetyConsent(approve bool) tea.Cmd {
	v := m.historySafety
	if !m.historySafetyVisible(v) || v.confirm == nil {
		return nil
	}
	consent := v.confirm
	current, ok := v.screen.SelectedDetail()
	top := m.overlayHost.Top()
	valid := ok && current.Reference == consent.detail.Reference && current.InspectionDigest == consent.detail.InspectionDigest &&
		top != nil && top.ID == historySafetyConfirmOverlay && consent.view == v
	m.overlayHost.Close(historySafetyConfirmOverlay)
	if !approve || !valid || m.historySafetyOperation != nil {
		return nil
	}
	op := &historySafetyOperation{owner: v.owner, port: v.port, generation: v.generation,
		request: consent.request, busy: true, detail: "Changing this exact safety protection. Do not repeat."}
	m.historySafetyOperation = op
	v.screen.SetPending("", op.detail)
	v.screen.SetBusy(true)
	m.markHistorySafetyDirty()
	return m.historySafetyCommand(op, func(ctx context.Context) historySafetyResultMsg {
		result, err := op.port.ChangeHistorySafetyProtection(ctx, op.request)
		return historySafetyResultMsg{operation: op, result: result, err: err}
	})
}

func (m *App) handleHistorySafetyConfirmInput(msg tea.Msg) (bool, tea.Cmd) {
	v := m.historySafety
	if v == nil || v.confirm == nil || m.overlayHost == nil {
		return false, nil
	}
	top := m.overlayHost.Top()
	if top == nil || top.ID != historySafetyConfirmOverlay {
		return false, nil
	}
	widget, ok := top.Window.Content.(*ui.ComponentWidget)
	if !ok {
		return true, m.resolveHistorySafetyConsent(false)
	}
	switch key := msg.(type) {
	case tea.KeyPressMsg:
		m.forceFrame()
		switch key.Code {
		case tea.KeyEsc:
			return true, m.resolveHistorySafetyConsent(false)
		case tea.KeyEnter:
			return true, m.resolveHistorySafetyConsent(ui.ConfirmButtonIndexFromID(widget.Host().Focused()) == 0)
		case tea.KeyLeft, tea.KeyUp:
			ui.MoveConfirmFocus(widget, -1, 2)
		case tea.KeyRight, tea.KeyDown, tea.KeyTab:
			delta := 1
			if key.Code == tea.KeyTab && key.Mod.Contains(tea.ModShift) {
				delta = -1
			}
			ui.MoveConfirmFocus(widget, delta, 2)
		}
		return true, nil
	case tea.PasteMsg, tea.MouseWheelMsg:
		return true, nil
	}
	return false, nil
}

func (m *App) historySafetyCommand(op *historySafetyOperation, work func(context.Context) historySafetyResultMsg) tea.Cmd {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	op.cancel = cancel
	var once sync.Once
	var result historySafetyResultMsg
	op.run = func() tea.Msg { once.Do(func() { defer cancel(); result = work(ctx) }); return result }
	return op.run
}

func (m *App) historySafetyCheck(id string) tea.Cmd {
	op := m.historySafetyOperation
	if op == nil || op.busy || id == "" || op.requestID != id || op.owner != m.ws ||
		op.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	op.busy = true
	if m.historySafetyVisible(m.historySafety) {
		m.historySafety.screen.SetBusy(true)
		m.markHistorySafetyDirty()
	}
	return m.historySafetyCommand(op, func(ctx context.Context) historySafetyResultMsg {
		msg := historySafetyResultMsg{operation: op}
		receipt, err := op.port.HistorySafetyReceipt(ctx, id)
		if err != nil {
			msg.err = err
			return msg
		}
		if receipt.RequestID != id || receipt.Method != coreprotocol.HistorySafetyChange || receipt.State != "completed" {
			msg.err = errors.New("original safety disposition is not a confirmed completed receipt; do not repeat")
			return msg
		}
		var out coreprotocol.HistorySafetyReply
		if receipt.Response.Error != nil {
			msg.err = receipt.Response.Error
		} else if msg.err = coreprotocol.Decode(receipt.Response.Payload, &out); msg.err == nil {
			msg.result = out.Result
			if msg.result.State == "unconfirmed" {
				// A matching append-only event is positive metadata commitment
				// evidence. Absence of one is never a no-effect verdict.
				detail, readErr := op.port.InspectHistorySafety(ctx, op.request.Reference)
				if readErr == nil && detail.LastEvent != nil && detail.LastEvent.Change.RequestID == id {
					msg.result.State, msg.result.Event, msg.result.Error = "committed", *detail.LastEvent, ""
				}
			}
		}
		return msg
	})
}

func (m *App) applyHistorySafetyResult(msg historySafetyResultMsg) tea.Cmd {
	op := msg.operation
	if op == nil || m.historySafetyOperation != op || op.owner != m.ws || op.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	op.busy = false
	result := msg.result
	exact := result.Reference == op.request.Reference && result.SnapshotID == op.request.SnapshotID && result.RequestID != "" &&
		(op.requestID == "" || result.RequestID == op.requestID)
	committed := confirmsHistorySafetyCommit(op, result)
	var uncertain *coreclient.UncertainError
	if committed || exact && result.State == "refused" {
		m.historySafetyOperation = nil
		if result.State == "refused" {
			m.Feedback.NotifyError(result.Error)
		} else {
			m.Feedback.NotifyInfo("Safety protection updated. No project files or snapshots were deleted.")
		}
		if msg.err != nil {
			m.Feedback.NotifyError("The original protection result is known; exchange cleanup needs inspection: " + msg.err.Error())
		}
		if m.historySafetyVisible(m.historySafety) {
			return m.historySafetyRead(m.historySafety, "inspect", op.request.Reference)
		}
		return nil
	}
	switch {
	case errors.As(msg.err, &uncertain):
		if op.requestID == "" {
			op.requestID = uncertain.RequestID
		}
		op.detail = "Check the original protection request; do not repeat: " + msg.err.Error()
	case exact && result.State == "unconfirmed":
		op.requestID, op.detail = result.RequestID, result.Error
	case op.requestID != "":
		op.detail = "Original protection outcome is still unresolved. Do not repeat."
		if msg.err != nil {
			op.detail += " " + msg.err.Error()
		}
	default:
		m.historySafetyOperation = nil
		op.detail = "Protection was not confirmed. Inspect the reference again before a new decision."
		if msg.err != nil {
			op.detail += " " + msg.err.Error()
		}
	}
	if m.historySafetyVisible(m.historySafety) {
		if op.requestID != "" {
			m.historySafety.screen.SetPending(op.requestID, op.detail)
		} else {
			m.historySafety.screen.SetError(op.detail)
		}
		m.markHistorySafetyDirty()
	} else {
		m.Feedback.NotifyError(op.detail)
	}
	return nil
}

func confirmsHistorySafetyCommit(op *historySafetyOperation, result workspace.HistorySafetyChangeResult) bool {
	return op != nil && result.Reference == op.request.Reference && result.SnapshotID == op.request.SnapshotID &&
		result.RequestID != "" && (op.requestID == "" || result.RequestID == op.requestID) &&
		result.State == "committed" && !result.SnapshotDeleted && result.Event.ID != "" &&
		result.Event.Reference == op.request.Reference && result.Event.SnapshotID == op.request.SnapshotID &&
		result.Event.Action == op.request.Action && result.Event.Revision == op.request.ExpectedRevision+1 &&
		result.Event.Change.Reference == op.request.Reference && result.Event.Change.SnapshotID == op.request.SnapshotID &&
		result.Event.Change.Action == op.request.Action && result.Event.Change.ExpectedRevision == op.request.ExpectedRevision &&
		result.Event.Change.RequestID == result.RequestID && result.Event.Change.Evidence.OperationID == op.request.OperationID &&
		result.Event.Change.Evidence.OperationDigest == op.request.OperationDigest
}

// StopAsyncIO calls this only after the program loop has stopped. Observe the
// original once-guarded mutation to completion, never start a replacement.
func (m *App) stopHistorySafetyOperation() {
	m.closeHistorySafetyView()
	if op := m.historySafetyOperation; op != nil && op.cancel != nil {
		op.cancel()
		if op.run != nil {
			op.run()
		}
	}
}
