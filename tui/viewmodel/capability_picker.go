package viewmodel

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/capabilitypicker"
	"crude/tui/ui"
	"crude/tui/wm"
	captypes "crude/types/capability"
	"crude/workspace"
)

// capabilityPickerOwner is one open Capabilities modal. It is bound to the
// session selected when it opened: its switches change only that session's
// capability gate (shared by the session's root and child agents).
type capabilityPickerOwner struct {
	screen     *capabilitypicker.Screen
	session    *workspace.Session
	runtime    shellport.Workspace
	generation uint64
	sequence   uint64
	cancel     context.CancelFunc
}

// capabilityToggleOutcome is one committed (or failed) session switch change.
type capabilityToggleOutcome struct {
	id     string
	result workspace.SessionSettingsResult
}

type capabilityPickerResultMsg struct {
	owner    *capabilityPickerOwner
	sequence uint64
	snapshot captypes.Snapshot
	err      error
	toggle   *capabilityToggleOutcome
}

func (app *App) openCapabilityPicker() tea.Cmd {
	return app.openCapabilityPickerTab(captypes.KindSkill, "")
}

func (app *App) openCapabilityPickerTab(kind captypes.Kind, query string) tea.Cmd {
	if app.capabilityPicker != nil {
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
	}
	owner := &capabilityPickerOwner{screen: capabilitypicker.New(app.Theme, app.capabilitySnapshot), session: app.ActiveSession, runtime: app.ws, generation: app.workspaceSwitchGen.Load()}
	owner.screen.SetCategory(kind)
	owner.screen.SetQuery(query)
	app.syncCapabilityPickerSession(owner)
	app.capabilityPicker = owner
	width, height := fitFloatingSize(app.width, app.height, 84, 34, 40, 14)
	// The body paints its own heading ("Skills · <session>"); the border
	// carries no title so it does not crowd the top edge.
	owner.screen.SetSessionLabel(app.capabilityPickerSessionLabel(owner.session))
	title := "Skills"
	if kind == captypes.KindMCP {
		title = "MCP servers"
	}
	frame := ui.NewWindowFrameWidget(app.Theme, "capability-picker/frame", ui.FrameWindow, "", owner.screen, nil, true, width, height)
	owner.screen.SetFocusHandler(frame.Focus)
	window := wm.NewFloatingWindow(string(ui.OverlayCapabilityPicker), title, frame, width, height, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, app.width, app.height)
	app.overlayHost.Open(ui.OverlayCapabilityPicker, window, ui.ModalDialogPolicy(), func() {
		if owner.cancel != nil {
			owner.cancel()
		}
		if app.capabilityPicker == owner {
			app.capabilityPicker = nil
		}
	})
	frame.Host().MarkLayout()
	frame.Host().Resize(width, height)
	frame.Host().Layout()
	owner.screen.FocusSearch()
	return app.capabilityPickerCmd(owner)
}

// capabilityPickerSessionLabel names the owning session by its root agent.
func (app *App) capabilityPickerSessionLabel(session *workspace.Session) string {
	if session == nil || app.ws == nil || app.ws.AgentMgr() == nil {
		return ""
	}
	if node := app.ws.AgentMgr().Get(session.AgentID); node != nil {
		return strings.TrimSpace(node.Label)
	}
	return ""
}

// syncCapabilityPickerSession installs the owning session's current gate.
func (app *App) syncCapabilityPickerSession(owner *capabilityPickerOwner) {
	if owner == nil {
		return
	}
	session := owner.session
	live := session != nil && app.ws != nil && app.ws.SessionsByID()[session.SessionID] == session
	var disabled []string
	if live && session.Settings != nil {
		disabled = session.Settings.Effective.SessionDisabledCapabilities
	}
	owner.screen.SetSession(live, disabled)
}

// refreshCapabilityPickerSession follows a settings change of the session
// that owns the open modal (another shell, the root agent, or a reset).
func (app *App) refreshCapabilityPickerSession(session *workspace.Session) {
	if owner := app.capabilityPicker; owner != nil && owner.session == session {
		app.syncCapabilityPickerSession(owner)
		app.invalidateCapabilityPicker()
	}
}

func (app *App) capabilityPickerCmd(owner *capabilityPickerOwner) tea.Cmd {
	if owner.cancel != nil {
		owner.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	owner.cancel = cancel
	owner.sequence++
	sequence, workspaceOwner := owner.sequence, app.ws
	return func() tea.Msg {
		defer cancel()
		snapshot, err := workspaceOwner.CapabilitySnapshot(ctx)
		return capabilityPickerResultMsg{owner: owner, sequence: sequence, snapshot: snapshot, err: err}
	}
}

// capabilityToggleCmd commits one switch to the owning session. Toggles are
// idempotent sets, never cancelled by a later refresh or another toggle.
func (app *App) capabilityToggleCmd(owner *capabilityPickerOwner, id string, enabled bool) tea.Cmd {
	fail := func(err error) tea.Cmd {
		return func() tea.Msg {
			return capabilityPickerResultMsg{owner: owner, sequence: owner.sequence, err: err, toggle: &capabilityToggleOutcome{id: id, result: workspace.SessionSettingsResult{Err: err}}}
		}
	}
	if owner.session == nil || owner.runtime == nil {
		return fail(errors.New("no session owns this window"))
	}
	operation, err := owner.runtime.PrepareSessionCapabilityToggle(owner.session.SessionID, id, enabled)
	if err != nil {
		return fail(err)
	}
	sequence := owner.sequence
	return func() tea.Msg {
		result := operation.Run()
		return capabilityPickerResultMsg{owner: owner, sequence: sequence, err: result.Err, toggle: &capabilityToggleOutcome{id: id, result: result}}
	}
}

func (app *App) handleCapabilityPickerAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "capability-picker.") || app.capabilityPicker == nil {
		return nil, false
	}
	owner := app.capabilityPicker
	switch action.ID {
	case "capability-picker.close":
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
	case "capability-picker.manage":
		target, _ := action.Payload.(capabilitypicker.ManageTarget)
		view := activitySkills
		if target.Kind == captypes.KindMCP {
			view = activityMCP
		}
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
		return app.openCapabilityManager(view, "", target.ID), true
	case "capability-picker.toggle":
		if owner.generation != app.workspaceSwitchGen.Load() {
			return nil, true
		}
		id, enabled, ok := owner.screen.BeginToggle(action.Payload)
		if ok {
			app.invalidateCapabilityPicker()
			return app.capabilityToggleCmd(owner, id, enabled), true
		}
	}
	return nil, true
}

func (app *App) invalidateCapabilityPicker() {
	if window := app.overlayHost.Window(ui.OverlayCapabilityPicker); window != nil {
		window.Content.(*ui.WindowFrameWidget).Host().MarkLayout()
		window.MarkContentDirty()
	}
}

func (app *App) applyCapabilityPickerResult(message capabilityPickerResultMsg) {
	owner := message.owner
	if outcome := message.toggle; outcome != nil {
		// The session commit is authoritative whether or not this modal is
		// still open; installing it is revision-fenced.
		if owner.runtime == app.ws && owner.generation == app.workspaceSwitchGen.Load() && outcome.result.Err == nil && outcome.result.ResultOwner() != nil {
			if app.ws.ApplySessionSettingsResult(outcome.result) {
				app.refreshSessionSettingsProjection(outcome.result.ResultOwner())
			}
		}
		if app.capabilityPicker != owner {
			return
		}
		app.syncCapabilityPickerSession(owner)
		owner.screen.FinishToggle(outcome.id, outcome.result.Err)
		app.invalidateCapabilityPicker()
		return
	}
	if app.capabilityPicker == nil || app.capabilityPicker != owner || owner.sequence != message.sequence || owner.generation != app.workspaceSwitchGen.Load() {
		return
	}
	owner.cancel = nil
	app.applyObservedCapabilitySnapshot(message.snapshot, message.err, true)
	owner.screen.SetSnapshot(app.capabilitySnapshot)
	app.invalidateCapabilityPicker()
}
